// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"sort"
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

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
// # File-open + dirty model (v1)
//
// The editor holds ONE document at a time. Clicking a file row opens it via
// State.GitOpenFile (reusing the panel's cached-content load path), replacing the
// editor buffer — so the ACTIVE file is the editable one; the others are shown
// with their committed git status until opened. A row's badge is the file's git
// status (browsergit classify: modified/staged/deleted/untracked), except the
// active file also reflects LIVE editor dirtiness (buffer != committed content)
// as "modified", so an unsaved edit shows immediately without a round-trip.
//
// Deferred (noted for a later pass): independent per-file edit buffers / a
// multi-tab editor so several files can be dirty at once; today only the active
// file carries live dirtiness, and opening another file replaces the buffer.

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

// Per-file git-status badge colours (fixed hues, not theme inks, so a status
// reads the same in light + dark — the same rationale as zones.readyDotColor).
// Green/amber/red reuse the toolkit Timeline/Alert severity palette so the
// sidebar's badges and its timeline markers speak one colour language.
var (
	badgeModifiedInk  = toolkit.RGBA{R: 0xE0, G: 0xA0, B: 0x30, A: 0xFF} // amber
	badgeStagedInk    = toolkit.RGBA{R: 0x2E, G: 0x8B, B: 0x57, A: 0xFF} // green
	badgeDeletedInk   = toolkit.RGBA{R: 0xC0, G: 0x30, B: 0x30, A: 0xFF} // red
	badgeUntrackedInk = toolkit.RGBA{R: 0x30, G: 0x80, B: 0xC0, A: 0xFF} // blue
)

// badgeFor maps a browsergit classify() status to the sidebar's one-letter badge
// glyph and its ink. A clean file (unknown/empty status) badges blank.
func badgeFor(status string) (glyph string, ink toolkit.RGBA) {
	switch status {
	case "modified":
		return "M", badgeModifiedInk
	case "staged":
		return "A", badgeStagedInk
	case "deleted":
		return "D", badgeDeletedInk
	case "untracked":
		return "U", badgeUntrackedInk
	default:
		return "", toolkit.RGBA{}
	}
}

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
	sbRoleClone // empty-state action: open the Remote-Git panel to clone
)

// sidebarButton is a laid-out command button: its role, rect and label.
type sidebarButton struct {
	role  sidebarRole
	rect  toolkit.Rect
	label string
}

// sidebar is the workspace sidebar affordance. All display state is derived each
// layout from the git view's observables + snapshots (no per-frame field copy);
// the tree/timeline are rebuilt only when a cheap signature changes, so scrolling
// and selection survive between frames.
type sidebar struct {
	s    *State
	open bool

	// bounds is the whole column; sub-rects are recomputed by layout().
	bounds     toolkit.Rect
	headerRect toolkit.Rect
	detailRect toolkit.Rect
	treeRect   toolkit.Rect
	tlRect     toolkit.Rect
	buttons    []sidebarButton

	// nodePaths maps a file leaf node to its working-tree path (dir nodes absent),
	// so a tree click resolves to the file to open. Rebuilt with the tree.
	nodePaths map[*toolkit.TreeTableNode]string
	// sig is the last tree/timeline signature; a change triggers a rebuild.
	sig string
	// commitDetail is the last-clicked commit's one-line detail (sha · subject ·
	// author), shown in the detail strip until the next git notice/error.
	commitDetail string

	// persistent toolkit widgets, built once and reused every frame.
	bg       *toolkit.Backdrop
	rule     *toolkit.Backdrop
	header   *toolkit.Label
	detail   *toolkit.Label
	tree     *toolkit.TreeTable
	timeline *toolkit.Timeline
	empty    *toolkit.EmptyState
	btns     map[sidebarRole]*toolkit.Button
}

// newSidebar builds the affordance for s (closed by default, so the canvas keeps
// its full width until the user opens the sidebar from the toolbar).
func newSidebar(s *State) *sidebar {
	return &sidebar{s: s, nodePaths: map[*toolkit.TreeTableNode]string{}}
}

