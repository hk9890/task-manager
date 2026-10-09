// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// treeNode is one issue placed under its parent, with the two derived marks the
// engine computes: ready, and blocked with the open blockers behind it.
type treeNode struct {
	issue     *tasks.Issue
	ready     bool
	blocked   bool
	blockedBy []tasks.Ref
	children  []*treeNode
}

// buildTree nests the open issues under their parents. With rootID it returns
// that issue alone, holding its open descendants; without, every issue whose
// parent is absent from the open set is a root.
//
// ready and blocked are read from the engine's own views rather than derived
// here, so the marks cannot disagree with `taskmgr ready` and `taskmgr blocked`.
func buildTree(s *tasks.Store, rootID string) ([]*treeNode, error) {
	open, err := s.List(tasks.Filter{})
	if err != nil {
		return nil, err
	}
	ready, err := s.List(tasks.Filter{Expr: "ready"})
	if err != nil {
		return nil, err
	}
	blocked, err := s.Blocked()
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]*treeNode, len(open))
	byParent := make(map[string][]*treeNode)
	for _, iss := range open {
		n := &treeNode{issue: iss}
		nodes[iss.ID] = n
		byParent[iss.Parent] = append(byParent[iss.Parent], n)
	}
	for _, iss := range ready {
		if n, ok := nodes[iss.ID]; ok {
			n.ready = true
		}
	}
	for _, b := range blocked {
		if n, ok := nodes[b.Issue.ID]; ok {
			n.blocked = true
			n.blockedBy = b.BlockedBy
		}
	}

	// The engine rejects an issue that is its own parent but not a longer parent
	// cycle, so seen is what ends the walk on one.
	seen := make(map[string]bool, len(open))
	var attach func(n *treeNode)
	attach = func(n *treeNode) {
		seen[n.issue.ID] = true
		for _, c := range byParent[n.issue.ID] {
			if !seen[c.issue.ID] {
				n.children = append(n.children, c)
				attach(c)
			}
		}
	}

	if rootID != "" {
		root, ok := nodes[rootID]
		if !ok {
			// A closed issue can still hold open children.
			iss, err := s.Get(rootID)
			if err != nil {
				return nil, err
			}
			root = &treeNode{issue: iss}
		}
		attach(root)
		return []*treeNode{root}, nil
	}

	var roots []*treeNode
	for _, iss := range open {
		if _, hasOpenParent := nodes[iss.Parent]; !hasOpenParent {
			n := nodes[iss.ID]
			roots = append(roots, n)
			attach(n)
		}
	}
	// What is still unseen sits on a parent cycle and has no root of its own.
	for _, iss := range open {
		if !seen[iss.ID] {
			n := nodes[iss.ID]
			roots = append(roots, n)
			attach(n)
		}
	}
	return roots, nil
}

// treeMark is the derived state of a node: "ready", "blocked by <ids>",
// "blocked" when no blocker resolves, or "" when the graph says neither.
func treeMark(n *treeNode) string {
	switch {
	case n.ready:
		return "ready"
	case n.blocked && len(n.blockedBy) > 0:
		ids := make([]string, len(n.blockedBy))
		for i, r := range n.blockedBy {
			ids[i] = r.ID
		}
		return "blocked by " + strings.Join(ids, ", ")
	case n.blocked:
		return "blocked"
	}
	return ""
}

func printTreeText(nodes []*treeNode, depth int) {
	for _, n := range nodes {
		i := n.issue
		line := fmt.Sprintf("%s%s  %s  P%d  %s  %s", strings.Repeat("  ", depth), i.ID, i.Status, i.Priority, i.Type, i.Title)
		if mark := treeMark(n); mark != "" {
			line += "  [" + mark + "]"
		}
		_, _ = fmt.Fprintln(stdout, line)
		printTreeText(n.children, depth+1)
	}
}

// mermaidEscaper keeps a title from ending its quoted label or being read as markup.
var mermaidEscaper = strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;")

// mermaidID is an issue ID as a Mermaid node ID. Mermaid reads a leading keyword
// before a dash ("end-", "graph-", "class-") as the keyword, so the one dash an
// ID holds becomes an underscore, which the ID grammar never uses.
func mermaidID(id string) string {
	return strings.Replace(id, "-", "_", 1)
}

