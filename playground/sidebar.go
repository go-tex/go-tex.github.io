// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// brandIndigo is the go-tex mark colour (the favicon tile), used for the topZone
// logo tile and wordmark (zones.go). It reads on both the light and dark surface.
var brandIndigo = toolkit.RGB(0x4F, 0x46, 0xE5)

// This file is the Git WORKSPACE SIDEBAR: a toggleable left column, disjoint
// from the editor+render body, that surfaces the cloned repository as a file
// tree, a row of git command buttons and a commit timeline. It is the read/act
// companion to the Remote-Git panel (git.go): the panel configures the remote
// and clones; this sidebar then browses the working tree, opens files into the
// editor, runs the basic git actions and shows the history.
//
// Everything here is composed from existing go-widgets/toolkit widgets — a
// TreeTable (file tree, with a per-file git-status badge column via the toolkit's
// new TreeTableNode.CellInk), a Timeline (commit history), an EmptyState (the
// no-repo prompt), Buttons and Labels, grounded on Backdrops exactly like zones.go
// — so it carries theming/HiDPI and passes bricolint with no hand-drawn UI. It is
// tagless and browser-free: the git plumbing it drives lives behind the gitBackend
// seam (git.go / gitworker.go), so the whole sidebar is covered by native tests.
//
// # File-open + dirty model (v2: independent per-file edit buffers)
//
// Clicking a file row opens it via State.GitOpenFile, which keeps an independent
// edit buffer per path (git.go): opening another file no longer discards the
// current file's unsaved edits, and SEVERAL files can be dirty at once. The single
// editor is bound to the ACTIVE file's buffer; switching files stashes the live
// edits and restores the target's buffer (text + caret + scroll). A row's badge
// combines the file's git status (browsergit classify: modified/staged/deleted/
// untracked) with its in-memory buffer dirtiness: a buffer that differs from the
// committed/indexed content badges "modified" (amber M) — for EVERY file, not just
// the active one — so a file edited then navigated away from stays dirty in the
// tree. Stage/Commit flush every dirty buffer to the working tree first (git.go's
// flushDirty), so browsergit's git-add-A captures them all.

// Sidebar geometry constants (logical px; scaled at layout time).
const (
	// sidebarW is the sidebar column's logical width when open.
	sidebarW = 264
	// sidebarBtnW is the toolbar toggle pill's logical width.
	sidebarBtnW = 92
	// sidebarTimelineH is the commit-timeline band's logical height at the bottom.
	sidebarTimelineH = 148
	// sidebarMinTreeH is the least logical height the file tree keeps; the
	// timeline yields its height first when the column is short.
	sidebarMinTreeH = 60
)

// errorInk is the detail strip's colour for a failure line.
//
// ⛔ The per-status badge palette that used to live here — amber, green, red,
// blue, with badgeFor mapping a browsergit status onto a letter and one of them
// — is GONE with the hand-built tree. The Explorer draws each entry's mark
// itself and picks its colour for CONTRAST: the first candidate that clears
// 4.5:1 against the row wins, with the row's own ink as the fallback. Fixed hues
// cannot make that promise, which is why they are not kept "for consistency":
// a mark nobody can read is a decoration. Only the error line's red stays, and
// it is not a status.
var errorInk = toolkit.RGBA{R: 0xC0, G: 0x30, B: 0x30, A: 0xFF}

// sidebarButton is one command control: a role for dispatch and the persistent
// Button it is drawn/hit-tested through.
type sidebarRole int

const (
	sbRoleNone sidebarRole = iota
	sbRoleRefresh
	sbRoleStage
	sbRoleCommit
	sbRolePull
	sbRolePush
	sbRoleForget
	sbRoleClone  // empty-state action: open the Remote-Git panel to clone
	sbRoleImport // take a file off this computer into the workspace
	sbRoleExport // hand the open file's current text back to this computer
)

// sidebarButton is a laid-out command button: its role, rect and label.
type sidebarButton struct {
	role  sidebarRole
	rect  toolkit.Rect
	label string
	// disabled is this button's OWN reason to be unavailable, independent of the
	// busy flag that greys the network commands. Import and Export have one the
	// others do not: off-wasm, and in any host that installs no file IO, there is
	// nothing on the other side of them — and a button that looks live and does
	// nothing is worse than one that is plainly out of reach.
	disabled bool
}

// sidebar is the workspace sidebar affordance. All display state is derived each
// layout from the git view's observables + snapshots (no per-frame field copy);
// the tree/timeline are rebuilt only when a cheap signature changes, so scrolling
// and selection survive between frames.
type sidebar struct {
	s    *State
	open bool

	// bounds is the whole column; sub-rects are recomputed by layout().
	bounds       toolkit.Rect
	detailRect   toolkit.Rect
	filesHdrRect toolkit.Rect
	histHdrRect  toolkit.Rect
	treeRect     toolkit.Rect
	tlRect       toolkit.Rect
	buttons      []sidebarButton

	// sig is the last tree/timeline signature; a change triggers a rebuild.
	sig string
	// commitDetail is the last-clicked commit's one-line detail (sha · subject ·
	// author), shown in the detail strip until the next git notice/error.
	commitDetail string

	// persistent toolkit widgets, built once and reused every frame.
	bg       *toolkit.Backdrop
	rule     *toolkit.Backdrop
	detail   *toolkit.Label
	filesExp *toolkit.Expander // "Files" accordion header (labelled with the active file) over the tree
	histExp  *toolkit.Expander // "History" accordion header (labelled with the branch) over the timeline
	// tree is the toolkit's Explorer, not a hand-built one. It carries New File,
	// New Folder, F2-rename with in-place validation, Delete behind a
	// confirmation, a context menu, a row dragged onto a folder and files dropped
	// from outside — and it draws the per-entry version-control mark this column
	// used to spell out in a second "S" column of its own. It changes nothing
	// itself: every verb arrives here as a FileRequest.
	tree     *toolkit.FileTree
	timeline *toolkit.Timeline
	empty    *toolkit.EmptyState
	// emptyRect is where layout put the empty state. draw MUST work from this
	// and never from b.empty.Bounds(): the busy branch moves the widget down and
	// shrinks it to make room for the spinner, and layout only runs on a resize —
	// so reading the widget back each frame compounds its own offset and walks
	// the whole prompt off the bottom of the column within a few frames. That is
	// what made a cloning workspace look simply blank.
	emptyRect toolkit.Rect
	spinner   *toolkit.Spinner     // shown while a clone is in flight (no repo yet)
	assetBar  *toolkit.ProgressBar // the git client's download, under the spinner
	btns      map[sidebarRole]*toolkit.Button
}

