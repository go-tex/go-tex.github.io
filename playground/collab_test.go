// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-widgets/toolkit"
)

// fakeBackend is an in-memory [collabBackend] for the state-machine tests: it
// records what it was asked, replies through done synchronously, and lets a test
// drive the connection state and fire the change hook.
type fakeBackend struct {
	name      string
	color     toolkit.RGBA
	offer     string // handed back by Host
	answer    string // handed back by Join
	hostErr   error
	joinErr   error
	acceptErr error

	gotOffer  string // the offer passed to Join
	gotAnswer string // the answer passed to AcceptAnswer

	connected bool
	peers     int
	onChange  func()

	disconnected bool
}

func (f *fakeBackend) Host(name string, color toolkit.RGBA, done func(string, error)) {
	f.name, f.color = name, color
	done(f.offer, f.hostErr)
}

func (f *fakeBackend) Join(name string, color toolkit.RGBA, offer string, done func(string, error)) {
	f.name, f.color, f.gotOffer = name, color, offer
	done(f.answer, f.joinErr)
}

func (f *fakeBackend) AcceptAnswer(answer string, done func(error)) {
	f.gotAnswer = answer
	done(f.acceptErr)
}

func (f *fakeBackend) Disconnect()          { f.disconnected = true }
func (f *fakeBackend) Connected() bool      { return f.connected }
func (f *fakeBackend) PeerCount() int       { return f.peers }
func (f *fakeBackend) SetOnChange(h func()) { f.onChange = h }

// withFake attaches a fresh fakeBackend to a fresh State and returns both plus a
// repaint counter.
func withFake(t *testing.T) (*State, *fakeBackend, *int) {
	t.Helper()
	s := newTestState(t, false)
	f := &fakeBackend{}
	n := 0
	s.collab.attach(f, func() { n++ })
	return s, f, &n
}

func TestNewCollabViewDefaults(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	if v.name == "" {
		t.Fatal("default display name is empty")
	}
	if v.color.A == 0 {
		t.Fatal("default caret colour is transparent")
	}
	if v.phase != phaseIdle || v.open {
		t.Fatalf("fresh view: phase=%d open=%v, want idle/closed", v.phase, v.open)
	}
	if got := s.CollabColorHex(); len(got) != 7 || got[0] != '#' {
		t.Fatalf("CollabColorHex = %q, want #rrggbb", got)
	}
}

func TestCollabHostSuccessAndError(t *testing.T) {
	s, f, n := withFake(t)
	f.offer = "OFFER-BLOB"

	var gotOffer string
	var gotErr error
	s.CollabHost(func(offer string, err error) { gotOffer, gotErr = offer, err })
	if gotErr != nil || gotOffer != "OFFER-BLOB" {
		t.Fatalf("host done = (%q, %v), want the offer", gotOffer, gotErr)
	}
	if s.CollabPhase() != int(phaseHostWait) {
		t.Fatalf("phase after Host = %d, want hostWait", s.CollabPhase())
	}
	if s.CollabOffer() != "OFFER-BLOB" {
		t.Fatalf("CollabOffer = %q", s.CollabOffer())
	}
	if f.name != s.CollabName() {
		t.Fatalf("backend got name %q, want %q", f.name, s.CollabName())
	}
	if *n == 0 {
		t.Fatal("Host did not repaint")
	}

	// Error path.
	s2, f2, _ := withFake(t)
	f2.hostErr = errors.New("no ICE")
	s2.CollabHost(nil)
	if s2.collab.errMsg == "" || s2.CollabPhase() != int(phaseIdle) {
		t.Fatalf("host error: errMsg=%q phase=%d", s2.collab.errMsg, s2.CollabPhase())
	}
}