// ensureWidgets lazily builds the persistent widgets. Called at the top of every
// draw / input path (the toolkit widgets need the theme at draw time, not here).
func (b *sidebar) ensureWidgets() {
	if b.bg != nil {
		return
	}
	b.bg = &toolkit.Backdrop{}
	b.rule = &toolkit.Backdrop{}
	b.header = toolkit.NewLabel("")
	b.detail = toolkit.NewLabel("")
	b.tree = toolkit.NewTreeTable(
		[]toolkit.TreeTableColumn{{Title: "Workspace"}, {Title: "S", Width: toolkit.Scaled(22), Align: toolkit.AlignCenter}},
		nil,
	)
	b.timeline = toolkit.NewTimeline(nil)
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

// toggle flips the sidebar open/closed and repaints.
func (b *sidebar) toggle() {
	b.open = !b.open
	b.s.git.refresh()
}

// activeDirty reports whether the loaded file's editor buffer differs from its
// committed working-tree content (the cached copy the last clone/pull filled). A
// read miss (uncached file) reports not-dirty — there is nothing to compare.
func (b *sidebar) activeDirty() bool {
	p := b.s.git.loaded.Get()
	if p == "" {
		return false
	}
	data, err := b.s.git.backend.ReadFile(p)
	if err != nil {
		return false
	}
	return string(data) != b.s.Source()
}

// fileBadge returns the badge glyph + ink for a working-tree path: the active
// file's live editor dirtiness (as "modified") takes precedence, otherwise the
// file's git status from the last snapshot, otherwise clean (blank).
func (b *sidebar) fileBadge(path string) (string, toolkit.RGBA) {
	if path == b.s.git.loaded.Get() && b.activeDirty() {
		return badgeFor("modified")
	}
	for _, c := range b.s.git.status.Changes {
		if c.Path == path {
			return badgeFor(c.Status)
		}
	}
	return "", toolkit.RGBA{}
}

// signature is a cheap digest of everything the tree + timeline render from, so
// a rebuild happens only on a real change (preserving scroll + selection between
// frames). It folds the file list, every dirty entry, the active path + its live
// dirtiness, and the commit hashes.
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
	if b.activeDirty() {
		sb.WriteString("*")
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
	roots, nodePaths := buildFileTree(b.s.git.files, b.fileBadge)
	setTreeRoot(b.tree, roots) // widget-state write isolated in sidebar_binding.go
	b.nodePaths = nodePaths
	// Re-select the active file's node so the highlight follows the open file.
	active := b.s.git.loaded.Get()
	b.tree.Selected().Set(nil)
	for node, p := range nodePaths {
		if p == active {
			b.tree.Selected().Set(node)
			break
		}
	}
	setTimelineEvents(b.timeline, timelineEvents(b.s.git.log)) // isolated in sidebar_binding.go
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

// buildFileTree turns a sorted slash-relative file list into a folder-grouped
// forest of TreeTable nodes (directories expandable + expanded, files as leaves
// carrying a name cell + a status-badge cell tinted via CellInk) and the leaf →
// path map a click resolves through. Pure + testable: the badge lookup is passed
// in so the tree build has no dependency on the live git state.
func buildFileTree(files []string, badge func(string) (string, toolkit.RGBA)) ([]*toolkit.TreeTableNode, map[*toolkit.TreeTableNode]string) {
	var roots []*toolkit.TreeTableNode
	dirs := map[string]*toolkit.TreeTableNode{}
	nodePaths := map[*toolkit.TreeTableNode]string{}

	// dirNode gets or creates the node for a slash dir path ("" is the root), so
	// nested directories are materialised on demand and attached to their parent.
	var dirNode func(dir string) *toolkit.TreeTableNode
	dirNode = func(dir string) *toolkit.TreeTableNode {
		if dir == "" {
			return nil
		}
		if n, ok := dirs[dir]; ok {
			return n
		}
		name, parent := dir, ""
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			name, parent = dir[i+1:], dir[:i]
		}
		n := &toolkit.TreeTableNode{Cells: []string{name + "/", ""}, Expanded: true}
		dirs[dir] = n
		if p := dirNode(parent); p != nil {
			p.Children = append(p.Children, n)
		} else {
			roots = append(roots, n)
		}
		return n
	}

	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		dir, name := "", f
		if i := strings.LastIndex(f, "/"); i >= 0 {
			dir, name = f[:i], f[i+1:]
		}
		glyph, ink := badge(f)
		leaf := &toolkit.TreeTableNode{Cells: []string{name, glyph}, CellInk: []toolkit.RGBA{{}, ink}}
		nodePaths[leaf] = f
		if p := dirNode(dir); p != nil {
			p.Children = append(p.Children, leaf)
		} else {
			roots = append(roots, leaf)
		}
	}
	return roots, nodePaths
}

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
	cur := r.Y + pad
	b.headerRect = toolkit.Rect{X: innerX, Y: cur, W: innerW, H: lineH}
	cur += lineH + gap

	if !b.hasRepo() {
		// Empty state: centre the prompt in the remaining column, with a Clone
		// button beneath it.
		bodyY := cur
		bodyH := r.Y + r.H - pad - btnH - gap - bodyY
		if bodyH < 0 {
			bodyH = 0
		}
		b.empty.SetBounds(toolkit.Rect{X: innerX, Y: bodyY, W: innerW, H: bodyH})
		cw := toolkit.Scaled(120)
		if cw > innerW {
			cw = innerW
		}
		b.buttons = append(b.buttons, sidebarButton{
			role:  sbRoleClone,
			rect:  toolkit.Rect{X: r.X + (r.W-cw)/2, Y: r.Y + r.H - pad - btnH, W: cw, H: btnH},
			label: "Clone…",
		})
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

	// Detail strip (clicked-commit detail, or the latest git notice/error).
	b.detailRect = toolkit.Rect{X: innerX, Y: cur, W: innerW, H: lineH}
	cur += lineH + gap

	// Timeline claims a fixed band at the bottom; the tree fills what is left.
	tlH := toolkit.Scaled(sidebarTimelineH)
	bottom := r.Y + r.H - pad
	treeTop := cur
	minTree := toolkit.Scaled(sidebarMinTreeH)
	if bottom-treeTop-tlH < minTree {
		tlH = bottom - treeTop - minTree // yield the timeline first on a short column
		if tlH < 0 {
			tlH = 0
		}
	}
	treeH := bottom - treeTop - tlH
	if treeH < 0 {
		treeH = 0
	}
	b.treeRect = toolkit.Rect{X: innerX, Y: treeTop, W: innerW, H: treeH}
	b.tlRect = toolkit.Rect{X: innerX, Y: treeTop + treeH, W: innerW, H: tlH}
	b.tree.SetBounds(b.treeRect)
	b.timeline.SetBounds(b.tlRect)
}