// newSidebar builds the affordance for s (OPEN by default, so the workspace is
// present on load — showing its EmptyState + Clone… until a repo is cloned). The
// toolbar toggle still lets the user close it to reclaim the full canvas width.
func newSidebar(s *State) *sidebar {
	return &sidebar{s: s, open: true}
}

// ensureWidgets lazily builds the persistent widgets. Called at the top of every
// draw / input path (the toolkit widgets need the theme at draw time, not here).
func (b *sidebar) ensureWidgets() {
	if b.bg != nil {
		return
	}
	b.bg = &toolkit.Backdrop{}
	b.rule = &toolkit.Backdrop{}
	b.detail = toolkit.NewLabel("")
	// The Explorer. There is no "S" column any more: the widget draws each entry's
	// version-control mark itself, which is what this column was hand-rolling a
	// second column for.
	b.tree = toolkit.NewFileTree(nil)
	b.tree.Label = "Project files"
	b.tree.Icon = func(name string, dir, open bool) func(painter.Painter, toolkit.Rect, toolkit.RGBA) {
		pack := iconPacks[b.s.iconPackIdx()]
		if dir {
			return pack.folderIconDrawer()
		}
		return pack.fileIconDrawer(name)
	}
	b.tree.OnRequest = b.onFileRequest
	// Accordion headers over the tree and the timeline, mutually exclusive: opening
	// one collapses the other (handleClick enforces it), so only one body shows at a
	// time and the workspace column stays compact. Files opens first. The widgets
	// are header-only (Content nil): the sidebar lays out and draws the
	// tree/timeline itself, gated on each header's Expanded state.
	b.filesExp = toolkit.NewExpander("Files", nil)
	b.filesExp.Expanded().Set(true)
	b.histExp = toolkit.NewExpander("History", nil)
	b.histExp.Expanded().Set(false)
	b.timeline = toolkit.NewTimeline(nil)
	b.spinner = toolkit.NewSpinner()
	b.spinner.Active().Set(true)
	b.assetBar = toolkit.NewProgressBar()
	b.empty = toolkit.NewEmptyState("No repository open").
		SetCaption("Clone one to browse its files here")
	b.btns = map[sidebarRole]*toolkit.Button{}
}

// btn returns the persistent Button for a role, creating it (wired to dispatch)
// on first use so its pressed/hover face survives between frames.
func (b *sidebar) btn(role sidebarRole, label string) *toolkit.Button {
	if bt := b.btns[role]; bt != nil {
		return bt
	}
	rr := role
	bt := toolkit.NewButton(label, func() { b.dispatch(rr) })
	b.btns[role] = bt
	return bt
}

// hasRepo reports whether a repository is open (a clone has succeeded).
func (b *sidebar) hasRepo() bool { return b.s.git.backend.HasRepo() }

// tick advances the loading spinner's animation by dt seconds. The host frame
// loop calls it (via State.Tick) while a clone is in flight.
func (b *sidebar) tick(dt float64) {
	b.ensureWidgets()
	b.spinner.Tick(dt)
}

// toggle flips the sidebar open/closed and repaints. Opening or closing the
// column changes the width left for the editor+render Paned by exactly the
// sidebar width; the divider is rescaled proportionally so the editor keeps its
// SHARE of the newly available width — otherwise the render pane would swallow
// the whole delta when the sidebar closes (leaving the editor stuck at the
// narrow, sidebar-open width) and yield it all back when it re-opens. The
// caller re-lays out after this; here we only adjust the absolute divider so
// that relayout preserves the ratio.
func (b *sidebar) toggle() {
	oldW := b.s.paned.Bounds().W
	b.open = !b.open
	if newW := b.s.w - b.width(); oldW > 0 && newW > 0 {
		pos := b.s.paned.Position()
		pos.Set(pos.Get() * newW / oldW)
	}
	b.s.git.refresh()
}

// activeDirty reports whether the active file's edit buffer differs from its
// committed working-tree content — the loaded-file case of the per-file dirtiness
// (git.go's bufferDirty). A read miss (uncached file) reports not-dirty.
func (b *sidebar) activeDirty() bool {
	return b.s.git.bufferDirty(b.s.git.loaded.Get())
}