func TestCollabJoinSuccessEmptyAndError(t *testing.T) {
	s, f, _ := withFake(t)
	f.answer = "ANSWER-BLOB"

	var gotAnswer string
	s.CollabJoin("  HOST-OFFER  ", func(answer string, err error) { gotAnswer = answer })
	if gotAnswer != "ANSWER-BLOB" || f.gotOffer != "HOST-OFFER" {
		t.Fatalf("join: answer=%q gotOffer=%q (offer should be trimmed)", gotAnswer, f.gotOffer)
	}
	if s.CollabPhase() != int(phaseGuestWait) || s.CollabAnswer() != "ANSWER-BLOB" {
		t.Fatalf("phase=%d answer=%q after Join", s.CollabPhase(), s.CollabAnswer())
	}

	// Empty offer is refused before the backend is touched.
	s2, _, _ := withFake(t)
	var emptyErr error
	s2.CollabJoin("   ", func(_ string, err error) { emptyErr = err })
	if !errors.Is(emptyErr, errEmptyBlob) || s2.collab.errMsg == "" {
		t.Fatalf("empty join: err=%v errMsg=%q", emptyErr, s2.collab.errMsg)
	}

	// Backend error.
	s3, f3, _ := withFake(t)
	f3.joinErr = errors.New("bad offer")
	s3.CollabJoin("x", nil)
	if s3.collab.errMsg == "" {
		t.Fatal("join backend error not surfaced")
	}
}

func TestCollabAcceptAnswerSuccessEmptyAndError(t *testing.T) {
	s, f, _ := withFake(t)
	var accepted bool
	s.CollabAcceptAnswer("  ANS  ", func(err error) { accepted = err == nil })
	if !accepted || f.gotAnswer != "ANS" {
		t.Fatalf("accept: ok=%v gotAnswer=%q (should be trimmed)", accepted, f.gotAnswer)
	}

	s2, _, _ := withFake(t)
	var emptyErr error
	s2.CollabAcceptAnswer("", func(err error) { emptyErr = err })
	if !errors.Is(emptyErr, errEmptyBlob) {
		t.Fatalf("empty accept err = %v", emptyErr)
	}

	s3, f3, _ := withFake(t)
	f3.acceptErr = errors.New("stale")
	s3.CollabAcceptAnswer("y", nil)
	if s3.collab.errMsg == "" {
		t.Fatal("accept backend error not surfaced")
	}
}

func TestOnBackendChangeConnectAndDrop(t *testing.T) {
	s, f, _ := withFake(t)
	s.collab.phase = phaseHostWait

	// Connect: the change hook advances the phase.
	f.connected = true
	f.onChange()
	if s.CollabPhase() != int(phaseConnected) {
		t.Fatalf("phase after connect = %d, want connected", s.CollabPhase())
	}
	if !s.CollabConnected() {
		t.Fatal("CollabConnected should be true")
	}

	// A second change while still connected is idempotent.
	f.onChange()
	if s.CollabPhase() != int(phaseConnected) {
		t.Fatal("connected phase should stay put")
	}

	// Drop: falls back to idle with a message.
	f.connected = false
	f.onChange()
	if s.CollabPhase() != int(phaseIdle) || s.collab.errMsg == "" {
		t.Fatalf("after drop: phase=%d errMsg=%q", s.CollabPhase(), s.collab.errMsg)
	}
}

func TestCollabDisconnect(t *testing.T) {
	s, f, _ := withFake(t)
	s.collab.phase = phaseConnected
	s.collab.offer, s.collab.answer = "a", "b"
	s.CollabDisconnect()
	if !f.disconnected {
		t.Fatal("backend not disconnected")
	}
	if s.CollabPhase() != int(phaseIdle) || s.CollabOffer() != "" || s.CollabAnswer() != "" {
		t.Fatalf("after disconnect: phase=%d offer=%q answer=%q", s.CollabPhase(), s.CollabOffer(), s.CollabAnswer())
	}
}

func TestCollabPeerCountAndSummary(t *testing.T) {
	s, f, _ := withFake(t)
	for i, want := range []string{
		"Connected. Waiting for your peer…",
		"Connected — 1 peer editing with you:",
		"Connected — 2 peers editing with you:",
	} {
		f.peers = i
		if got := s.collab.connectedSummary(); got != want {
			t.Fatalf("peers=%d summary=%q, want %q", i, got, want)
		}
	}
	f.peers = 3
	if s.CollabPeerCount() != 3 {
		t.Fatalf("CollabPeerCount = %d", s.CollabPeerCount())
	}
}

