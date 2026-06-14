// Command bench measures how the .tasks file store scales, and quantifies the
// payoff of two proposed changes: moving comments to append-only sidecars, and
// cold-partitioning closed issues into a closed/ subdir.
//
// It is a standalone module (its own go.mod, replace onto ../sdk) so it never
// enters the CLI module's dependency graph, `go build ./...`, or `make test`.
//
// Usage:
//
//	go run .                      # synthetic corpus (default -n 419, ~90% closed)
//	go run . -n 2000              # bigger synthetic corpus
//	go run . -jsonl PATH          # import a real issues.jsonl export instead
//	go run . -mode scaling        # only the current-design scaling phases
//	go run . -mode redesign       # only the sidecar + closed/ payoff phases
//
// Findings on a 419-issue real corpus (dtctl-test) are recorded in README.md.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
	"gopkg.in/yaml.v3"
)

func main() {
	var (
		jsonlPath = flag.String("jsonl", "", "path to a real issues.jsonl export to import (default: synthetic)")
		n         = flag.Int("n", 419, "number of synthetic issues when -jsonl is empty")
		mode      = flag.String("mode", "all", "all | scaling | redesign | yaml")
		workdir   = flag.String("workdir", "", "scratch dir for stores (default: a temp dir)")
	)
	flag.Parse()

	recs := load(*jsonlPath, *n)
	idset := map[string]bool{}
	nClosed := 0
	for _, r := range recs {
		idset[r.ID] = true
		if mapStatus(r.Status) == tasks.StatusClosed {
			nClosed++
		}
	}
	fmt.Printf("corpus: %d issues (%d closed, %d active)\n", len(recs), nClosed, len(recs)-nClosed)
	src := "synthetic"
	if *jsonlPath != "" {
		src = *jsonlPath
	}
	fmt.Printf("source: %s\n\n", src)

	work := *workdir
	if work == "" {
		d, err := os.MkdirTemp("", "atbench-")
		must(err)
		defer os.RemoveAll(d)
		work = d
	}

	switch *mode {
	case "scaling":
		scaling(recs, idset, work)
	case "redesign":
		redesign(recs, idset, work)
	case "yaml":
		yamlProbe()
	default:
		scaling(recs, idset, work)
		redesign(recs, idset, work)
		yamlProbe()
	}
}

// ---- PHASE GROUP 1: current-design scaling ----