// fileStatus is a working-tree path's state as the Explorer's vocabulary: its
// in-memory edit-buffer dirtiness takes precedence — for ANY file with a buffer,
// so a file edited then navigated away from still carries its mark — otherwise
// the file's git status from the last snapshot, otherwise unchanged.
//
// ⛔ The widget's states are NOT git's, deliberately: they are the vocabulary
// git, Mercurial, Subversion, Jujutsu and Fossil share, so the mapping belongs
// here, where what a commit is is known, and not in the widget. The one that
// needs saying is "staged", which this workspace writes through `git add`
// without committing: that is git's staged-new, which the shared vocabulary
// calls added.
func (b *sidebar) fileStatus(path string) toolkit.FileStatus {
	// ⛔ BEFORE the dirty check, not after. bufferDirty answers true for a path
	// the working tree does not list — it cannot read a committed version to
	// compare against — so a file created a moment ago would read as MODIFIED, a
	// state that means "tracked and edited" and so claims it is in the repository.
	// Untracked is both the truth and the more useful thing to show.
	if _, held := b.s.git.buffers[path]; held && !b.s.git.treeLists(path) {
		return toolkit.FileUntracked
	}
	if b.s.git.bufferDirty(path) {
		return toolkit.FileModified
	}
	for _, c := range b.s.git.status.Changes {
		if c.Path != path {
			continue
		}
		switch c.Status {
		case "modified":
			return toolkit.FileModified
		case "untracked":
			return toolkit.FileUntracked
		case "deleted":
			return toolkit.FileDeleted
		case "staged":
			return toolkit.FileAdded
		}
		return toolkit.FileUnchanged
	}
	return toolkit.FileUnchanged
}

// signature is a cheap digest of everything the tree + timeline render from, so
// a rebuild happens only on a real change (preserving scroll + selection between
// frames). It folds the file list, every git dirty entry, the active path, the
// per-file edit-buffer dirtiness of EVERY buffer (so a file that becomes or stops
// being dirty — active or not — triggers a rebuild), and the commit hashes.
func (b *sidebar) signature() string {
	var sb strings.Builder
	sb.WriteString(strings.Join(b.s.git.files, "\x1f"))
	sb.WriteByte('\x1e')
	for _, c := range b.s.git.status.Changes {
		sb.WriteString(c.Path)
		sb.WriteByte(':')
		sb.WriteString(c.Status)
		sb.WriteByte(',')
	}
	sb.WriteByte('\x1e')
	sb.WriteString(b.s.git.loaded.Get())
	sb.WriteByte('\x1e')
	for _, p := range b.s.GitBufferPaths() {
		sb.WriteString(p)
		if b.s.git.bufferDirty(p) {
			sb.WriteByte('*')
		}
		sb.WriteByte(',')
	}
	sb.WriteByte('\x1e')
	for _, c := range b.s.git.log {
		sb.WriteString(c.Hash)
		sb.WriteByte(',')
	}
	return sb.String()
}

// rebuild regenerates the file tree + timeline from the current git snapshot and
// re-selects the active file's node. Called only when signature() changes.
func (b *sidebar) rebuild() {
	setFileEntries(b.tree, b.workspaceEntries()) // widget-state write isolated in sidebar_binding.go
	// The highlight follows the open file. Selection is the PATH now, not a node,
	// so there is nothing to look up: the tree resolves it, and a path the list no
	// longer holds simply selects nothing.
	b.tree.Selected().Set(b.s.git.loaded.Get())
	setTimelineEvents(b.timeline, timelineEvents(b.s.git.log)) // isolated in sidebar_binding.go
}