func TestCollabNameEditing(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	v.open = true
	v.nameFocused = true
	v.name = ""

	if !s.HandleChar("A") || !s.HandleChar("b") {
		t.Fatal("focused name field did not consume characters")
	}
	if v.name != "Ab" {
		t.Fatalf("name = %q, want Ab", v.name)
	}
	// Backspace, Enter (commit), Escape (defocus / close).
	s.HandleKeyDown("Backspace")
	if v.name != "A" {
		t.Fatalf("name after backspace = %q", v.name)
	}
	if !s.HandleKeyDown("Enter") || v.nameFocused {
		t.Fatal("Enter should defocus the name field")
	}
	// A char with the field unfocused is not consumed by the panel.
	if v.handleChar("z") {
		t.Fatal("unfocused name field consumed a char")
	}
	// Escape while open-but-unfocused closes the panel.
	if !s.HandleKeyDown("Escape") || v.open {
		t.Fatal("Escape should close the panel")
	}
	// A multi-rune code (a key name) is not inserted.
	v.open, v.nameFocused = true, true
	before := v.name
	v.handleChar("Shift")
	if v.name != before {
		t.Fatal("a key-name should not be inserted as text")
	}
	// Escape while the field is focused defocuses it but leaves the panel open.
	v.open, v.nameFocused = true, true
	if !v.handleKey("Escape") || v.nameFocused || !v.open {
		t.Fatal("Escape while focused should defocus but keep the panel open")
	}
	// Open panel, unfocused field, a non-Escape key: not consumed by the field.
	if v.handleKey("Backspace") {
		t.Fatal("unfocused name field should not consume an editing key")
	}
	// "Return" (a synonym for Enter) also commits; a non-editing key while
	// focused is still swallowed by the modal field.
	v.open, v.nameFocused = true, true
	if !v.handleKey("Return") || v.nameFocused {
		t.Fatal("Return should defocus the name field")
	}
	v.nameFocused = true
	if !v.handleKey("ArrowLeft") {
		t.Fatal("a focused name field should swallow other keys")
	}
	// handleKey with the panel closed is ignored.
	v.open = false
	if v.handleKey("Backspace") {
		t.Fatal("closed panel consumed a key")
	}
}

func TestCollabLauncherThroughHandleClick(t *testing.T) {
	s := newTestState(t, false)
	s.collab.layout()
	lb := s.collab.launcher
	// A click on the launcher, routed through the app's own HandleClick, opens the
	// panel and is reported as consumed (the app.go hook's true branch).
	if !s.HandleClick(lb.X+lb.W/2, lb.Y+lb.H/2) {
		t.Fatal("HandleClick did not consume the launcher click")
	}
	if !s.CollabActive() {
		t.Fatal("launcher click via HandleClick did not open the panel")
	}
}

func TestCollabLayoutEdges(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	// Before the host's first layout the toolbar height is zero: the launcher
	// falls back to a default height rather than collapsing.
	s.toolbarH = 0
	v.layout()
	if v.launcher.H <= 0 {
		t.Fatalf("launcher height with toolbarH==0 = %d", v.launcher.H)
	}
	// A surface narrower than the panel clamps the panel to fit.
	s.w = 120
	v.open = true
	v.layout()
	if v.panel.W > s.w {
		t.Fatalf("panel width %d exceeds surface %d", v.panel.W, s.w)
	}
}

