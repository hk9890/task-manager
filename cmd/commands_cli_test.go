//go:build integration

// L4 CLI tests for `taskmgr commands` — the machine-readable command catalog.
//
// Coverage:
//   - Output is valid YAML with the expected top-level shape.
//   - Every user-facing command is present, each with a purpose and an example.
//   - Derived metadata is accurate (required flags, positional placeholders).
//   - --json emits the same catalog as valid JSON.
package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type catFlag struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required"`
}

type catCommand struct {
	Name    string    `yaml:"name" json:"name"`
	Purpose string    `yaml:"purpose" json:"purpose"`
	Usage   string    `yaml:"usage" json:"usage"`
	Example string    `yaml:"example" json:"example"`
	Flags   []catFlag `yaml:"flags" json:"flags"`
}

type catalog struct {
	Binary      string       `yaml:"binary" json:"binary"`
	Description string       `yaml:"description" json:"description"`
	Commands    []catCommand `yaml:"commands" json:"commands"`
}

// commandsByName indexes a catalog by command name for assertions.
func commandsByName(c catalog) map[string]catCommand {
	m := make(map[string]catCommand, len(c.Commands))
	for _, cmd := range c.Commands {
		m[cmd.Name] = cmd
	}
	return m
}

// allUserFacingCommands is the full surface the catalog must cover (at-lwt).
var allUserFacingCommands = []string{
	"create", "import", "init", "show", "update", "close", "reopen",
	"dep", "dep add", "dep rm",
	"rel", "rel add", "rel rm",
	"comment", "comment add", "comment edit", "comment rm",
	"list", "search", "ready", "blocked", "labels", "statuses", "types",
	"version", "commands",
}

func TestL4_Commands_YAMLCatalog(t *testing.T) {
	root := t.TempDir() // commands needs no store, but the helper passes --dir.
	stdout, stderr, code := taskmgr(t, root, "commands")
	if code != 0 {
		t.Fatalf("commands exit=%d stderr=%q", code, stderr)
	}

	var cat catalog
	if err := yaml.Unmarshal([]byte(stdout), &cat); err != nil {
		t.Fatalf("output is not valid YAML: %v\n---\n%s", err, stdout)
	}
	if cat.Binary != "taskmgr" {
		t.Errorf("binary = %q, want %q", cat.Binary, "taskmgr")
	}

	byName := commandsByName(cat)

	// Every user-facing command present, each with a purpose and an example.
	for _, want := range allUserFacingCommands {
		cmd, ok := byName[want]
		if !ok {
			t.Errorf("command %q missing from catalog", want)
			continue
		}
		if strings.TrimSpace(cmd.Purpose) == "" {
			t.Errorf("command %q has empty purpose", want)
		}
		if strings.TrimSpace(cmd.Example) == "" {
			t.Errorf("command %q has empty example", want)
		}
		if !strings.HasPrefix(cmd.Example, "taskmgr "+want) {
			t.Errorf("command %q example %q does not start with %q", want, cmd.Example, "taskmgr "+want)
		}
	}

	// Derived accuracy: create marks --title required; show takes a positional id.
	if create, ok := byName["create"]; ok {
		var titleRequired bool
		for _, f := range create.Flags {
			if f.Name == "title" && f.Required {
				titleRequired = true
			}
		}
		if !titleRequired {
			t.Errorf("create catalog should mark --title required; flags=%+v", create.Flags)
		}
		if !strings.Contains(create.Example, "--title") {
			t.Errorf("create example should include --title, got %q", create.Example)
		}
	}
	if show, ok := byName["show"]; ok && !strings.Contains(show.Example, "<id>") {
		t.Errorf("show example should include the <id> placeholder, got %q", show.Example)
	}
}

func TestL4_Commands_JSON(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := taskmgr(t, root, "--json", "commands")
	if code != 0 {
		t.Fatalf("commands --json exit=%d stderr=%q", code, stderr)
	}
	var cat catalog
	if err := json.Unmarshal([]byte(stdout), &cat); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if cat.Binary != "taskmgr" {
		t.Errorf("binary = %q, want %q", cat.Binary, "taskmgr")
	}
	if len(cat.Commands) < len(allUserFacingCommands) {
		t.Errorf("json catalog has %d commands, want >= %d", len(cat.Commands), len(allUserFacingCommands))
	}
}