func mermaidNode(id, title string, facts ...string) string {
	return fmt.Sprintf("  %s[\"%s: %s (%s)\"]", mermaidID(id), id, mermaidEscaper.Replace(title), strings.Join(facts, ", "))
}

// printTreeMermaid prints the tree as a Mermaid flowchart: a solid edge from a
// parent to each child, a dotted "blocks" edge from an open blocker to what it
// holds. A blocker outside the printed tree is declared too, so no edge points
// at a node without a label.
func printTreeMermaid(roots []*treeNode) {
	declared := make(map[string]bool)
	var parentEdges, blockerNodes, blockerEdges []string
	_, _ = fmt.Fprintln(stdout, "graph TD")

	var declare func(nodes []*treeNode)
	declare = func(nodes []*treeNode) {
		for _, n := range nodes {
			i := n.issue
			facts := []string{string(i.Type), string(i.Status)}
			if mark := treeMark(n); mark != "" {
				facts = append(facts, mark)
			}
			_, _ = fmt.Fprintln(stdout, mermaidNode(i.ID, i.Title, facts...))
			declared[i.ID] = true
			for _, c := range n.children {
				parentEdges = append(parentEdges, fmt.Sprintf("  %s --> %s", mermaidID(i.ID), mermaidID(c.issue.ID)))
			}
			declare(n.children)
		}
	}
	declare(roots)

	var collectBlockers func(nodes []*treeNode)
	collectBlockers = func(nodes []*treeNode) {
		for _, n := range nodes {
			for _, r := range n.blockedBy {
				if !declared[r.ID] {
					declared[r.ID] = true
					blockerNodes = append(blockerNodes, mermaidNode(r.ID, r.Title, string(r.Type), string(r.Status)))
				}
				blockerEdges = append(blockerEdges, fmt.Sprintf("  %s -.->|blocks| %s", mermaidID(r.ID), mermaidID(n.issue.ID)))
			}
			collectBlockers(n.children)
		}
	}
	collectBlockers(roots)

	for _, lines := range [][]string{blockerNodes, parentEdges, blockerEdges} {
		for _, l := range lines {
			_, _ = fmt.Fprintln(stdout, l)
		}
	}
}

type treeDTO struct {
	issueDTO
	Ready    bool      `json:"ready"`
	Blocked  bool      `json:"blocked"`
	Children []treeDTO `json:"children,omitempty"`
}

func toTreeDTOs(store string, nodes []*treeNode) []treeDTO {
	out := make([]treeDTO, len(nodes))
	for i, n := range nodes {
		out[i] = treeDTO{issueDTO: toIssueDTO(store, n.issue), Ready: n.ready, Blocked: n.blocked}
		if len(n.children) > 0 {
			out[i].Children = toTreeDTOs(store, n.children)
		}
	}
	return out
}

var treeFlags struct {
	format string
}

var treeCmd = &cobra.Command{
	Use:   "tree [id]",
	Short: "Print open issues nested under their parents, marked ready or blocked",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch treeFlags.format {
		case "text", "mermaid":
		default:
			return fmt.Errorf("invalid --format value %q: must be one of text, mermaid", treeFlags.format)
		}
		if flagJSON && treeFlags.format != "text" {
			return fmt.Errorf("--format %s cannot be combined with --json", treeFlags.format)
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		rootID := ""
		if len(args) == 1 {
			rootID = args[0]
		}
		roots, err := buildTree(s, rootID)
		if err != nil {
			return err
		}
		switch {
		case flagJSON:
			return printJSON(toTreeDTOs(s.Name(), roots))
		case treeFlags.format == "mermaid":
			printTreeMermaid(roots)
		case len(roots) == 0:
			_, _ = fmt.Fprintln(stdout, "(none)")
		default:
			printTreeText(roots, 0)
		}
		return nil
	},
}

func init() {
	treeCmd.Flags().StringVar(&treeFlags.format, "format", "text", "output format: text|mermaid")
	rootCmd.AddCommand(treeCmd)
}