func TestCollabClickRouting(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.layout()

	// Closed: a click off the launcher is ignored; on it opens the panel.
	if v.handleClick(-100, -100) {
		t.Fatal("off-launcher click consumed while closed")
	}
	lb := v.launcher
	if !v.handleClick(lb.X+lb.W/2, lb.Y+lb.H/2) || !v.open {
		t.Fatal("launcher click did not open the panel")
	}

	// Open + idle: click the name field to focus it.
	v.layout()
	if !v.handleClick(v.nameRect.X+2, v.nameRect.Y+2) || !v.nameFocused {
		t.Fatal("name-field click did not focus it")
	}

	// Click a button (Host) — find it in the laid-out items.
	v.layout()
	hit := false
	for _, b := range v.buttons {
		if b.role == roleHost {
			hit = v.handleClick(b.rect.X+1, b.rect.Y+1)
		}
	}
	if !hit {
		t.Fatal("Host button click not consumed")
	}
	if v.nameFocused {
		t.Fatal("clicking a button should defocus the name field")
	}

	// A click on empty panel space is still swallowed (modal).
	if !v.handleClick(v.panel.X+1, v.panel.Y+v.panel.H-2) {
		t.Fatal("modal panel should swallow a background click")
	}
}

func TestCollabDispatchRoles(t *testing.T) {
	s, f, _ := withFake(t)
	v := s.collab
	f.offer, f.answer = "O", "A"

	var written string
	v.clipWrite = func(text string) { written = text }
	v.clipRead = func(cb func(string)) { cb("PASTED") }

	v.open = true
	v.dispatch(roleClose)
	if v.open {
		t.Fatal("roleClose did not close")
	}

	v.dispatch(roleJoin)
	if v.phase != phaseGuestOffer {
		t.Fatalf("roleJoin phase=%d", v.phase)
	}

	v.dispatch(roleHost)
	if v.phase != phaseHostWait {
		t.Fatalf("roleHost phase=%d", v.phase)
	}

	v.offer = "OFF"
	v.dispatch(roleCopyOffer)
	if written != "OFF" {
		t.Fatalf("roleCopyOffer wrote %q", written)
	}
	v.answer = "ANS"
	v.dispatch(roleCopyAnswer)
	if written != "ANS" {
		t.Fatalf("roleCopyAnswer wrote %q", written)
	}

	// Paste roles read the clipboard and feed Join / AcceptAnswer.
	v.dispatch(rolePasteOffer)
	if f.gotOffer != "PASTED" {
		t.Fatalf("rolePasteOffer fed %q to Join", f.gotOffer)
	}
	v.dispatch(rolePasteAnswer)
	if f.gotAnswer != "PASTED" {
		t.Fatalf("rolePasteAnswer fed %q to AcceptAnswer", f.gotAnswer)
	}

	name0 := v.name
	for i := 0; i < 20; i++ {
		v.dispatch(roleShuffle)
		if v.name != name0 {
			break
		}
	}
	// (Shuffle may land on the same name once; over 20 draws it should change.)

	v.phase = phaseConnected
	v.dispatch(roleDisconnect)
	if !f.disconnected || v.phase != phaseIdle {
		t.Fatalf("roleDisconnect: disconnected=%v phase=%d", f.disconnected, v.phase)
	}

	// roleNone / roleNameField are no-ops in dispatch.
	v.dispatch(roleNone)
	v.dispatch(roleNameField)
}

func TestClipboardHooksNilSafe(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	// No hooks installed: read/write must be silent no-ops.
	v.readClipboard(func(string) { t.Fatal("cb should not fire with no reader") })
	v.writeClipboard("ignored")
}