// workspaceEntries is the working-tree list as the Explorer's entries, each
// carrying the mark its state earns.
//
// Folders are not listed: any path under one implies it, and an empty folder is
// the only case that would need an entry of its own — which a git working tree
// cannot have, because git tracks files and not directories.
func (b *sidebar) workspaceEntries() []toolkit.FileEntry {
	files := append([]string(nil), b.s.git.files...)
	sort.Strings(files)
	// A buffer for a file the tree does not list is a created or imported file
	// that has not been written back yet. It is part of the workspace and the
	// person must be able to see it, rename it and delete it, so it is listed —
	// untracked, which is exactly what it is.
	for _, p := range b.s.GitBufferPaths() {
		if !b.s.git.treeLists(p) {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	out := make([]toolkit.FileEntry, 0, len(files))
	for _, f := range files {
		out = append(out, toolkit.FileEntry{Path: f, Status: b.fileStatus(f)})
	}
	return out
}

// onFileRequest applies one Explorer request to the workspace.
//
// ⛔ The tree asks and never acts: it does not change its own entries, so each
// of these has to land in the store and come back through [sidebar.rebuild].
// That is why a request that fails leaves the row where it was rather than the
// row vanishing optimistically and reappearing on the error.
func (b *sidebar) onFileRequest(r toolkit.FileRequest) {
	switch r.Action {
	case toolkit.FileOpen:
		b.commitDetail = ""
		b.s.GitOpenFile(r.Path)
	case toolkit.FileCreate:
		b.s.GitCreateFile(r.Path)
	case toolkit.FileCreateDir:
		// A git working tree has no empty directories to create: a folder exists
		// because something is in it. Saying so is better than a New Folder that
		// appears to work and is gone on the next refresh.
		b.s.git.errMsg.Set("A folder appears when a file is put in it; create the file instead")
	case toolkit.FileRename, toolkit.FileMove:
		b.s.GitRenameFile(r.Path, r.To, nil)
	case toolkit.FileDelete:
		b.s.GitDeleteFile(r.Path, nil)
	case toolkit.FileImport:
		// The tree reports WHICH names were dropped; the host holds the bytes, and
		// in a browser they arrive through the picker rather than with the drop. So
		// a drop opens the same picker the Import button does — guarded the same
		// way, because a guard that lives only in how a button is painted is one
		// refactor from a nil call.
		if b.s.git.io.Import != nil {
			b.s.git.io.Import(b.s.git.importDelivered)
		}
	}
	b.s.git.refresh()
}

// timelineEvents converts the recent commits into Timeline events: the short
// hash + subject as the title, the author as the dim detail line.
func timelineEvents(log []GitCommitInfo) []toolkit.TimelineEvent {
	out := make([]toolkit.TimelineEvent, 0, len(log))
	for _, c := range log {
		title := shortHash(c.Hash)
		if c.Subject != "" {
			title += " " + c.Subject
		}
		out = append(out, toolkit.TimelineEvent{Title: title, Detail: c.Author, Kind: toolkit.TimelineDefault})
	}
	return out
}

// ⛔ buildFileTree is GONE, and that is the point of this change. It assembled a
// folder-grouped forest of TreeTable nodes with a second column for the status
// badge, and a leaf→path map to resolve a click through — about fifty lines
// reimplementing the front of toolkit.FileTree, which in exchange for them also
// carries rename, delete, move, drag-and-drop and the context menu this column
// never had. Its own doc names this exact trade: "a consumer with a repository
// to show has to hand-roll a file view to get these marks, and loses rename,
// delete, move, drag-and-drop and the context menu doing it."

// width is the sidebar's device-pixel width when open, 0 when closed. layout()
// (in app.go) reserves it so the editor+render body shrinks to the right.
func (b *sidebar) width() int {
	if !b.open {
		return 0
	}
	return toolkit.Scaled(sidebarW)
}

// syncModel rebuilds the tree + timeline when the git snapshot (files, statuses,
// active file + its live dirtiness, commits) changed since the last build, so an
// edit or a git op is reflected without a full re-layout. Cheap + idempotent when
// nothing changed, so it is safe to call every frame.
func (b *sidebar) syncModel() {
	b.ensureWidgets()
	if sig := b.signature(); sig != b.sig {
		b.sig = sig
		b.rebuild()
	}
}

// forceRebuild makes the next syncModel rebuild the tree even when the git
// snapshot is unchanged — used when the icon pack changes, which re-renders every
// row's icon without touching the file list.
func (b *sidebar) forceRebuild() { b.sig = "" }

// setBounds records the column rect and recomputes the sub-region layout,
// rebuilding the tree/timeline when the signature changed.
func (b *sidebar) setBounds(r toolkit.Rect) {
	b.bounds = r
	b.syncModel()
	b.layout()
}

// layout positions the header, button rows, detail strip, tree and timeline (or,
// with no repo, the empty state + Clone button) inside the column.
func (b *sidebar) layout() {
	r := b.bounds
	pad := toolkit.Scaled(8)
	gap := toolkit.Scaled(6)
	lineH := toolkit.Scaled(baseFontPx + 4)
	btnH := toolkit.Scaled(24)
	innerX := r.X + pad
	innerW := r.W - 2*pad
	if innerW < 1 {
		innerW = 1
	}

	b.buttons = b.buttons[:0]
	// The brand lockup now lives at the very top of the app (the topZone), not in
	// this column, so the workspace content starts right at the top padding.
	cur := r.Y + pad

	if !b.hasRepo() {
		// Empty state: centre the prompt in the remaining column, with a Clone
		// button beneath it.
		bodyY := cur
		bodyH := r.Y + r.H - pad - btnH - gap - bodyY
		if bodyH < 0 {
			bodyH = 0
		}
		b.emptyRect = toolkit.Rect{X: innerX, Y: bodyY, W: innerW, H: bodyH}
		b.empty.SetBounds(b.emptyRect)
		// ⛔ Import belongs in the EMPTY state, not only beside the git commands.
		// Cloning was the only way to put a document in this workspace, which made
		// "bring your own .tex" conditional on having a repository to bring it to —
		// the opposite of the order a user arrives in. The two sit side by side.
		cw := toolkit.Scaled(120)
		if 2*cw+gap > innerW {
			cw = (innerW - gap) / 2
		}
		if cw < 1 {
			cw = 1
		}
		rowY := r.Y + r.H - pad - btnH
		rowX := r.X + (r.W-(2*cw+gap))/2
		b.buttons = append(b.buttons,
			sidebarButton{
				role:  sbRoleClone,
				rect:  toolkit.Rect{X: rowX, Y: rowY, W: cw, H: btnH},
				label: "Clone…",
			},
			sidebarButton{
				role:     sbRoleImport,
				rect:     toolkit.Rect{X: rowX + cw + gap, Y: rowY, W: cw, H: btnH},
				label:    "Import…",
				disabled: b.s.git.io.Import == nil,
			},
		)
		b.treeRect = toolkit.Rect{}
		b.tlRect = toolkit.Rect{}
		b.detailRect = toolkit.Rect{}
		return
	}

	// Command buttons, wrapped into rows of up to three.
	cmds := []sidebarButton{
		{role: sbRoleRefresh, label: "Refresh"},
		{role: sbRoleStage, label: "Stage"},
		{role: sbRoleCommit, label: "Commit"},
		{role: sbRolePull, label: "Pull"},
		{role: sbRolePush, label: "Push"},
		// Drops the copy this browser keeps, so the next visit clones afresh.
		// It leaves the open workspace alone — see [State.GitForget].
		{role: sbRoleForget, label: "Forget"},
		// Off the network entirely: these two talk to the host machine, not the
		// remote, so they carry their own reason to be unavailable rather than the
		// busy flag's.
		{role: sbRoleImport, label: "Import…", disabled: b.s.git.io.Import == nil},
		{role: sbRoleExport, label: "Export…", disabled: b.s.git.io.Export == nil},
	}
	for start := 0; start < len(cmds); start += 3 {
		end := start + 3
		if end > len(cmds) {
			end = len(cmds)
		}
		row := cmds[start:end]
		bw := (innerW - (len(row)-1)*gap) / len(row)
		for i := range row {
			row[i].rect = toolkit.Rect{X: innerX + i*(bw+gap), Y: cur, W: bw, H: btnH}
			b.buttons = append(b.buttons, row[i])
		}
		cur += btnH + gap
	}

	// Detail strip (clicked-commit detail, or the latest git notice/error). It
	// reserves a line only when it has something to show — an idle column (the
	// common case while browsing files, since opening a file no longer posts a
	// notice) gives that height to the tree instead.
	if text, _ := b.detailText(); text != "" {
		b.detailRect = toolkit.Rect{X: innerX, Y: cur, W: innerW, H: lineH}
		cur += lineH + gap
	} else {
		b.detailRect = toolkit.Rect{}
	}

	// Two accordion sections stacked: a "Files" header over the tree, then a
	// "History" header over the timeline. Either header collapses its body to give
	// the column to the other; the timeline body is a fixed band when open, the
	// tree fills the rest.
	headerH := toolkit.ExpanderHeaderHeight()
	bottom := r.Y + r.H - pad
	avail := bottom - cur - 2*headerH // room the two bodies share
	if avail < 0 {
		avail = 0
	}
	filesOpen := b.filesExp.Expanded().Get()
	histOpen := b.histExp.Expanded().Get()
	tlH := 0
	if histOpen {
		tlH = toolkit.Scaled(sidebarTimelineH)
		if !filesOpen || tlH > avail {
			tlH = avail // a collapsed tree (or a short column) yields the rest to history
		}
	}
	treeH := 0
	if filesOpen {
		// tlH is clamped to <= avail above, so avail - tlH is never negative.
		treeH = avail - tlH
	}
	y := cur
	b.filesHdrRect = toolkit.Rect{X: innerX, Y: y, W: innerW, H: headerH}
	y += headerH
	b.treeRect = toolkit.Rect{X: innerX, Y: y, W: innerW, H: treeH}
	y += treeH
	b.histHdrRect = toolkit.Rect{X: innerX, Y: y, W: innerW, H: headerH}
	y += headerH
	b.tlRect = toolkit.Rect{X: innerX, Y: y, W: innerW, H: tlH}
	b.tree.SetBounds(b.treeRect)
	b.timeline.SetBounds(b.tlRect)
}

// headerText is the column title, annotated with the branch when a repo is open.
// filesHeaderText is the "Files" accordion header: the active file's base name is
// appended so the workspace shows which document is open without a separate line
// (opening a file no longer posts a "Loaded …" notice). Just "Files" when nothing
// is open.
func (b *sidebar) filesHeaderText() string {
	if p := b.s.git.loaded.Get(); p != "" {
		return "Files · " + path.Base(p)
	}
	return "Files"
}

// histHeaderText is the "History" accordion header: the branch is appended (the
// git log is per-branch), which is where the branch now lives since the standalone
// "Workspace · <branch>" line was removed. Just "History" with no open repo.
func (b *sidebar) histHeaderText() string {
	if b.s.git.statusOK && b.s.git.status.Branch != "" {
		return "History · " + b.s.git.status.Branch
	}
	return "History"
}

// detailText is the detail strip's text + ink: a git error (red) wins, then the
// last-clicked commit detail, then the latest success notice, else empty.
func (b *sidebar) detailText() (string, toolkit.RGBA) {
	if e := b.s.git.errMsg.Get(); e != "" {
		return "⚠ " + e, errorInk
	}
	if b.commitDetail != "" {
		return b.commitDetail, toolkit.RGBA{}
	}
	if n := b.s.git.notice.Get(); n != "" {
		return n, toolkit.RGBA{}
	}
	// With a repository already open the workspace keeps showing its tree during
	// an operation, so the only sign anything is happening is that the buttons went
	// flat. Name the operation, and — once it has been running a moment — its
	// elapsed seconds, so a slow pull/push reads as progressing rather than hung.
	// The host runs an animation-frame loop while busy (see SubscribeGitBusy), so
	// this ticks up on screen.
	if b.s.git.busy.Get() {
		if op := b.s.git.op.Get(); op != "" {
			// Real object-count progress off the remote's sideband wins over the bare
			// clock: "Pulling — Receiving objects:  45% (450/1000)" says the transfer
			// is moving, not just that time is passing. It falls back to the elapsed
			// seconds (from #120) whenever the server streams no progress.
			if line := b.s.git.progressLine; line != "" {
				return op + " — " + line, toolkit.RGBA{}
			}
			if secs := int(b.s.git.opElapsed); secs >= 1 {
				return fmt.Sprintf("%s… %ds", op, secs), toolkit.RGBA{}
			}
			return op + "…", toolkit.RGBA{}
		}
	}
	return "", toolkit.RGBA{}
}

// loadingText is what the empty workspace says while an operation is in flight.
// A clone that is still DOWNLOADING its git client is not yet talking to any
// remote, and saying "Cloning…" for the tens of seconds that download can take
// misattributes the wait to a slow server. So the asset phase is named for what
// it is, and the operation's own name takes over once the binary has landed.
func (b *sidebar) loadingText() (msg, caption string) {
	if a := b.s.AssetLoading(); a != "" {
		// The asset name ALONE. The column is 264 px and the caption does not
		// wrap: anything longer is clipped mid-word at the edge, which reads as
		// a rendering fault rather than as information. The message above
		// already says what the wait is for.
		return "Downloading the git client…", a
	}
	if op := b.s.git.op.Get(); op != "" {
		// Restoring reads this browser's own saved copy; it touches no remote,
		// and saying "fetching" there would name the wrong thing entirely. Its two
		// coarse phases (reading the saved copy, reopening the repository) ride the
		// same progressLine as a clone/pull sideband; before the first phase lands it
		// falls back to the generic caption plus the elapsed clock, so the panel
		// always says what the restore is doing rather than a frozen line.
		if op == "Restoring" {
			caption := "Reopening your saved workspace"
			if line := b.s.git.progressLine; line != "" {
				caption = line
			} else if secs := int(b.s.git.opElapsed); secs >= 1 {
				caption = fmt.Sprintf("%s — %ds", caption, secs)
			}
			return "Restoring…", caption
		}
		// The remote's own sideband line, when it is sending one, is the truest
		// caption ("Receiving objects:  45% (450/1000)"); it falls back to the generic
		// "Fetching…" plus the elapsed clock (from #120) when the server is quiet.
		caption := "Fetching the sample repository"
		if line := b.s.git.progressLine; line != "" {
			caption = line
		} else if secs := int(b.s.git.opElapsed); secs >= 1 {
			caption = fmt.Sprintf("%s — %ds", caption, secs)
		}
		return op + "…", caption
	}
	return "Working…", ""
}

// draw paints the column ground + rule, the header, and either the repo view
// (buttons + detail + tree + timeline) or the empty state + Clone button.
func (b *sidebar) draw(p painter.Painter, theme *toolkit.Theme) {
	if !b.open {
		return
	}
	b.ensureWidgets()
	r := b.bounds
	if r.W <= 0 || r.H <= 0 {
		return
	}
	// An edit (or a git op) between layouts changes the model but not the column
	// geometry, so re-sync the tree/timeline here; the sub-rects still hold.
	b.syncModel()
	b.bg.Fill = theme.Surface
	b.bg.SetBounds(r)
	b.bg.Draw(p, theme)
	// Right-edge hairline separating the column from the body.
	b.rule.Fill = theme.Border
	b.rule.SetBounds(toolkit.Rect{X: r.X + r.W - toolkit.Scaled(1), Y: r.Y, W: toolkit.Scaled(1), H: r.H})
	b.rule.Draw(p, theme)

	if !b.hasRepo() {
		body := b.emptyRect
		// A clone in flight: the workspace is present but still loading, so show a
		// spinner (ticked by the host frame loop) rather than the empty-or-failed
		// prompt. This is the "workspace shows before the clone finishes" state.
		if b.s.git.busy.Get() {
			sz := toolkit.Scaled(28)
			b.spinner.SetBounds(toolkit.Rect{X: body.X + (body.W-sz)/2, Y: body.Y + body.H/3 - sz/2, W: sz, H: sz})
			b.spinner.Draw(p, theme)
			msg, caption := b.loadingText()
			b.empty.Message().Set(msg)
			b.empty.Caption().Set(caption)
			b.empty.SetBounds(toolkit.Rect{X: body.X, Y: body.Y + body.H/3 + sz, W: body.W, H: body.H*2/3 - sz})
			b.empty.Draw(p, theme)
			// A clone is two waits back to back: first the git client (a separate
			// multi-megabyte binary) downloads, then the repository's objects come
			// off the remote. One bar under the message carries both — the asset's
			// own download fraction while it streams in, then the remote's parsed
			// object count once the client has landed — determinate when either can
			// be measured, sliding when neither can. Reused across the whole clone so
			// the reader never watches an empty column with no sign of movement.
			assetActive := b.s.AssetLoading() != ""
			frac := b.s.GitProgressFraction()
			if assetActive {
				frac = b.s.AssetProgress()
			}
			w := min(body.W-2*toolkit.Scaled(16), toolkit.Scaled(220))
			if w > 0 {
				b.assetBar.Indeterminate = frac < 0
				if frac >= 0 {
					b.assetBar.SetFraction(frac)
				}
				b.assetBar.SetBounds(toolkit.Rect{
					X: body.X + (body.W-w)/2,
					Y: body.Y + body.H/3 + sz + toolkit.Scaled(64),
					W: w, H: toolkit.Scaled(6),
				})
				b.assetBar.Draw(p, theme)
			}
			b.drawButtons(p, theme)
			return
		}
		// An empty workspace after a FAILED boot clone is not the same thing as
		// one nobody asked for, and it looks identical. Say which it is, here —
		// beside what is missing — rather than in the global status band, where
		// it would sit for as long as the page is open and crowd out the line
		// that says the engine is ready.
		if n := b.s.BootNotice(); n != "" {
			b.empty.Message().Set("Sample documents unavailable")
			b.empty.Caption().Set(n)
		} else {
			b.empty.Message().Set("No repository open")
			b.empty.Caption().Set("Clone one to browse its files here")
		}
		b.empty.Draw(p, theme)
		b.drawButtons(p, theme)
		return
	}

	if b.detailRect.H > 0 {
		text, ink := b.detailText()
		b.detail.Text().Set(text)
		b.detail.SetBounds(b.detailRect)
		b.detail.Ink = ink
		b.detail.VAlign = toolkit.VMiddle
		b.detail.Draw(p, theme)
	}

	// Files accordion header — labelled with the active file so the workspace shows
	// which document is open without a separate line — then the tree when the
	// section is open; then the History header (labelled with the branch) and the
	// timeline. A collapsed section has a zero-height body rect (see layout), so its
	// widget is simply not drawn.
	b.filesExp.Label = b.filesHeaderText()
	b.filesExp.SetBounds(b.filesHdrRect)
	b.filesExp.Draw(p, theme)
	if b.treeRect.H > 0 {
		b.tree.Draw(p, theme)
	}
	b.histExp.Label = b.histHeaderText()
	b.histExp.SetBounds(b.histHdrRect)
	b.histExp.Draw(p, theme)
	if b.tlRect.H > 0 {
		b.timeline.Draw(p, theme)
	}
	b.drawButtons(p, theme)
}

// drawButtons paints every laid-out command button, disabling the network ones
// while a git op is in flight.
func (b *sidebar) drawButtons(p painter.Painter, theme *toolkit.Theme) {
	busy := b.s.git.busy.Get()
	for _, sbBtn := range b.buttons {
		bt := b.btn(sbBtn.role, sbBtn.label)
		bt.Label().Set(sbBtn.label)
		bt.SetBounds(sbBtn.rect)
		bt.Disabled().Set(busy || sbBtn.disabled)
		bt.Draw(p, theme)
	}
}

// handleClick routes a press inside the column to a button, the file tree or the
// timeline. An open sidebar consumes every press within its bounds (it is a
// distinct region, never overlapping the body). Returns whether it consumed it.
func (b *sidebar) handleClick(x, y int) bool {
	if !b.open || !b.bounds.Contains(x, y) {
		return false
	}
	b.ensureWidgets()
	// Command buttons first.
	for _, sbBtn := range b.buttons {
		if sbBtn.rect.Contains(x, y) {
			bt := b.btn(sbBtn.role, sbBtn.label)
			bt.SetBounds(sbBtn.rect)
			bt.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - sbBtn.rect.X, Y: y - sbBtn.rect.Y})
			return true
		}
	}
	// Accordion headers: a click toggles the section, then re-lays out so the
	// tree/timeline resize immediately. The two are mutually exclusive — opening one
	// collapses the other, so at most one body shows and the column stays compact.
	if b.hasRepo() && b.filesHdrRect.Contains(x, y) {
		b.filesExp.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.filesHdrRect.X, Y: y - b.filesHdrRect.Y})
		if b.filesExp.Expanded().Get() {
			b.histExp.Expanded().Set(false)
		}
		b.layout()
		return true
	}
	if b.hasRepo() && b.histHdrRect.Contains(x, y) {
		b.histExp.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.histHdrRect.X, Y: y - b.histHdrRect.Y})
		if b.histExp.Expanded().Get() {
			b.filesExp.Expanded().Set(false)
		}
		b.layout()
		return true
	}
	// File tree: route the click and let the widget decide what it was. Opening a
	// file is no longer read back out of the selection here — a click that lands
	// on a folder's twisty, on the rename entry or on the delete confirmation is
	// not an open, and inferring "open" from "something is selected" made all
	// three of those open a file as a side effect. The tree says which it was,
	// through onFileRequest.
	if b.hasRepo() && b.treeRect.Contains(x, y) {
		b.tree.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.treeRect.X, Y: y - b.treeRect.Y})
		b.s.git.refresh()
		return true
	}
	// Timeline: map the click to a commit and show its detail.
	if b.hasRepo() && b.tlRect.Contains(x, y) {
		if i := b.timeline.EventAt(x-b.tlRect.X, y-b.tlRect.Y); i >= 0 && i < len(b.s.git.log) {
			b.commitDetail = commitDetailLine(b.s.git.log[i])
			b.s.git.refresh()
		}
		return true
	}
	return true // modal within its own column
}