func scaling(recs []rec, idset map[string]bool, work string) {
	st, root := freshStore(work, "scaling", "dtt")
	dir := st.Dir()

	var sizes []int
	var total, escaped, block int
	t0 := time.Now()
	for _, r := range recs {
		data, err := tasks.Marshal(issueOf(r, idset))
		must(err)
		must(os.WriteFile(filepath.Join(dir, r.ID+".md"), data, 0o644))
		sizes = append(sizes, len(data))
		total += len(data)
		s := string(data)
		if strings.Contains(s, `\n`) {
			escaped++
		}
		if strings.Contains(s, "body: |") {
			block++
		}
	}
	sort.Ints(sizes)
	fmt.Println("== SCALING: current design (comments in frontmatter, flat dir) ==")
	fmt.Printf("import:            %v (%.3f ms/issue)\n", time.Since(t0), ms(time.Since(t0))/float64(len(recs)))
	fmt.Printf("on-disk bytes:     %s\n", human(total))
	fmt.Printf("size med/p90/max:  %s / %s / %s\n", human(sizes[len(sizes)/2]), human(sizes[len(sizes)*9/10]), human(sizes[len(sizes)-1]))
	fmt.Printf("YAML-escaped (\\n): %d/%d files   block-scalar: %d\n", escaped, len(recs), block)

	st2, _ := tasks.Open(root)
	fmt.Printf("All() scan:        %v  (runs inside the lock on every Create/Update/AddDep)\n", scanAll(st2, 20))

	// at-y1x: AddComment/Update reject closed issues (immutable), so the
	// write-latency loops below must target only OPEN ids — the synthetic
	// corpus is ~90% closed and an unfiltered random pick frequently fatals.
	var ids []string
	for _, r := range recs {
		if mapStatus(r.Status) != tasks.StatusClosed {
			ids = append(ids, r.ID)
		}
	}
	var ac, up time.Duration
	const reps = 30
	prio := 2
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < reps; i++ {
		t := time.Now()
		_, err := st2.AddComment(ids[rng.Intn(len(ids))], "b", "tiny")
		must(err)
		ac += time.Since(t)
	}
	for i := 0; i < reps; i++ {
		t := time.Now()
		_, err := st2.Update(ids[rng.Intn(len(ids))], tasks.UpdateInput{Priority: &prio})
		must(err)
		up += time.Since(t)
	}
	fmt.Printf("AddComment (O(1)): %v/op    Update (O(N) checkRefs): %v/op  (%.1fx)\n",
		ac/reps, up/reps, float64(up)/float64(ac))

	fmt.Println("concurrent writers (shared store, real flock), 800 AddComment ops:")
	for _, w := range []int{1, 4, 8, 16} {
		const ops = 800
		per := ops / w
		var wg sync.WaitGroup
		var done int64
		t := time.Now()
		for k := 0; k < w; k++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				r := rand.New(rand.NewSource(int64(seed)))
				for i := 0; i < per; i++ {
					if _, err := st2.AddComment(ids[r.Intn(len(ids))], "load", "probe"); err == nil {
						atomic.AddInt64(&done, 1)
					}
				}
			}(k*7919 + 1)
		}
		wg.Wait()
		d := time.Since(t)
		fmt.Printf("  workers=%2d  wall=%9v  %6.0f ops/s\n", w, d, float64(done)/d.Seconds())
	}

	fmt.Println("Create latency as the store grows (campaign O(N) slope):")
	st3, _ := freshStore(work, "slope", "agt")
	mark := map[int]bool{50: true, 100: true, 200: true, 400: true, 800: true}
	cap := 800
	if len(recs) < 400 {
		cap = 400
		mark[len(recs)] = true
	}
	for i := 1; i <= cap; i++ {
		t := time.Now()
		_, err := st3.Create(tasks.CreateInput{Title: fmt.Sprintf("issue %d", i), Description: strings.Repeat("body ", 40)})
		must(err)
		if mark[i] {
			fmt.Printf("  Create #%4d  %v\n", i, time.Since(t))
		}
	}
	fmt.Println()
}

// ---- PHASE GROUP 2: redesign payoff ----