func TestCollabDrawEveryPhase(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	f := &fakeBackend{peers: 1}
	v.attach(f, func() {})
	buf := make([]byte, testW*testH*4)

	// A remote decoration exercises the connected-peer rows (named + anonymous).
	s.editor.Decorations = []toolkit.Decoration{
		{Label: "Alice", Color: toolkit.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF}, CursorLine: 1, CursorCol: 2},
		{Label: "", Color: toolkit.RGBA{R: 0x44, G: 0x55, B: 0x66, A: 0xFF}},
	}

	phases := []struct {
		name  string
		setup func()
	}{
		{"launcher-closed", func() { v.open = false }},
		{"idle", func() { v.open, v.phase, v.busy, v.errMsg = true, phaseIdle, false, "" }},
		{"busy", func() { v.busy = true }},
		{"hostWait", func() { v.busy, v.phase, v.offer = false, phaseHostWait, "OFFER" }},
		{"guestOffer", func() { v.phase = phaseGuestOffer }},
		{"guestWait", func() { v.phase, v.answer = phaseGuestWait, "ANSWER" }},
		{"connected", func() { v.phase = phaseConnected }},
		{"error", func() { v.phase, v.errMsg = phaseIdle, "boom" }},
		{"name-focused", func() { v.phase, v.errMsg, v.nameFocused = phaseIdle, "", true }},
	}
	for _, ph := range phases {
		ph.setup()
		s.Draw(buf) // must not panic; draws the launcher + (when open) the panel
	}

	// The remote decoration is reported through the public read-back.
	decs := s.CollabRemoteDecorations()
	if len(decs) != 2 || decs[0].Label != "Alice" || decs[0].ColorHex != "#112233" || decs[0].Col != 2 {
		t.Fatalf("CollabRemoteDecorations = %+v", decs)
	}
}

func TestCollabSetNameAndOpen(t *testing.T) {
	s := newTestState(t, false)
	s.SetCollabName("Zaphod")
	if s.CollabName() != "Zaphod" {
		t.Fatalf("name = %q", s.CollabName())
	}
	s.SetCollabOpen(true)
	if !s.CollabActive() {
		t.Fatal("SetCollabOpen(true) did not open")
	}
	s.SetCollabOpen(false)
	if s.CollabActive() {
		t.Fatal("SetCollabOpen(false) did not close")
	}
}

func TestNopBackend(t *testing.T) {
	var b nopBackend
	if b.Connected() || b.PeerCount() != 0 {
		t.Fatal("nop backend should be unconnected with no peers")
	}
	b.SetOnChange(func() {})
	b.Disconnect()

	var hostErr, joinErr, acceptErr error
	b.Host("n", toolkit.RGBA{}, func(_ string, e error) { hostErr = e })
	b.Join("n", toolkit.RGBA{}, "o", func(_ string, e error) { joinErr = e })
	b.AcceptAnswer("a", func(e error) { acceptErr = e })
	if !errors.Is(hostErr, errNoBrowser) || !errors.Is(joinErr, errNoBrowser) || !errors.Is(acceptErr, errNoBrowser) {
		t.Fatalf("nop backend errors: host=%v join=%v accept=%v", hostErr, joinErr, acceptErr)
	}
}

func TestHexRoundTrip(t *testing.T) {
	c := toolkit.RGBA{R: 0xAB, G: 0xCD, B: 0xEF, A: 0xFF}
	if got := hexColor(c); got != "#abcdef" {
		t.Fatalf("hexColor = %q", got)
	}
	back, ok := parseHex("#ABCDEF")
	if !ok || back.R != 0xAB || back.G != 0xCD || back.B != 0xEF || back.A != 0xFF {
		t.Fatalf("parseHex = %+v ok=%v", back, ok)
	}
	if _, ok := parseHex("#abcdef88"); !ok {
		t.Fatal("8-digit hex should parse")
	}
	for _, bad := range []string{"", "#12", "nothex", "#zzzzzz"} {
		if _, ok := parseHex(bad); ok {
			t.Fatalf("parseHex(%q) should fail", bad)
		}
	}
}

func TestDefaultICEServersAreSTUN(t *testing.T) {
	s := newTestState(t, false)
	// Out of the box a participant is configured with public STUN, so
	// collaboration crosses NATs without any setup.
	got := s.CollabICEServers()
	if len(got) == 0 {
		t.Fatal("default ICE configuration is empty; collaboration would not cross NATs")
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "stun:") {
			t.Fatalf("default ICE server %q is not a STUN URL", u)
		}
	}
	// The full config carries no credentials for a bare STUN default.
	for _, sv := range s.CollabICEConfig() {
		if sv.Username != "" || sv.Credential != "" {
			t.Fatalf("default STUN server %q should carry no credentials", sv.URL)
		}
	}
}