// commitDetailLine formats a clicked commit's one-line detail for the strip.
func commitDetailLine(c GitCommitInfo) string {
	line := shortHash(c.Hash)
	if c.Subject != "" {
		line += " · " + c.Subject
	}
	if c.Author != "" {
		line += " · " + c.Author
	}
	return line
}

// handleRelease clears the momentary pressed face of every command button on the
// mouseup that ends a press this column captured.
func (b *sidebar) handleRelease(x, y int) {
	if b.btns == nil {
		return
	}
	for _, bt := range b.btns {
		bt.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
	}
	b.s.git.refresh()
}

// handleScroll forwards a wheel scroll to the tree or the timeline under the
// pointer. Returns whether it consumed the scroll.
func (b *sidebar) handleScroll(x, y, dy int) bool {
	if !b.open || !b.bounds.Contains(x, y) || !b.hasRepo() {
		return false
	}
	b.ensureWidgets()
	switch {
	case b.treeRect.Contains(x, y):
		b.tree.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, Delta: dy})
	case b.tlRect.Contains(x, y):
		b.timeline.OnEvent(toolkit.Event{Kind: toolkit.EventScroll, Delta: dy})
	default:
		return false
	}
	b.s.git.refresh()
	return true
}

// dispatch runs a clicked command button's action. Network actions are guarded
// against a busy race here as well as being drawn disabled.
func (b *sidebar) dispatch(role sidebarRole) {
	switch role {
	case sbRoleRefresh:
		// Re-snapshot the cached status and force a tree/timeline rebuild, so a
		// buffer edit's dirtiness (and any fresh status) shows without a git op.
		b.s.git.refreshStatus()
		b.sig = ""
	case sbRoleStage:
		b.s.GitStage(nil)
	case sbRoleCommit:
		b.s.GitCommit(nil)
	case sbRoleForget:
		b.s.GitForget(nil)
	case sbRolePull:
		b.s.GitPull(nil)
	case sbRolePush:
		b.s.GitPush(nil)
	case sbRoleClone:
		b.s.git.openPanel()
	case sbRoleImport:
		// ⛔ Guarded here as well as drawn disabled: a click's path to this point
		// runs through the button widget, and a guard that lives only in how the
		// button is PAINTED is one refactor away from a nil call. The picker is
		// asynchronous — deliver runs once per chosen file, later, on the event
		// loop — so nothing after this call may assume a file arrived.
		if b.s.git.io.Import != nil {
			b.s.git.io.Import(b.s.git.importDelivered)
		}
	case sbRoleExport:
		b.s.git.exportActive()
	}
	b.commitDetail = ""
	b.s.git.refresh()
}