func redesign(recs []rec, idset map[string]bool, work string) {
	fmt.Println("== REDESIGN: sidecar comments + closed/ partition ==")

	// A: current
	stA, rootA := freshStore(work, "A", "dtt")
	writeCorpus(stA.Dir(), recs, idset)
	bA, nA := dirBytes(stA.Dir())
	fmt.Printf("A) current (inline comments, flat):     hot=%d files %s  All()=%v\n", nA, human(bA), scanAll(open(rootA), 20))

	// B: sidecar comments
	stB, rootB := freshStore(work, "B", "dtt")
	side := filepath.Join(stB.Dir(), "comments")
	os.MkdirAll(side, 0o755)
	writeCorpus(stB.Dir(), recs, idset)
	var sb, sc int
	for _, r := range recs {
		if len(r.Comments) > 0 {
			var b strings.Builder
			for _, c := range r.Comments {
				fmt.Fprintf(&b, "<!-- @%s %s -->\n%s\n\n", c.Author, c.Created, strings.TrimSpace(c.Text))
			}
			os.WriteFile(filepath.Join(side, r.ID+".comments.md"), []byte(b.String()), 0o644)
			sb += b.Len()
			sc++
		}
	}
	bB, nB := dirBytes(stB.Dir())
	fmt.Printf("B) sidecar comments:                    hot=%d files %s  All()=%v  (sidecars: %d files %s, cold)\n",
		nB, human(bB), scanAll(open(rootB), 20), sc, human(sb))

	// C: sidecar + closed/
	stC, rootC := freshStore(work, "C", "dtt")
	closed := filepath.Join(stC.Dir(), "closed")
	os.MkdirAll(closed, 0o755)
	var openR, closedR []rec
	for _, r := range recs {
		if mapStatus(r.Status) == tasks.StatusClosed {
			closedR = append(closedR, r)
		} else {
			openR = append(openR, r)
		}
	}
	writeCorpus(stC.Dir(), openR, idset)
	writeCorpus(closed, closedR, idset)
	bC, nC := dirBytes(stC.Dir())
	cb, cn := dirBytes(closed)
	fmt.Printf("C) sidecar + closed/ (hot=open only):   hot=%d files %s  All()=%v  (closed/: %d files %s, cold)\n",
		nC, human(bC), scanAll(open(rootC), 20), cn, human(cb))

	// confirm All() already ignores the closed/ subdir, and show the nextID reuse risk
	all, _ := open(rootC).All()
	leaked := false
	for _, i := range all {
		if i.Status == tasks.StatusClosed {
			leaked = true
		}
	}
	fmt.Printf("   All() over C returned %d issues; closed leaked in: %v (subdir is skipped, no SDK change)\n", len(all), leaked)
	if res, err := open(rootC).Create(tasks.CreateInput{Title: "probe"}); err == nil {
		fmt.Printf("   nextID saw %d hot files and allocated %s -> collision-resistant token, no closed/ reuse risk (at-2fb)\n", nC, res.Issue.ID)
	}

	// sidecar append vs whole-file rewrite
	big := "VERIFIED.\n\n```\n" + strings.Repeat("line with trailing ws   \n", 300) + "```\n"
	sp := filepath.Join(side, "append.comments.md")
	os.WriteFile(sp, []byte("seed\n"), 0o644)
	t := time.Now()
	f, _ := os.OpenFile(sp, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(big)
	f.Sync()
	f.Close()
	fmt.Printf("   append %s comment: sidecar O_APPEND+fsync=%v (fsync-bound; win is structural, not latency)\n\n", human(len(big)), time.Since(t))
}

// ---- PHASE GROUP 3: which comment bodies force YAML to escape ----

func yamlProbe() {
	fmt.Println("== YAML: comment-body shapes that force escaped single-line output ==")
	cases := []struct{ name, body string }{
		{"clean markdown", "## T\n\ntext\n- a\n"},
		{"tab-indented code", "code:\n\tx=1\n"},
		{"trailing space on a line", "text   \nmore\n"},
		{"code fence + trailing ws", "```\n$ cmd   \nout \n```\n"},
		{"CRLF endings", "a\r\nb\r\n"},
		{"whitespace-only line", "p\n   \nq\n"},
	}
	for _, c := range cases {
		out, _ := yaml.Marshal(map[string]string{"body": c.body})
		form := "double-quoted \\n (UGLY)"
		if strings.Contains(string(out), "body: |") {
			form = "block scalar | (readable)"
		}
		fmt.Printf("  %-28s -> %s\n", c.name, form)
	}
	fmt.Println()
}

// ---- helpers ----

// freshStore wipes work/name, re-Inits a store there with prefix, and returns
// both the store and the root path so callers can re-Open(root) byte-identically.
func freshStore(work, name, prefix string) (*tasks.Store, string) {
	root := filepath.Join(work, name)
	os.RemoveAll(root)
	st, err := tasks.Init(root, prefix)
	must(err)
	return st, root
}

// writeCorpus marshals each rec and writes <dir>/<ID>.md, mirroring the corpus
// layout used by the redesign() variants.
func writeCorpus(dir string, recs []rec, idset map[string]bool) {
	for _, r := range recs {
		d, _ := tasks.Marshal(issueOf(r, idset))
		os.WriteFile(filepath.Join(dir, r.ID+".md"), d, 0o644)
	}
}

type rec struct {
	ID, Title, Desc, Status, IssueType, Owner, Created, Updated, Closed, CloseR string
	Priority                                                                    int
	Labels                                                                      []string
	Comments                                                                    []struct{ Author, Text, Created string }
}

func load(path string, n int) []rec {
	if path == "" {
		return synth(n)
	}
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	var out []rec
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m struct {
			Type      string   `json:"_type"`
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Desc      string   `json:"description"`
			Status    string   `json:"status"`
			Priority  int      `json:"priority"`
			IssueType string   `json:"issue_type"`
			Owner     string   `json:"owner"`
			Labels    []string `json:"labels"`
			Created   string   `json:"created_at"`
			Updated   string   `json:"updated_at"`
			Closed    string   `json:"closed_at"`
			CloseR    string   `json:"close_reason"`
			Comments  []struct {
				Author  string `json:"author"`
				Text    string `json:"text"`
				Created string `json:"created_at"`
			} `json:"comments"`
		}
		if json.Unmarshal([]byte(line), &m) != nil || m.Type != "issue" || m.ID == "" {
			continue
		}
		r := rec{ID: m.ID, Title: m.Title, Desc: m.Desc, Status: m.Status, IssueType: m.IssueType,
			Owner: m.Owner, Created: m.Created, Updated: m.Updated, Closed: m.Closed, CloseR: m.CloseR,
			Priority: m.Priority, Labels: m.Labels}
		for _, c := range m.Comments {
			r.Comments = append(r.Comments, struct{ Author, Text, Created string }{c.Author, c.Text, c.Created})
		}
		out = append(out, r)
	}
	must(sc.Err())
	return out
}

