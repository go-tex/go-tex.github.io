// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"path"
	"strings"
)

// The workspace's file verbs: create, delete, rename/move.
//
// # Why they live here and not in the widget
//
// [toolkit.FileTree] already offers the Explorer's affordances — New File, New
// Folder, F2 to rename in place with validation, Delete with a confirmation, a
// context menu, a row dragged onto a folder, files dropped from outside — and it
// changes NOTHING itself: it hands the host one FileRequest and waits for the
// host to apply it and set Entries. These are that host side. The split is the
// right one, because what a change costs is not a widget's business: a delete
// here is a worker round-trip that can fail, and a rename is two of them.
//
// # The capability was already there
//
// `RemoveFile` exists on [gitBackend] and in all three implementations, with the
// worker op, the dispatch and the session behind it, and tests at every layer.
// What it did not have was a caller: nothing a person could press reached it.
// These verbs are that caller.
//
// # What a delete must not forget
//
// Dropping the path from the working tree is the easy half. The file also has an
// edit buffer and may have a tab, and a buffer that outlived its file is not
// inert: [gitView.flushDirty] writes every dirty buffer back before a stage or a
// commit, so a deleted file whose buffer survived would be RECREATED by the next
// commit — the delete would appear to work and then quietly undo itself. So the
// buffer and the tab go with it, in that order: the tab first, because closing
// the active tab stashes the live editor text into the buffer on its way out, and
// the buffer is deleted after that stash rather than before it.