// headerText is the column title, annotated with the branch when a repo is open.
func (b *sidebar) headerText() string {
	if b.s.git.statusOK && b.s.git.status.Branch != "" {
		return "Workspace · " + b.s.git.status.Branch
	}
	return "Workspace"
}

// detailText is the detail strip's text + ink: a git error (red) wins, then the
// last-clicked commit detail, then the latest success notice, else empty.
func (b *sidebar) detailText() (string, toolkit.RGBA) {
	if e := b.s.git.errMsg.Get(); e != "" {
		return "⚠ " + e, badgeDeletedInk
	}
	if b.commitDetail != "" {
		return b.commitDetail, toolkit.RGBA{}
	}
	if n := b.s.git.notice.Get(); n != "" {
		return n, toolkit.RGBA{}
	}
	return "", toolkit.RGBA{}
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

	b.header.Text().Set(b.headerText())
	b.header.SetBounds(b.headerRect)
	b.header.Ink = theme.OnSurface
	b.header.VAlign = toolkit.VMiddle
	b.header.Draw(p, theme)

	if !b.hasRepo() {
		b.empty.Draw(p, theme)
		b.drawButtons(p, theme)
		return
	}

	text, ink := b.detailText()
	b.detail.Text().Set(text)
	b.detail.SetBounds(b.detailRect)
	b.detail.Ink = ink
	b.detail.VAlign = toolkit.VMiddle
	b.detail.Draw(p, theme)

	b.tree.Draw(p, theme)
	b.timeline.Draw(p, theme)
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
		bt.Disabled().Set(busy)
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
	// File tree: route the click, then open the newly selected file (if any).
	if b.hasRepo() && b.treeRect.Contains(x, y) {
		b.tree.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - b.treeRect.X, Y: y - b.treeRect.Y})
		if node := b.tree.Selected().Get(); node != nil {
			if path, ok := b.nodePaths[node]; ok {
				b.commitDetail = ""
				b.s.GitOpenFile(path)
			}
		}
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
	case sbRolePull:
		b.s.GitPull(nil)
	case sbRolePush:
		b.s.GitPush(nil)
	case sbRoleClone:
		b.s.git.openPanel()
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

// SidebarFileRows returns the flattened visible file/dir rows as "name" strings
// with their badge glyph appended (" M"/" A"/…), so a headless proof can assert
// the tree + per-file status without pixel-reading. Directories carry no badge.
func (s *State) SidebarFileRows() []string {
	var out []string
	var walk func(nodes []*toolkit.TreeTableNode)
	walk = func(nodes []*toolkit.TreeTableNode) {
		for _, n := range nodes {
			row := n.Cells[0]
			if len(n.Cells) > 1 && n.Cells[1] != "" {
				row += " " + n.Cells[1]
			}
			out = append(out, row)
			if n.Expanded {
				walk(n.Children)
			}
		}
	}
	walk(s.sidebar.tree.Root)
	return out
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