func TestParseICEServersAndSet(t *testing.T) {
	cases := []struct {
		in   string
		want []ICEServer
	}{
		{"", nil},
		{"   ,  ,", nil},
		{"stun:a:1", []ICEServer{{URL: "stun:a:1"}}},
		{
			" stun:a:1 , turn:b:2|user|secret ",
			[]ICEServer{{URL: "stun:a:1"}, {URL: "turn:b:2", Username: "user", Credential: "secret"}},
		},
		// A URL with a username but no credential, and trailing empty fields.
		{"turn:c:3|onlyuser|", []ICEServer{{URL: "turn:c:3", Username: "onlyuser"}}},
		{"turn:d:4|", []ICEServer{{URL: "turn:d:4"}}},
	}
	for _, c := range cases {
		got := parseICEServers(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("parseICEServers(%q) = %d entries, want %d (%+v)", c.in, len(got), len(c.want), got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseICEServers(%q)[%d] = %+v, want %+v", c.in, i, got[i], c.want[i])
			}
		}
	}

	// SetCollabICEServers reconfigures both the URL view and the full config; an
	// empty string clears it back to host-candidate-only.
	s := newTestState(t, false)
	s.SetCollabICEServers("stun:x:1, turn:y:2|u|p")
	if urls := s.CollabICEServers(); len(urls) != 2 || urls[0] != "stun:x:1" || urls[1] != "turn:y:2" {
		t.Fatalf("CollabICEServers after set = %v", urls)
	}
	cfg := s.CollabICEConfig()
	if len(cfg) != 2 || cfg[1].Username != "u" || cfg[1].Credential != "p" {
		t.Fatalf("CollabICEConfig after set = %+v", cfg)
	}
	s.SetCollabICEServers("")
	if len(s.CollabICEServers()) != 0 {
		t.Fatalf("empty config should clear the servers, got %v", s.CollabICEServers())
	}
}

func TestCollabButtonRects(t *testing.T) {
	s := newTestState(t, false)
	// Closed: only the launcher is exposed.
	r := s.CollabButtonRects()
	if _, ok := r["launcher"]; !ok {
		t.Fatal("launcher rect not exposed while closed")
	}
	if _, ok := r["host"]; ok {
		t.Fatal("panel buttons should not be exposed while closed")
	}
	// Open + idle: Host and Join buttons and the name field appear.
	s.SetCollabOpen(true)
	r = s.CollabButtonRects()
	for _, name := range []string{"launcher", "name", "host", "join", "close", "shuffle"} {
		rc, ok := r[name]
		if !ok {
			t.Fatalf("expected %q rect while open+idle; have %v", name, keysOf(r))
		}
		if rc[2] <= 0 || rc[3] <= 0 {
			t.Fatalf("%q rect has non-positive size %v", name, rc)
		}
	}
	// Every button role maps to its stable, non-empty name; the non-button roles
	// (roleNone / roleNameField) map to the empty string.
	wantNames := map[collabRole]string{
		roleClose:       "close",
		roleHost:        "host",
		roleJoin:        "join",
		roleCopyOffer:   "copyOffer",
		roleCopyAnswer:  "copyAnswer",
		rolePasteOffer:  "pasteOffer",
		rolePasteAnswer: "pasteAnswer",
		roleShuffle:     "shuffle",
		roleCancel:      "cancel",
		roleDisconnect:  "disconnect",
		roleNone:        "",
		roleNameField:   "",
	}
	for role, want := range wantNames {
		if got := collabRoleName(role); got != want {
			t.Fatalf("collabRoleName(%d) = %q, want %q", role, got, want)
		}
	}
}

// keysOf lists a rect map's keys for a failure message.
func keysOf(m map[string][4]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCollabPhaseAndConnectedAccessors(t *testing.T) {
	s := newTestState(t, false)
	if s.CollabConnected() {
		t.Fatal("fresh state should be unconnected")
	}
	if s.CollabPeerCount() != 0 {
		t.Fatal("fresh state should have no peers")
	}
	if s.CollabPhase() != int(phaseIdle) {
		t.Fatal("fresh state should be idle")
	}
}