// --- host / headless-proof introspection -------------------------------------

// SidebarOpen reports whether the workspace sidebar is showing.
func (s *State) SidebarOpen() bool { return s.sidebar.open }

// ToggleSidebar flips the sidebar and relays out (host toolbar / proof control).
func (s *State) ToggleSidebar() {
	s.sidebar.toggle()
	s.layout()
}

// SetSidebarOpen sets the sidebar open state explicitly (host / proof control).
func (s *State) SetSidebarOpen(open bool) {
	if s.sidebar.open != open {
		s.sidebar.toggle()
	}
	s.layout()
}

// SidebarWidth is the column's reserved device width (0 when closed).
func (s *State) SidebarWidth() int { return s.sidebar.width() }

// SidebarRect is the sidebar column's device-pixel [x,y,w,h] bounds.
func (s *State) SidebarRect() [4]int {
	r := s.sidebar.bounds
	return [4]int{r.X, r.Y, r.W, r.H}
}

// SidebarFileRows returns the workspace's files as "name" rows with their status
// mark appended (" M"/" A"/" ?"/…), folders first at each level and each group
// alphabetical, so a headless proof can assert the tree + per-file status without
// pixel-reading. Directories carry no mark.
//
// ⚠ It is derived from the ENTRIES, which is what the host put in, following the
// order [toolkit.FileTree] documents for what it draws — folders before files,
// each by name. It therefore does not know about a folder the person has
// collapsed, where the old node walk did. That is a deliberate trade: the
// alternative is reaching into the widget's row state, and what this is for is
// asserting that the workspace lists what the repository holds. The drawn result
// is covered by the pixel proof instead.
func (s *State) SidebarFileRows() []string {
	entries := s.sidebar.tree.Entries().Get()
	marks := make(map[string]string, len(entries))
	byDir := map[string][]string{}
	subdirs := map[string]map[string]bool{}
	for _, e := range entries {
		dir, name := "", e.Path
		if i := strings.LastIndex(e.Path, "/"); i >= 0 {
			dir, name = e.Path[:i], e.Path[i+1:]
		}
		byDir[dir] = append(byDir[dir], name)
		marks[e.Path] = fileStatusMark(e.Status)
		// Every ancestor of a path is a folder, whether or not anything listed it.
		for d := dir; d != ""; {
			parent := ""
			if i := strings.LastIndex(d, "/"); i >= 0 {
				parent = d[:i]
			}
			if subdirs[parent] == nil {
				subdirs[parent] = map[string]bool{}
			}
			subdirs[parent][d] = true
			d = parent
		}
	}
	var out []string
	var walk func(dir string)
	walk = func(dir string) {
		kids := make([]string, 0, len(subdirs[dir]))
		for d := range subdirs[dir] {
			kids = append(kids, d)
		}
		sort.Strings(kids)
		for _, d := range kids {
			name := d
			if i := strings.LastIndex(d, "/"); i >= 0 {
				name = d[i+1:]
			}
			out = append(out, name+"/")
			walk(d)
		}
		files := append([]string(nil), byDir[dir]...)
		sort.Strings(files)
		for _, name := range files {
			full := name
			if dir != "" {
				full = dir + "/" + name
			}
			row := name
			if m := marks[full]; m != "" {
				row += " " + m
			}
			out = append(out, row)
		}
	}
	walk("")
	return out
}

// fileStatusMark is the one-character mark the Explorer draws for a status —
// `git status --short`'s vocabulary, which [toolkit.FileStatus] shares.
//
// ⛔ It is spelled out here rather than read off the widget because the widget
// does not export it, and a proof that asserted nothing because the mark came
// back empty would look exactly like a proof that passed. If these ever diverge
// from what is drawn, the pixel proof is what says so.
func fileStatusMark(s toolkit.FileStatus) string {
	switch s {
	case toolkit.FileModified:
		return "M"
	case toolkit.FileAdded:
		return "A"
	case toolkit.FileDeleted:
		return "D"
	case toolkit.FileUntracked:
		return "?"
	case toolkit.FileConflicted:
		return "!"
	}
	return ""
}

// SidebarTimelineTitles returns the commit-timeline event titles, newest first.
func (s *State) SidebarTimelineTitles() []string {
	out := make([]string, 0, len(s.sidebar.timeline.Events))
	for _, ev := range s.sidebar.timeline.Events {
		out = append(out, ev.Title)
	}
	return out
}

// SidebarDetail is the detail strip's current text (a clicked commit's detail,
// or the latest git notice/error).
func (s *State) SidebarDetail() string {
	t, _ := s.sidebar.detailText()
	return t
}