// synth builds a corpus that mirrors a mature store: ~90% closed, multi-paragraph
// bodies, and some comments whose bodies carry trailing whitespace (to exercise
// the YAML escaping path). Deterministic for stable numbers.
func synth(n int) []rec {
	rng := rand.New(rand.NewSource(42))
	body := strings.Repeat("This is a realistic multi-paragraph issue body. ", 25)
	const base = "2026-05-26T17:00:00Z"
	out := make([]rec, 0, n)
	for i := 1; i <= n; i++ {
		status := "closed"
		if rng.Float64() < 0.10 {
			status = []string{"open", "in_progress", "blocked"}[rng.Intn(3)]
		}
		r := rec{
			ID: fmt.Sprintf("syn-%04d", i), Title: fmt.Sprintf("Synthetic issue %d", i),
			Desc: body, Status: status, IssueType: []string{"bug", "task", "chore"}[rng.Intn(3)],
			Owner: "bench", Created: base, Updated: base, Closed: base, CloseR: "done",
			Priority: rng.Intn(5), Labels: []string{"area:bench", fmt.Sprintf("k:%d", i%7)},
		}
		for c := 0; c < rng.Intn(4); c++ {
			text := "## Verification\n\n```\n" + strings.Repeat("observed output line   \n", 8+rng.Intn(20)) + "```\n"
			r.Comments = append(r.Comments, struct{ Author, Text, Created string }{"bench", text, base})
		}
		out = append(out, r)
	}
	return out
}

func issueOf(r rec, idset map[string]bool) *tasks.Issue {
	iss := &tasks.Issue{
		ID: r.ID, Title: r.Title, Status: mapStatus(r.Status), Type: mapType(r.IssueType),
		Priority: clampPrio(r.Priority), Assignee: r.Owner, Labels: r.Labels,
		Created: ts(r.Created), Updated: ts(r.Updated), CloseReason: r.CloseR, Description: r.Desc,
	}
	if iss.Status == tasks.StatusClosed {
		c := ts(r.Closed)
		if c.IsZero() {
			c = ts(r.Updated)
		}
		if c.IsZero() {
			c = ts("2026-05-26T17:00:00Z")
		}
		iss.Closed = c
	}
	if i := strings.LastIndex(r.ID, "."); i > 0 && idset[r.ID[:i]] {
		iss.Parent = r.ID[:i]
	}
	// Comments are stored in the sidecar (Issue has no Comments field since the
	// sidecar migration). Inline-comment benchmarking is no longer applicable.
	return iss
}

func mapStatus(s string) tasks.Status {
	switch s {
	case "closed":
		return tasks.StatusClosed
	case "in_progress":
		return tasks.StatusInProgress
	case "blocked", "deferred":
		return tasks.StatusBlocked
	default:
		return tasks.StatusOpen
	}
}

func mapType(s string) tasks.Type {
	switch s {
	case "bug":
		return tasks.TypeBug
	case "epic":
		return tasks.TypeEpic
	case "chore":
		return tasks.TypeChore
	case "feature":
		return tasks.TypeFeature
	default:
		return tasks.TypeTask
	}
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Second)
}

func clampPrio(p int) int {
	if p < 0 {
		return 0
	}
	if p > 4 {
		return 4
	}
	return p
}

func open(root string) *tasks.Store { st, err := tasks.Open(root); must(err); return st }

func scanAll(st *tasks.Store, reps int) time.Duration {
	var d time.Duration
	for i := 0; i < reps; i++ {
		t := time.Now()
		_, err := st.All()
		must(err)
		d += time.Since(t)
	}
	return d / time.Duration(reps)
}

func dirBytes(dir string) (int, int) {
	ents, _ := os.ReadDir(dir)
	tot, n := 0, 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fi, _ := e.Info()
		tot += int(fi.Size())
		n++
	}
	return tot, n
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
