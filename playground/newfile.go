// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

// Creating a file in the workspace — the first of the three things #225 says a
// file manager does and this column could not: create, rename, delete.
//
// ⛔ IT NEEDED NOTHING NEW FROM THE WORKER, and that is worth saying because the
// obvious reading is that a new file needs a new RPC op. It does not: a created
// file is a buffer whose path the repository has never heard of, which is exactly
// what an imported file is, and #227 made that state mean "dirty" — so the
// existing write-back carries it into the worktree at the next stage or commit.
// Rename and delete are the two that genuinely need a new op, because removing a
// path cannot be expressed as writing one.

// newFileStem and newFileExt name a created file before anyone has renamed it.
//
// A created file is NOT left empty. An empty .tex renders as a blank page, which
// is indistinguishable from a file that failed to open, and the one thing a new
// file should do is look like itself. One comment line naming the file is valid
// under every class, adds nothing to the typeset output, and makes the file
// visibly new.
const (
	newFileStem = "untitled"
	newFileExt  = ".tex"
)

// newFileBody is what a created file starts as.
func newFileBody(name string) string {
	return "% " + name + " — created in the go-tex playground\n"
}

// createFile adds a new file to the workspace and opens it, returning the name it
// took, or "" when it could not be created.
//
// The name is generated rather than asked for: this column has no text entry of
// its own yet, and the honest order is to make creating a file possible first and
// naming it possible second (#225 puts rename third for that reason, since rename
// is what makes a generated name temporary rather than permanent).
//
// ⛔ It never overwrites, through the same uniqueImportName that import uses —
// one predicate for both, rather than two rules that can disagree about what
// "already taken" means. The workspace IS the only copy of an edited file until
// it is committed.
func (v *gitView) createFile() string {
	taken := v.workspaceNames()
	name := uniqueImportName(taken, newFileStem+newFileExt)
	if name == "" {
		v.errMsg.Set("Not created: too many files already called “" + newFileStem + newFileExt + "”")
		return ""
	}
	body := newFileBody(name)
	// Refused by the same policy that refuses an import, so a name this code
	// generates can never be one the workspace would have rejected from the host.
	// It also means a future rename has exactly one rule to satisfy.
	if why := importRefusal(name, []byte(body)); why != "" {
		v.errMsg.Set("Not created: " + why)
		return ""
	}

	v.stashActive()
	v.buffers[name] = &fileBuffer{text: body}
	v.loaded.Set(name)
	v.addTab(name)
	v.s.SetSource(body)
	v.notice.Set("Created " + name)
	v.errMsg.Set("")
	return name
}
