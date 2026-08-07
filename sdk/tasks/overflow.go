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

// overflow.go — the body-overflow rule: what keeps every file in the hot
// directory small enough that a scan stays cheap no matter what a caller pastes
// into an issue.
//
// A body over MaxInlineBody is written to a content sidecar instead of into the
// .md, and the .md records body_external: true. This applies to EVERY issue
// type, not just doc — it is what bounds the cost of the hot scan (which reads
// whole files) by construction. TASK-STORAGE-SPEC §4.6.
//
// Pure core: the layout decision and the rendering of an issue into its two
// on-disk pieces are decided from a byte count and a bool, with no filesystem
// access. The disk half lives in content.go (the imperative shell).
package tasks

import "strings"

const (
	// MaxInlineBody is the largest body kept inside the .md file. A body over
	// this moves to the content sidecar. It is a fixed constant, not
	// configurable: one behaviour in every store, nothing to document per-repo,
	// and no way for two clones to disagree about what they will write.
	MaxInlineBody = 65536

	// joinInlineBody is the lower watermark: an external body returns inline only
	// once it drops below this, not the moment it falls under MaxInlineBody.
	//
	// The gap is deliberate. Without it, an issue hovering at the cap would flip
	// layout on every edit, and each flip moves the entire body between two files
	// — a maximal git diff in both directions, forever. With the gap, an issue
	// has to genuinely shrink before the layout changes again.
	joinInlineBody = 32768

	// MaxCommentBody is the hard limit on a single comment body. Unlike an issue
	// body it does NOT overflow — see validateCommentBody for why.
	MaxCommentBody = 65536

	// contentDirName holds the body sidecars. Like comments/ and closed/ it is a
	// subdirectory, so the hot scan (which ignores subdirectories) never sees it.
	contentDirName = "content"
)

// layoutFor reports whether a body belongs in the sidecar, given whether it is
// currently stored there. This is the hysteresis rule (TASK-STORAGE-SPEC §4.6):
// split above MaxInlineBody, re-join only below joinInlineBody, and keep the
// current layout in between.
func layoutFor(body string, prevExternal bool) bool {
	n := len(strings.TrimSpace(body))
	if prevExternal {
		return n >= joinInlineBody
	}
	return n > MaxInlineBody
}

// renderForWrite computes the on-disk representation of iss.
//
// It returns the bytes of the .md, the sidecar bytes to write BEFORE it (nil
// when the body stays inline), and whether a now-stale sidecar must be removed
// AFTER it. That ordering is the whole crash contract — see Store.writeFiles.
//
// iss is not modified: the flag is applied to a copy, so the caller's issue
// keeps a populated Description and a clear flag and stays safe to Marshal.
func renderForWrite(iss *Issue, prevExternal bool) (md, sidecar []byte, dropSidecar bool, err error) {
	external := layoutFor(iss.Description, prevExternal)

	out := *iss // shallow copy: Marshal only reads
	out.bodyExternal = external
	if external {
		sidecar = []byte(strings.TrimSpace(iss.Description))
		out.Description = ""
	} else {
		// Was external, is not any more: the sidecar is stale once the .md lands.
		dropSidecar = prevExternal
	}

	md, err = Marshal(&out)
	if err != nil {
		return nil, nil, false, err
	}
	return md, sidecar, dropSidecar, nil
}