// workspaceNameRefusal explains why name cannot be a workspace path, or "" when
// it can.
//
// The widget validates a typed name before it ever becomes a FileRequest, and
// says so in place. This is not that check repeated for show: these verbs are
// also reached by the headless workspace tests and by import, where no widget is
// in the loop, and a host that trusted its own UI to be the only caller would be
// one refactor away from writing "../../etc/passwd" into a repository.
func workspaceNameRefusal(name string) string {
	switch {
	case name == "":
		return "a file needs a name"
	case strings.ContainsAny(name, `\`):
		return "“" + name + "” holds a backslash; paths here are separated by /"
	case strings.HasPrefix(name, "/"):
		return "“" + name + "” starts at the root; a workspace path is relative"
	case path.Clean(name) != name:
		// Catches "a/../b", "./a", "a//b" and the trailing slash, all of which
		// name something other than what they appear to.
		return "“" + name + "” is not a plain path"
	case name == "." || name == ".." || strings.HasPrefix(name, "../"):
		return "“" + name + "” leaves the workspace"
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "“" + name + "” holds a control character"
		}
	}
	return ""
}

// createFile adds an empty file to the workspace and opens it.
//
// It is [gitView.importFile] with no bytes, deliberately: a created file and an
// imported one are the same thing to everything downstream — a buffer, a tab, a
// render, a stage — and giving creation its own notion of a new file would be a
// second one to keep in step. The no-overwrite guarantee comes from the same
// [uniqueImportName], so "main.tex" created beside an existing one becomes
// "main (2).tex" rather than destroying work that has never been committed.
//
// It returns the name the file took, or "" when it was refused.
func (v *gitView) createFile(name string) string {
	if why := workspaceNameRefusal(name); why != "" {
		v.errMsg.Set("Not created: " + why)
		return ""
	}
	final := uniqueImportName(v.workspaceNames(), name)
	if final == "" {
		v.errMsg.Set("Not created: too many files already called “" + name + "”")
		return ""
	}
	v.stashActive()
	v.buffers[final] = &fileBuffer{}
	v.loaded.Set(final)
	v.addTab(final)
	v.s.SetSource("")
	v.notice.Set("Created " + final)
	v.errMsg.Set("")
	return final
}

// deleteFile removes path from the working tree and from the workspace's own
// state, and reports the error.
//
// A path the tree does not list but a buffer holds — a created or imported file
// that has never been written back — is dropped locally without troubling the
// backend: there is nothing there to remove, and a worker round-trip that
// answered "not-exist" would turn a correct delete into an error message.
func (v *gitView) deleteFile(p string, done func(error)) {
	if p == "" {
		v.fail(errNoGitPath, done)
		return
	}
	if !v.treeLists(p) {
		if _, held := v.buffers[p]; !held {
			v.fail(errNoGitPath, done)
			return
		}
		v.forgetLocal(p)
		v.notice.Set("Deleted " + p)
		v.refresh()
		if done != nil {
			done(nil)
		}
		return
	}
	v.beginOp("Deleting")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.RemoveFile(p, func(files []string, err error) {
		v.endOp()
		if err != nil {
			v.errMsg.Set(gitErrorMessage(err))
		} else {
			v.setFiles(files)
			v.forgetLocal(p)
			v.refreshStatus()
			v.notice.Set("Deleted " + p)
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// forgetLocal drops p's tab and then its edit buffer.
//
// ⛔ The order is the point. [gitView.closeTab] stashes the live editor text into
// the active file's buffer as it activates a neighbour, so a buffer deleted first
// is recreated a moment later by the very call that was meant to close it — and
// that resurrected buffer is dirty, which means the next commit writes the
// deleted file back.
func (v *gitView) forgetLocal(p string) {
	v.closeTab(p)
	delete(v.buffers, p)
}

// renameFile moves from to to, and reports the error.
//
// # Write the new one first
//
// A rename is two operations over a protocol that can fail between them, so one
// of the two orders has to be chosen deliberately. Writing the new path before
// removing the old one means a failure in between leaves TWO files where there
// was one; removing first would mean a failure leaves NONE. Duplication is
// visible, nameable and repairable by the person who caused it; a lost file in a
// workspace that is the only copy until a commit is not. So: write, then remove,
// and say plainly in the error which half got through.
func (v *gitView) renameFile(from, to string, done func(error)) {
	if from == "" || to == "" {
		v.fail(errNoGitPath, done)
		return
	}
	if from == to {
		if done != nil {
			done(nil)
		}
		return
	}
	if why := workspaceNameRefusal(to); why != "" {
		v.errMsg.Set("Not renamed: " + why)
		v.refresh()
		if done != nil {
			done(errNoGitPath)
		}
		return
	}
	if v.workspaceNames()[to] {
		// NOT suffixed to a free name the way an import is. An import has no
		// opinion about its name and a rename is nothing but an opinion about
		// one: answering "main.tex" with "main (2).tex" would be ignoring what
		// the person just typed.
		v.errMsg.Set("Not renamed: “" + to + "” already exists")
		v.refresh()
		if done != nil {
			done(errNoGitPath)
		}
		return
	}
	content, ok := v.renameSource(from)
	if !ok {
		v.fail(errNoGitPath, done)
		return
	}
	active := v.loaded.Get() == from

	// A file that exists only as a buffer needs no backend at all: renaming it is
	// renaming a map key, and the write-back that follows will use the new name.
	if !v.treeLists(from) {
		v.adoptRenamed(from, to, content, active)
		v.notice.Set("Renamed to " + to)
		v.refresh()
		if done != nil {
			done(nil)
		}
		return
	}

	v.beginOp("Renaming")
	v.errMsg.Set("")
	v.notice.Set("")
	v.refresh()
	v.backend.WriteFiles(map[string]string{to: content}, func(err error) {
		if err != nil {
			v.finishOp(err, done)
			return
		}
		v.backend.RemoveFile(from, func(files []string, err error) {
			v.endOp()
			if err != nil {
				// Both halves are reported, because "renaming failed" would send the
				// person looking for one file when the workspace now holds two.
				v.errMsg.Set("“" + to + "” was written but “" + from + "” could not be removed: " +
					gitErrorMessage(err))
			} else {
				v.setFiles(files)
				v.adoptRenamed(from, to, content, active)
				v.refreshStatus()
				v.notice.Set("Renamed to " + to)
			}
			if done != nil {
				done(err)
			}
			v.refresh()
		})
	})
}

// renameSource is the content to carry across: the live buffer when there is
// one, else the committed file.
func (v *gitView) renameSource(p string) (string, bool) {
	if c, ok := v.bufferContent(p); ok {
		return c, true
	}
	data, err := v.backend.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// adoptRenamed moves the workspace's own state from one name to the other: the
// new path gets the content, the old path's tab and buffer go, and the new path
// is opened when the old one was what the editor was showing.
func (v *gitView) adoptRenamed(from, to, content string, active bool) {
	v.buffers[to] = &fileBuffer{text: content}
	v.forgetLocal(from)
	if !active {
		v.addTab(to)
		return
	}
	v.loaded.Set(to)
	v.addTab(to)
	v.s.SetSource(content)
}

// GitCreateFile adds an empty file to the workspace and makes it the active one
// (the workspace column's New File drives it). It returns the name the file
// took, which is not always the one asked for: a collision suffixes rather than
// overwrites. A no-op while a network op is in flight or with no repository.
func (s *State) GitCreateFile(name string) string {
	v := s.git
	if v.busy.Get() || !v.backend.HasRepo() {
		return ""
	}
	final := v.createFile(name)
	v.refresh()
	return final
}

// GitDeleteFile removes path from the workspace and the working tree (the
// workspace column's Delete drives it, once the person has confirmed in the
// tree). A no-op while a network op is in flight or with no repository.
func (s *State) GitDeleteFile(path string, done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	v.deleteFile(path, done)
}

// GitRenameFile renames or moves from to to — the same verb, since a new name
// holding a slash is a move (the workspace column's Rename and its
// drag-onto-a-folder both drive it). A no-op while a network op is in flight or
// with no repository.
func (s *State) GitRenameFile(from, to string, done func(err error)) {
	v := s.git
	if v.busy.Get() {
		return
	}
	if !v.backend.HasRepo() {
		v.fail(errNoGitRepo, done)
		return
	}
	v.renameFile(from, to, done)
}
