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
// same rule for package entries in HOOK-SPEC §3.4). Until there was a writer, that
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
	setScalar(root, "prefix", cfg.Prefix)
	setScalar(root, "hook_timeout", cfg.HookTimeout)
	if err := setUse(root, cfg.Use, hasDefect(cfg.defects, useKey)); err != nil {
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
	setNode(root, "version", &yaml.Node{
		Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", version),
	})
	setScalar(root, "central_root", cfg.CentralRoot)
	setScalar(root, "hook_timeout", cfg.HookTimeout)
	if err := setUse(root, cfg.Use, hasDefect(cfg.defects, useKey)); err != nil {
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
func setNode(mapping *yaml.Node, key string, value *yaml.Node) {
	if i := findKey(mapping, key); i >= 0 {
		// Carry the old value's comments across: they annotate the setting, not
		// the value that happened to be there.
		value.HeadComment = mapping.Content[i].HeadComment
		value.LineComment = mapping.Content[i].LineComment
		value.FootComment = mapping.Content[i].FootComment
		mapping.Content[i] = value
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
}

// setScalar assigns a string value, or removes the key entirely when the value
// is empty — the `omitempty` behaviour of the struct tags, applied to a document
// that is being edited rather than regenerated.
func setScalar(mapping *yaml.Node, key, value string) {
	if value == "" {
		removeKey(mapping, key)
		return
	}
	setNode(mapping, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// setUse writes the `use:` sequence, reusing the original node of any entry
// whose modelled fields are unchanged.
//
// Reuse is what carries an entry's own unknown keys through an unrelated edit:
// adding one package must not strip a key a newer taskmgr wrote on another. An
// entry that did change is re-encoded and loses them, which is the honest
// outcome — the writer has no way to merge a field it cannot see.
//
// defective says the file's `use:` value is not a list at all, so nothing here
// models it. The key is then left exactly as it stands: rendering an empty model
// over it would delete a line the author wrote, from a write aimed at some
// unrelated key, which is the one thing this file exists to prevent.
func setUse(mapping *yaml.Node, refs []PackageRef, defective bool) error {
	if defective {
		return nil
	}
	if len(refs) == 0 {
		removeKey(mapping, useKey)
		return nil
	}

	var oldNodes []*yaml.Node
	if i := findKey(mapping, useKey); i >= 0 && mapping.Content[i].Kind == yaml.SequenceNode {
		oldNodes = mapping.Content[i].Content
	}
	used := make([]bool, len(oldNodes))

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, ref := range refs {
		if node := matchUseNode(oldNodes, used, ref); node != nil {
			seq.Content = append(seq.Content, node)
			continue
		}
		// An entry this build cannot model is written back from the node it was
		// read from, comment and all. Re-encoding it would round-trip through
		// text and lose both — and an entry yaml.v3 drops on its own (an empty
		// `-`) would come back as something else entirely, or vanish.
		if ref.raw != nil {
			seq.Content = append(seq.Content, ref.raw)
			continue
		}
		fresh := &yaml.Node{}
		if err := fresh.Encode(ref); err != nil {
			return err
		}
		seq.Content = append(seq.Content, fresh)
	}
	setNode(mapping, useKey, seq)
	return nil
}

// matchUseNode returns the first unused original node that decodes to exactly
// ref, marking it used. Matching by value rather than by index keeps reuse
// correct when entries are added, removed, or reordered.
func matchUseNode(oldNodes []*yaml.Node, used []bool, ref PackageRef) *yaml.Node {
	for i, node := range oldNodes {
		if used[i] {
			continue
		}
		var got PackageRef
		if err := node.Decode(&got); err != nil {
			continue
		}
		if got != ref {
			continue
		}
		used[i] = true
		return node
	}
	return nil
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
