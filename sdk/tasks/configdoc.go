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

// configdoc.go — rendering a config change back into an existing config.yaml
// without destroying what the file already says.
//
// Both config files are documented as forward-compatible: a reader ignores keys
// it does not know rather than rejecting them (TASK-STORAGE-SPEC §4.2, and the
// same rule for hook entries in HOOK-SPEC §3.4). Until there was a writer, that
// promise was free. Round-tripping the file through Config would break it: every
// unknown key, and every comment in a hand-edited file, would vanish on the first
// `taskmgr config set` — silently, and for a key a *newer* taskmgr put there.
//
// So a write edits the parsed document in place: it sets the keys the struct
// models and leaves every other node exactly as the author wrote it. Pure — it
// maps bytes to bytes and touches no filesystem.
package tasks

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// yamlIndent matches the encoder settings used everywhere else in the store, so
// a rewritten file does not reindent itself relative to the one init wrote.
const yamlIndent = 4

// applyConfigToDoc renders cfg into old, a store config.yaml (which may be empty
// for a new store), preserving unknown keys, key order, and comments.
func applyConfigToDoc(old []byte, cfg Config) ([]byte, error) {
	root, err := parseMappingDoc(old)
	if err != nil {
		return nil, err
	}
	if err := setScalar(root, "prefix", cfg.Prefix); err != nil {
		return nil, err
	}
	if err := setScalar(root, "hook_timeout", cfg.HookTimeout); err != nil {
		return nil, err
	}
	if err := setHooks(root, cfg.Hooks); err != nil {
		return nil, err
	}
	return encodeDoc(root)
}

// applyGlobalConfigToDoc is applyConfigToDoc for the per-user config
// (CONFIG-SPEC §2).
func applyGlobalConfigToDoc(old []byte, cfg GlobalConfig) ([]byte, error) {
	root, err := parseMappingDoc(old)
	if err != nil {
		return nil, err
	}
	version := cfg.Version
	if version == 0 {
		version = 1
	}
	if err := setNode(root, "version", &yaml.Node{
		Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", version),
	}); err != nil {
		return nil, err
	}
	if err := setScalar(root, "central_root", cfg.CentralRoot); err != nil {
		return nil, err
	}
	if err := setScalar(root, "hook_timeout", cfg.HookTimeout); err != nil {
		return nil, err
	}
	if err := setHooks(root, cfg.Hooks); err != nil {
		return nil, err
	}
	return encodeDoc(root)
}

// parseMappingDoc returns the mapping node at the root of data, or a fresh empty
// mapping when data holds no document (a new file). A document whose root is not
// a mapping is a parse error rather than something to overwrite: the file is not
// a config, and silently replacing it would lose whatever it is.
func parseMappingDoc(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse config: expected a YAML mapping at the top level")
	}
	return root, nil
}

// findKey returns the index of key's *value* node within a mapping's Content, or
// -1. Mapping content alternates key, value.
func findKey(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

// removeKey drops key and its value from a mapping, keeping everything else in
// place. It is what makes `config unset` remove the line rather than write an
// empty one.
func removeKey(mapping *yaml.Node, key string) {
	if i := findKey(mapping, key); i >= 0 {
		mapping.Content = append(mapping.Content[:i-1], mapping.Content[i+1:]...)
	}
}

// setNode assigns value to key, replacing the existing value node in place (so
// the key keeps its position and any comment attached to it) or appending the
// pair when the key is new.
func setNode(mapping *yaml.Node, key string, value *yaml.Node) error {
	if i := findKey(mapping, key); i >= 0 {
		// Carry the old value's comments across: they annotate the setting, not
		// the value that happened to be there.
		value.HeadComment = mapping.Content[i].HeadComment
		value.LineComment = mapping.Content[i].LineComment
		value.FootComment = mapping.Content[i].FootComment
		mapping.Content[i] = value
		return nil
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
	return nil
}

// setScalar assigns a string value, or removes the key entirely when the value
// is empty — the `omitempty` behaviour of the struct tags, applied to a document
// that is being edited rather than regenerated.
func setScalar(mapping *yaml.Node, key, value string) error {
	if value == "" {
		removeKey(mapping, key)
		return nil
	}
	return setNode(mapping, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// setHooks writes the hooks sequence, reusing the original node of any entry
// whose modelled fields are unchanged.
//
// Reuse is what carries an entry's own unknown keys through an unrelated edit:
// adding one hook must not strip a key a newer taskmgr wrote on another. An
// entry that did change is re-encoded and loses them, which is the honest
// outcome — the writer has no way to merge a field it cannot see.
func setHooks(mapping *yaml.Node, hooks []Hook) error {
	if len(hooks) == 0 {
		removeKey(mapping, "hooks")
		return nil
	}

	var oldNodes []*yaml.Node
	if i := findKey(mapping, "hooks"); i >= 0 && mapping.Content[i].Kind == yaml.SequenceNode {
		oldNodes = mapping.Content[i].Content
	}
	used := make([]bool, len(oldNodes))

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, h := range hooks {
		if node := matchHookNode(oldNodes, used, h); node != nil {
			seq.Content = append(seq.Content, node)
			continue
		}
		fresh := &yaml.Node{}
		if err := fresh.Encode(h); err != nil {
			return err
		}
		seq.Content = append(seq.Content, fresh)
	}
	return setNode(mapping, "hooks", seq)
}

// matchHookNode returns the first unused original node that decodes to exactly
// h, marking it used. Matching by value rather than by index keeps reuse correct
// when entries are added, removed, or reordered.
func matchHookNode(oldNodes []*yaml.Node, used []bool, h Hook) *yaml.Node {
	for i, node := range oldNodes {
		if used[i] {
			continue
		}
		var got Hook
		if err := node.Decode(&got); err != nil {
			continue
		}
		if !sameHook(got, h) {
			continue
		}
		used[i] = true
		return node
	}
	return nil
}

func sameHook(a, b Hook) bool {
	if a.ID != b.ID || a.Event != b.Event || a.When != b.When || len(a.Run) != len(b.Run) {
		return false
	}
	for i := range a.Run {
		if a.Run[i] != b.Run[i] {
			return false
		}
	}
	return true
}

func encodeDoc(root *yaml.Node) ([]byte, error) {
	var buf yamlBuffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// yamlBuffer is a minimal io.Writer sink; bytes.Buffer would do, but the package
// otherwise imports no bytes and this keeps the dependency surface flat.
type yamlBuffer struct{ b []byte }

func (w *yamlBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func (w *yamlBuffer) Bytes() []byte { return w.b }
