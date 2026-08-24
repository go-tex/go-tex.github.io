// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// collabSeedCounter distinguishes the rng stream of every collabView built in
// one process, so two peers in one page (the headless proof) get DIFFERENT
// default names and colours rather than colliding on the same seed.
var collabSeedCounter atomic.Int64

// collabSeed returns a distinct seed per collabView.
func collabSeed() int64 {
	return time.Now().UnixNano() ^ int64(uint64(collabSeedCounter.Add(1))*0x9E3779B97F4A7C15)
}

// This file is the tagless, browser-free half of the playground's live
// collaborative-editing feature: the session state machine, the copy-paste
// signalling flow, the display-name/colour bookkeeping and the whole toolkit
// overlay panel — everything that can be built and exercised by a native
// `go test`. The syscall/js glue that actually opens a WebRTC connection, runs
// the collab Server/Client and binds the shared document to the editor lives in
// collab_js.go behind the [collabBackend] seam, so this file never imports
// syscall/js and stays 100 %-coverable off a browser.
//
// # How two peers meet, server-less
//
// Signalling is a person carrying one blob each way (paste it into the call you
// are already on), so there is no server to sign a document into — it works on
// GitHub Pages. One peer HOSTS (it holds the document, playing the collab
// Server) and the other JOINS:
//
//   - Host clicks Host → [State.CollabHost] makes the offer blob; the user copies
//     it to their peer. The user pastes the peer's ANSWER back →
//     [State.CollabAcceptAnswer] completes the handshake and the channel opens.
//   - Guest clicks Join, pastes the host's OFFER → [State.CollabJoin] makes the
//     answer blob; the user copies it back to the host. Once the host accepts,
//     the channel opens and the guest joins the shared document.
//
// Both editors are then bound to the same CRDT text part, so edits and carets
// flow between them (see collab_js.go and toolkit.CollabText).

// docName / textName name the shared composite document and the text part inside
// it that the editor is bound to. Every participant — the guest and the host's
// own editor — joins the SAME document name, so they converge on one replica.
const (
	docName  = "playground"
	textName = "file:main.tex"
)

// collabPhase is where a session is in the copy-paste handshake.
type collabPhase int

const (
	// phaseIdle offers the Host / Join choice: no session yet.
	phaseIdle collabPhase = iota
	// phaseHostWait is the host after it made its offer: the offer is ready to
	// copy and it is waiting for the peer's answer to be pasted back.
	phaseHostWait
	// phaseGuestOffer is the guest right after Join: it needs the host's offer
	// pasted in before it can answer.
	phaseGuestOffer
	// phaseGuestWait is the guest after it answered: the answer is ready to copy
	// back and it is waiting for the host to accept and the channel to open.
	phaseGuestWait
	// phaseConnected is a live session.
	phaseConnected
)

// CollabDecoration is one remote participant's caret as it is painted into the
// local editor — the read-back a headless test asserts on to prove a peer's
// caret and colour crossed the wire. It mirrors one toolkit.Decoration.
type CollabDecoration struct {
	Label     string // the peer's display name
	ColorHex  string // the peer's caret colour as "#rrggbb"
	Line, Col int    // the peer's caret position (rune coordinates)
}

// collabBackend is the browser-only seam this file drives: the real
// implementation (collab_js.go) opens the WebRTC connection, runs the collab
// Server/Client and binds the shared document to the editor. A native build
// gets [nopBackend], so the state machine and the panel are fully testable
// without a browser.
//
// Host, Join and AcceptAnswer are asynchronous (ICE gathering and the channel
// opening take time): each reports its blob (or error) through done, which the
// implementation may call from another goroutine. A later transition to a live
// connection is reported through the [collabBackend.SetOnChange] hook, since it
// happens after done has already returned the blob the user must relay.
type collabBackend interface {
	// Host starts hosting and yields the offer blob to hand to the peer.
	Host(name string, color toolkit.RGBA, done func(offer string, err error))
	// Join takes the host's offer and yields the answer blob to hand back.
	Join(name string, color toolkit.RGBA, offer string, done func(answer string, err error))
	// AcceptAnswer completes the host's handshake with the peer's answer.
	AcceptAnswer(answer string, done func(err error))
	// Disconnect tears the whole session down.
	Disconnect()
	// Connected reports whether a peer's channel is open and joined.
	Connected() bool
	// PeerCount is how many remote participants are in the document.
	PeerCount() int
	// SetOnChange installs the hook fired when the connection state, the peer set
	// or the shared text changed, so the view repaints and the phase advances.
	SetOnChange(func())
}

// collabView is the "Collaborate" affordance: a launcher button and, when open,
// a modal overlay panel driving the copy-paste handshake. It owns the session
// phase and the display name/colour, and reflects the [collabBackend] state.
type collabView struct {
	s       *State
	backend collabBackend
	repaint func() // host repaint hook (the wasm driver's render); nil in tests
	clipboardHooks

	open        bool
	phase       collabPhase
	busy        bool   // an async handshake step is in flight
	errMsg      string // last error, shown in the panel
	offer       string // the host's offer blob, to copy to the peer
	answer      string // the guest's answer blob, to copy back to the host
	name        string // this participant's display name
	color       toolkit.RGBA
	nameFocused bool // the name field has keyboard focus (typing edits the name)

	// iceServers is the STUN/TURN configuration the browser backend hands to every
	// WebRTC peer. It defaults to public STUN (defaultICEServers) so collaboration
	// works across NATs out of the box; SetCollabICEServers reconfigures it.
	iceServers []ICEServer

	// iceText is the editable value of the panel's "ICE servers (STUN/TURN)" field,
	// held as an observable (the app's MVVM contract) so typing mutates one source
	// of truth the view renders; committing it (blur/Enter) parses it through
	// [parseICEServers] into iceServers and persists it via icePersist.
	iceText    *mvvm.Observable[string]
	iceFocused bool             // the ICE field has keyboard focus
	icePersist func(csv string) // host hook: persist the config (localStorage); nil in tests

	rng *rand.Rand

	// geometry, recomputed by layout() before every draw and hit-test.
	launcher toolkit.Rect
	panel    toolkit.Rect
	buttons  []collabItem
	labels   []collabLabel
	swatches []collabSwatch
	nameRect toolkit.Rect
	iceRect  toolkit.Rect

	// Persistent toolkit widgets the panel is built from — created once and
	// re-used every frame so they hold their own interactive state (a Button's
	// pressed/hover face, an Entry's caret) between the mousedown that presses a
	// control and the mouseup that releases it. The panel draws and routes events
	// through these instead of hand-painting rectangles + text, so press / hover /
	// focus feedback is intrinsic to the widgets rather than absent. See the
	// widget accessors (ensureWidgets / btn / label / swatch).
	launcherBtn *toolkit.Button
	scrim       *toolkit.Backdrop              // modal dim behind the panel
	card        *toolkit.Backdrop              // the panel's Surface body + border
	btns        map[collabRole]*toolkit.Button // one persistent Button per role
	nameEntry   *toolkit.Entry
	iceEntry    *toolkit.Entry
	labelPool   []*toolkit.Label // reused, one per visible text line
	swatchPool  []*toolkit.Backdrop
}

// collabItem is one clickable button in the panel: its role drives dispatch.
type collabItem struct {
	role  collabRole
	rect  toolkit.Rect
	label string
}

// collabLabel is one line of static text in the panel.
type collabLabel struct {
	rect toolkit.Rect
	text string
	ink  toolkit.RGBA // zero (A==0) inherits the theme
}

// collabSwatch is a small colour chip (a peer's or the local caret colour).
type collabSwatch struct {
	rect  toolkit.Rect
	color toolkit.RGBA
}

// collabRole names what a panel button does; dispatch switches on it.
type collabRole int

const (
	roleNone collabRole = iota
	roleClose
	roleHost
	roleJoin
	roleCopyOffer
	roleCopyAnswer
	rolePasteOffer
	rolePasteAnswer
	roleShuffle
	roleCancel
	roleDisconnect
	roleNameField
)

// caretColors is the palette a participant's caret colour is drawn from — eight
// hues chosen to stay legible against both the light and dark editor paper.
var caretColors = []toolkit.RGBA{
	{R: 0xE5, G: 0x39, B: 0x35, A: 0xFF}, // red
	{R: 0x1E, G: 0x88, B: 0xE5, A: 0xFF}, // blue
	{R: 0x43, G: 0xA0, B: 0x47, A: 0xFF}, // green
	{R: 0xF4, G: 0x8F, B: 0x00, A: 0xFF}, // orange
	{R: 0x8E, G: 0x24, B: 0xAA, A: 0xFF}, // purple
	{R: 0x00, G: 0xAC, B: 0xC1, A: 0xFF}, // cyan
	{R: 0xD8, G: 0x1B, B: 0x60, A: 0xFF}, // magenta
	{R: 0x6D, G: 0x4C, B: 0x41, A: 0xFF}, // brown
}

// nameAdjectives / nameAnimals seed the random default display name.
var (
	nameAdjectives = []string{"Swift", "Amber", "Cobalt", "Bright", "Quiet", "Bold", "Jade", "Coral"}
	nameAnimals    = []string{"Fox", "Heron", "Otter", "Lynx", "Wren", "Ibis", "Moth", "Hare"}
)

// newCollabView builds the affordance for s with a no-op backend (a native
// build stays here; the wasm driver swaps in the real backend via [State.EnableCollab]).
func newCollabView(s *State) *collabView {
	v := &collabView{
		s:          s,
		backend:    nopBackend{},
		iceServers: defaultICEServers(),
		rng:        rand.New(rand.NewSource(collabSeed())),
	}
	v.name = v.randomName()
	v.color = v.randomColor()
	// The ICE field is pre-filled with the effective configuration — the public
	// STUN default out of the box — so the user sees (and can edit) what the peers
	// will actually use.
	v.iceText = mvvm.NewObservable(iceConfigString(v.iceServers))
	return v
}

// --- ICE (STUN/TURN) server configuration ------------------------------------

// ICEServer is one STUN or TURN server the WebRTC peers use to discover an
// address both browsers can reach. URL is the server ("stun:host:port" or
// "turn:host:port"); Username and Credential are the long-term credentials a
// TURN relay requires and are empty for a STUN server. It mirrors one entry of
// an RTCPeerConnection's iceServers, so the Collaborate config shape can express
// a credentialed TURN relay — the sovereign coturn planned for the EU host — and
// not only credential-free STUN.
type ICEServer struct {
	URL        string
	Username   string
	Credential string
}

// defaultICEServers is the out-of-the-box configuration: Google's public STUN
// servers, so two browsers behind different NATs can each discover a
// server-reflexive address and connect today with no setup. STUN cannot relay
// through a symmetric NAT — that needs a TURN server, added through
// [State.SetCollabICEServers].
//
// TODO(go-tex): this public-STUN default is an interim. Replace it with the
// sovereign coturn URL(s) on the EU host once that relay is stood up (it will
// also supply TURN credentials, which the config shape above already carries).
// The maintainer stands up coturn; this is only the config point.
func defaultICEServers() []ICEServer {
	return []ICEServer{
		{URL: "stun:stun.l.google.com:19302"},
		{URL: "stun:stun1.l.google.com:19302"},
		{URL: "stun:stun2.l.google.com:19302"},
	}
}

// parseICEServers parses the Collaborate ICE configuration string: a
// comma-separated list of entries, each a STUN/TURN URL optionally followed by
// "|username|credential" for a credentialed TURN relay (e.g.
// "stun:stun.l.google.com:19302, turn:turn.eu.example:3478|user|secret").
// Whitespace around entries and fields is trimmed and blank entries are dropped;
// a string with no usable entry yields nil, which the backend treats as
// host-candidate-only (LAN / same-machine, the pre-configuration behaviour).
func parseICEServers(csv string) []ICEServer {
	var out []ICEServer
	for _, entry := range strings.Split(csv, ",") {
		fields := strings.Split(entry, "|")
		url := strings.TrimSpace(fields[0])
		if url == "" {
			continue
		}
		s := ICEServer{URL: url}
		if len(fields) > 1 {
			s.Username = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			s.Credential = strings.TrimSpace(fields[2])
		}
		out = append(out, s)
	}
	return out
}

// iceConfigString renders an ICE configuration back into the field's
// comma-separated form, the inverse of [parseICEServers]: a bare URL for a
// credential-free STUN server, "url|username|credential" for a credentialed TURN
// relay. It is what the panel field is pre-filled with and what a programmatic
// reconfiguration reflects back into the field.
func iceConfigString(servers []ICEServer) string {
	parts := make([]string, 0, len(servers))
	for _, sv := range servers {
		p := sv.URL
		if sv.Username != "" || sv.Credential != "" {
			p += "|" + sv.Username + "|" + sv.Credential
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// SetCollabICEServers reconfigures the WebRTC ICE (STUN/TURN) servers from a
// config string (see [parseICEServers]); it takes effect on the next Host/Join.
// An empty or all-blank string restores host-candidate-only signalling. The panel
// field is kept in sync with the resulting configuration, so a programmatic
// reconfiguration (a restored setting, the gotexSetICEServers hook) shows up in
// the visible field.
func (s *State) SetCollabICEServers(csv string) {
	s.collab.iceServers = parseICEServers(csv)
	s.collab.iceText.Set(iceConfigString(s.collab.iceServers))
	s.collab.refresh()
}

// CollabICEConfig is the configured ICE servers, credentials included — the
// browser backend reads this to build each peer's RTCPeerConnection.
func (s *State) CollabICEConfig() []ICEServer { return s.collab.iceServers }

// CollabICEServers is the configured ICE server URLs, for the config field's
// current value and headless introspection.
func (s *State) CollabICEServers() []string {
	out := make([]string, 0, len(s.collab.iceServers))
	for _, sv := range s.collab.iceServers {
		out = append(out, sv.URL)
	}
	return out
}

// CollabICEText is the current text of the panel's ICE-servers field, for the DOM
// host and headless introspection (the visible, editable value).
func (s *State) CollabICEText() string { return s.collab.iceText.Get() }

// commitICE applies the ICE field's current text: it parses the value through
// [parseICEServers] into the live configuration and persists it, so the NEXT
// Host/Join uses it. An empty or all-blank field is treated as "use the default"
// — it falls back to the public STUN servers rather than breaking collaboration —
// and the field is refilled with that effective default so what is shown always
// matches what the peers will use.
func (v *collabView) commitICE() {
	csv := strings.TrimSpace(v.iceText.Get())
	if csv == "" {
		v.iceServers = defaultICEServers()
		v.iceText.Set(iceConfigString(v.iceServers))
	} else {
		// Reuse #29's parser via the public setter, which also normalises the field.
		v.s.SetCollabICEServers(csv)
	}
	if v.icePersist != nil {
		v.icePersist(v.iceText.Get())
	}
	v.refresh()
}

// blurICE defocuses the ICE field, committing its value first. It is the field's
// blur path (a click away, Enter, Escape).
func (v *collabView) blurICE() {
	if !v.iceFocused {
		return
	}
	v.iceFocused = false
	v.commitICE()
}

// attach swaps in a real backend and the host repaint hook, wiring the backend's
// change signal to the view. Called by the wasm driver.
func (v *collabView) attach(b collabBackend, repaint func()) {
	v.backend = b
	v.repaint = repaint
	b.SetOnChange(v.onBackendChange)
}

// randomName / randomColor pick a default identity.
func (v *collabView) randomName() string {
	return nameAdjectives[v.rng.Intn(len(nameAdjectives))] + " " + nameAnimals[v.rng.Intn(len(nameAnimals))]
}

func (v *collabView) randomColor() toolkit.RGBA { return caretColors[v.rng.Intn(len(caretColors))] }

// refresh repaints if a host hook is installed.
func (v *collabView) refresh() {
	v.s.dirty = true
	if v.repaint != nil {
		v.repaint()
	}
}

// onBackendChange advances the phase when a live connection appears or drops and
// repaints. It is the backend's "something moved" signal (connect, peer join or
// leave, a remote edit).
func (v *collabView) onBackendChange() {
	switch {
	case v.backend.Connected():
		if v.phase != phaseConnected {
			v.phase = phaseConnected
			v.busy = false
		}
	case v.phase == phaseConnected:
		// The peer left or the channel dropped; fall back to idle.
		v.phase = phaseIdle
		v.offer, v.answer = "", ""
		v.errMsg = "the peer disconnected"
	}
	v.refresh()
}

// --- the copy-paste handshake, driven by the panel and the headless proof ----

// CollabHost starts a hosting session: it seeds the shared document with the
// current editor source, binds the editor to it and makes the offer blob to hand
// to the peer. done (optional) receives the offer or the error; the panel passes
// nil and reads v.offer, the headless proof passes a chained handler.
func (s *State) CollabHost(done func(offer string, err error)) {
	v := s.collab
	v.busy, v.errMsg = true, ""
	v.backend.Host(v.name, v.color, func(offer string, err error) {
		v.busy = false
		if err != nil {
			v.errMsg = err.Error()
		} else {
			v.offer, v.phase = offer, phaseHostWait
		}
		if done != nil {
			done(offer, err)
		}
		v.refresh()
	})
}

// CollabJoin joins a hosting peer from its pasted offer: it binds the editor to
// the shared document and makes the answer blob to hand back to the host. The
// live connection follows once the host accepts (reported via the change hook).
func (s *State) CollabJoin(offer string, done func(answer string, err error)) {
	v := s.collab
	offer = strings.TrimSpace(offer)
	if offer == "" {
		v.errMsg = "paste the host's invitation first"
		v.refresh()
		if done != nil {
			done("", errEmptyBlob)
		}
		return
	}
	v.busy, v.errMsg = true, ""
	v.backend.Join(v.name, v.color, offer, func(answer string, err error) {
		v.busy = false
		if err != nil {
			v.errMsg = err.Error()
		} else {
			v.answer, v.phase = answer, phaseGuestWait
		}
		if done != nil {
			done(answer, err)
		}
		v.refresh()
	})
}

// CollabAcceptAnswer completes the host's handshake with the guest's pasted
// answer; the live connection follows (reported via the change hook).
func (s *State) CollabAcceptAnswer(answer string, done func(err error)) {
	v := s.collab
	answer = strings.TrimSpace(answer)
	if answer == "" {
		v.errMsg = "paste your peer's reply first"
		v.refresh()
		if done != nil {
			done(errEmptyBlob)
		}
		return
	}
	v.busy, v.errMsg = true, ""
	v.backend.AcceptAnswer(answer, func(err error) {
		v.busy = false
		if err != nil {
			v.errMsg = err.Error()
		}
		if done != nil {
			done(err)
		}
		v.refresh()
	})
}

// CollabDisconnect tears the session down and returns the panel to its idle
// choice.
func (s *State) CollabDisconnect() {
	v := s.collab
	v.backend.Disconnect()
	v.phase = phaseIdle
	v.offer, v.answer, v.errMsg = "", "", ""
	v.busy = false
	v.refresh()
}

// errEmptyBlob is returned when a paste step got nothing to work with.
var errEmptyBlob = fmt.Errorf("collab: nothing to paste")

// --- introspection for the headless proof and the DOM host -------------------

// CollabConnected reports whether a live peer session is up.
func (s *State) CollabConnected() bool { return s.collab.backend.Connected() }

// CollabPeerCount is how many remote participants are in the document.
func (s *State) CollabPeerCount() int { return s.collab.backend.PeerCount() }

// CollabPhase is the session phase as an int (0 idle … 4 connected), for a
// headless assertion.
func (s *State) CollabPhase() int { return int(s.collab.phase) }

// CollabActive reports whether the panel is open.
func (s *State) CollabActive() bool { return s.collab.open }

// SetCollabOpen opens or closes the Collaborate panel (host / headless-proof
// control; the user toggles it with the launcher button).
func (s *State) SetCollabOpen(open bool) { s.collab.open = open; s.collab.refresh() }

// CollabName / SetCollabName read and set this participant's display name.
func (s *State) CollabName() string     { return s.collab.name }
func (s *State) SetCollabName(n string) { s.collab.name = n; s.collab.refresh() }

// CollabColorHex is this participant's caret colour as "#rrggbb".
func (s *State) CollabColorHex() string { return hexColor(s.collab.color) }

// CollabOffer / CollabAnswer expose the current signalling blobs (for a DOM host
// that shows them in a real textarea, or a test).
func (s *State) CollabOffer() string  { return s.collab.offer }
func (s *State) CollabAnswer() string { return s.collab.answer }

// collabRoleName maps a panel role to a stable string key for the headless
// two-tab harness, which locates a button by name and clicks its real rect.
func collabRoleName(r collabRole) string {
	switch r {
	case roleClose:
		return "close"
	case roleHost:
		return "host"
	case roleJoin:
		return "join"
	case roleCopyOffer:
		return "copyOffer"
	case roleCopyAnswer:
		return "copyAnswer"
	case rolePasteOffer:
		return "pasteOffer"
	case rolePasteAnswer:
		return "pasteAnswer"
	case roleShuffle:
		return "shuffle"
	case roleCancel:
		return "cancel"
	case roleDisconnect:
		return "disconnect"
	default:
		return ""
	}
}

// CollabButtonRects returns the device-pixel [x,y,w,h] rectangles of the
// Collaborate launcher and every currently-visible panel control, keyed by a
// stable name ("launcher", "name", and one per button role), so a headless
// two-tab harness can dispatch REAL pointer clicks at the actual buttons — the
// same handleClick → dispatch path a user drives — rather than calling the
// handlers directly. Host-facing introspection only.
func (s *State) CollabButtonRects() map[string][4]int {
	v := s.collab
	v.layout()
	rect := func(r toolkit.Rect) [4]int { return [4]int{r.X, r.Y, r.W, r.H} }
	out := map[string][4]int{"launcher": rect(v.launcher)}
	if !v.open {
		return out
	}
	if v.nameRect.W > 0 {
		out["name"] = rect(v.nameRect)
	}
	if v.iceRect.W > 0 {
		out["ice"] = rect(v.iceRect)
	}
	for _, b := range v.buttons {
		if name := collabRoleName(b.role); name != "" {
			out[name] = rect(b.rect)
		}
	}
	return out
}

// CollabRemoteDecorations returns the remote carets currently painted into the
// editor — the proof that a peer's caret, colour and name crossed the wire. It
// reads the editor's live Decorations, which toolkit.CollabText rebuilt from the
// session's peers.
func (s *State) CollabRemoteDecorations() []CollabDecoration {
	out := make([]CollabDecoration, 0, len(s.editor.Decorations))
	for _, d := range s.editor.Decorations {
		out = append(out, CollabDecoration{
			Label:    d.Label,
			ColorHex: hexColor(d.Color),
			Line:     d.CursorLine,
			Col:      d.CursorCol,
		})
	}
	return out
}

// --- the panel: layout, draw, input ------------------------------------------

// collabPanelW is the panel's logical width; layout scales it to the metric
// scale and clamps it to the surface.
const collabPanelW = 380

// layout recomputes the launcher rect and, when open, the panel geometry and its
// interactive items. It is idempotent and cheap, called before every draw and
// every hit-test so neither depends on the other's ordering.
func (v *collabView) layout() {
	pad := toolkit.Scaled(8)
	bh := toolkit.Scaled(26)
	// Launcher: a pill in the top-right corner of the toolbar row.
	lw := toolkit.Scaled(collabLauncherW)
	v.launcher = toolkit.Rect{X: v.s.w - lw - pad, Y: toolkit.Scaled(4), W: lw, H: v.s.toolbarH - 2*toolkit.Scaled(4)}
	if v.s.toolbarH == 0 { // before the first layout() of the host
		v.launcher.H = bh
	}

	v.buttons = v.buttons[:0]
	v.labels = v.labels[:0]
	v.swatches = v.swatches[:0]
	v.nameRect = toolkit.Rect{}
	v.iceRect = toolkit.Rect{}
	if !v.open {
		return
	}

	pw := toolkit.Scaled(collabPanelW)
	if pw > v.s.w-2*pad {
		pw = v.s.w - 2*pad
	}
	x := (v.s.w - pw) / 2
	y := pad + v.s.toolbarH
	line := toolkit.Scaled(22)
	gap := toolkit.Scaled(6)
	innerX := x + pad
	innerW := pw - 2*pad

	cur := y + pad
	// Title row + close button.
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW - bh, H: line}, text: "Collaborate"})
	v.buttons = append(v.buttons, collabItem{role: roleClose, rect: toolkit.Rect{X: x + pw - pad - bh, Y: cur, W: bh, H: bh}, label: "X"})
	cur += bh + gap

	// Identity row: "You:" + name field + colour swatch + Shuffle.
	shuffleW := toolkit.Scaled(72)
	swatch := toolkit.Scaled(18)
	youW := toolkit.Scaled(34)
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: youW, H: bh}, text: "You:"})
	nameX := innerX + youW
	nameW := innerW - youW - swatch - gap - shuffleW - 2*gap
	v.nameRect = toolkit.Rect{X: nameX, Y: cur, W: nameW, H: bh}
	v.swatches = append(v.swatches, collabSwatch{rect: toolkit.Rect{X: nameX + nameW + gap, Y: cur + (bh-swatch)/2, W: swatch, H: swatch}, color: v.color})
	v.buttons = append(v.buttons, collabItem{role: roleShuffle, rect: toolkit.Rect{X: x + pw - pad - shuffleW, Y: cur, W: shuffleW, H: bh}, label: "Shuffle"})
	cur += bh + gap + gap

	// ICE (STUN/TURN) row: a labelled field so a user can point the WebRTC peers at
	// their own relay. Empty falls back to public STUN; a credentialed sovereign
	// coturn URL goes here later (see defaultICEServers's TODO).
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: "ICE servers (STUN/TURN):"})
	cur += line
	v.iceRect = toolkit.Rect{X: innerX, Y: cur, W: innerW, H: bh}
	cur += bh + gap
	hintInk := toolkit.RGBA{R: 0x8A, G: 0x8A, B: 0x8A, A: 0xFF}
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: "Comma-separated stun:host:port or turn:host:port|user|cred.", ink: hintInk})
	cur += line
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: "Empty falls back to public STUN.", ink: hintInk})
	cur += line + gap

	// Phase-specific rows.
	addLabel := func(text string) {
		v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: text})
		cur += line
	}
	addButtons := func(items ...collabItem) {
		n := len(items)
		bw := (innerW - (n-1)*gap) / n
		for i := range items {
			items[i].rect = toolkit.Rect{X: innerX + i*(bw+gap), Y: cur, W: bw, H: bh}
			v.buttons = append(v.buttons, items[i])
		}
		cur += bh + gap
	}

	switch {
	case v.busy:
		addLabel("Working…")
	case v.phase == phaseIdle:
		addLabel("Edit this document together, peer-to-peer.")
		addButtons(collabItem{role: roleHost, label: "Host"}, collabItem{role: roleJoin, label: "Join"})
	case v.phase == phaseHostWait:
		addLabel("1. Send this invitation to your peer:")
		addButtons(collabItem{role: roleCopyOffer, label: "Copy invitation"})
		addLabel("2. Paste their reply here to connect:")
		addButtons(collabItem{role: rolePasteAnswer, label: "Paste reply & connect"})
		addButtons(collabItem{role: roleCancel, label: "Cancel"})
	case v.phase == phaseGuestOffer:
		addLabel("Paste your host's invitation:")
		addButtons(collabItem{role: rolePasteOffer, label: "Paste invitation"})
		addButtons(collabItem{role: roleCancel, label: "Cancel"})
	case v.phase == phaseGuestWait:
		addLabel("Send this reply back to the host:")
		addButtons(collabItem{role: roleCopyAnswer, label: "Copy reply"})
		addLabel("Waiting for the host to connect…")
		addButtons(collabItem{role: roleCancel, label: "Cancel"})
	case v.phase == phaseConnected:
		addLabel(v.connectedSummary())
		for _, p := range v.s.CollabRemoteDecorations() {
			c, _ := parseHex(p.ColorHex)
			v.swatches = append(v.swatches, collabSwatch{rect: toolkit.Rect{X: innerX, Y: cur + (line-toolkit.Scaled(12))/2, W: toolkit.Scaled(12), H: toolkit.Scaled(12)}, color: c})
			name := p.Label
			if name == "" {
				name = "(anonymous)"
			}
			v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX + toolkit.Scaled(18), Y: cur, W: innerW - toolkit.Scaled(18), H: line}, text: name})
			cur += line
		}
		addButtons(collabItem{role: roleDisconnect, label: "Disconnect"})
	}

	if v.errMsg != "" {
		v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: "⚠ " + v.errMsg, ink: toolkit.RGBA{R: 0xE5, G: 0x39, B: 0x35, A: 0xFF}})
		cur += line
	}

	v.panel = toolkit.Rect{X: x, Y: y, W: pw, H: cur + pad - y}
}

// collabLauncherW / collabLauncherH are the launcher pill's logical size.
const (
	collabLauncherW = 96
)

// connectedSummary is the connected header line: the peer count.
func (v *collabView) connectedSummary() string {
	n := v.backend.PeerCount()
	switch n {
	case 0:
		return "Connected. Waiting for your peer…"
	case 1:
		return "Connected — 1 peer editing with you:"
	default:
		return fmt.Sprintf("Connected — %d peers editing with you:", n)
	}
}

// ensureWidgets lazily builds the persistent toolkit widgets the panel is drawn
// from and routes events through. Called at the top of every draw / input path so
// a freshly-constructed view (or a test) always has them.
func (v *collabView) ensureWidgets() {
	if v.launcherBtn != nil {
		return
	}
	// The launcher opens the panel from its own OnClick, so a press on it depresses
	// the pill through the same toolkit path as any other button.
	v.launcherBtn = toolkit.NewButton("", func() { v.open = true; v.refresh() })
	v.scrim = &toolkit.Backdrop{Fill: toolkit.RGBA{A: 0x66}, Interactive: true}
	v.card = &toolkit.Backdrop{}
	v.btns = map[collabRole]*toolkit.Button{}
	v.nameEntry = toolkit.NewEntry("")
	v.iceEntry = toolkit.NewEntry("")
}

// btn returns the persistent Button for a role, creating it (wired to dispatch
// that role) on first use so its pressed / hover state survives between frames.
func (v *collabView) btn(role collabRole) *toolkit.Button {
	if b := v.btns[role]; b != nil {
		return b
	}
	r := role
	b := toolkit.NewButton("", func() { v.dispatch(r) })
	v.btns[role] = b
	return b
}

// label / swatch return reused, pooled widgets for the i-th visible text line /
// colour chip, growing the pool as needed — so no widget is allocated per frame.
func (v *collabView) label(i int) *toolkit.Label {
	for len(v.labelPool) <= i {
		v.labelPool = append(v.labelPool, toolkit.NewLabel(""))
	}
	return v.labelPool[i]
}

func (v *collabView) swatch(i int) *toolkit.Backdrop {
	for len(v.swatchPool) <= i {
		v.swatchPool = append(v.swatchPool, &toolkit.Backdrop{})
	}
	return v.swatchPool[i]
}

// draw paints the launcher and, when open, the overlay panel — entirely from
// persistent toolkit widgets (a Backdrop scrim + card, Entry fields, Label lines,
// Backdrop swatches, Button controls), so every element carries its own press /
// hover / focus feedback and nothing is hand-painted.
func (v *collabView) draw(p painter.Painter, theme *toolkit.Theme) {
	v.layout()
	v.ensureWidgets()

	// Launcher pill: a real Button that depresses on press and lights (Selected)
	// while the panel is open or a session is live.
	v.launcherBtn.Label().Set(v.launcherLabel())
	v.launcherBtn.SetBounds(v.launcher)
	v.launcherBtn.Selected().Set(v.open || v.phase == phaseConnected)
	v.launcherBtn.Draw(p, theme)

	if !v.open {
		return
	}

	// Modal scrim + panel body, each a Backdrop rather than hand-filled rects.
	v.scrim.SetBounds(toolkit.Rect{X: 0, Y: v.s.toolbarH, W: v.s.w, H: v.s.h - v.s.toolbarH - v.s.statusH})
	v.scrim.Draw(p, theme)
	v.card.Fill = theme.Surface
	v.card.Stroke = theme.Border
	v.card.StrokeWidth = toolkit.Scaled(1)
	v.card.SetBounds(v.panel)
	v.card.Draw(p, theme)

	// Name field: a real Entry (own border, text + focus caret).
	if v.nameRect.W > 0 {
		v.nameEntry.SetBounds(v.nameRect)
		v.nameEntry.SetText(v.name)
		v.nameEntry.SetFocused(v.nameFocused)
		v.nameEntry.Draw(p, theme)
	}
	// ICE-servers field: a real Entry in the same style.
	if v.iceRect.W > 0 {
		v.iceEntry.SetBounds(v.iceRect)
		v.iceEntry.SetText(v.iceText.Get())
		v.iceEntry.SetFocused(v.iceFocused)
		v.iceEntry.Draw(p, theme)
	}

	// Static text lines, each a reused Label.
	for i, l := range v.labels {
		lb := v.label(i)
		lb.SetBounds(l.rect)
		lb.Text().Set(l.text)
		lb.Ink = l.ink
		lb.Draw(p, theme)
	}
	// Colour chips, each a reused Backdrop.
	for i, sw := range v.swatches {
		sb := v.swatch(i)
		sb.Fill = sw.color
		sb.SetBounds(sw.rect)
		sb.Draw(p, theme)
	}
	// Action buttons, each a persistent Button so it depresses on press.
	for _, it := range v.buttons {
		b := v.btn(it.role)
		b.Label().Set(it.label)
		b.SetBounds(it.rect)
		b.Draw(p, theme)
	}
}

// launcherLabel is the pill's caption.
func (v *collabView) launcherLabel() string {
	if v.phase == phaseConnected {
		return "● Live"
	}
	return "Collaborate"
}

// handleClick routes a pointer press to the launcher or, when the panel is open,
// to a panel control. Every hit is resolved through the target widget's own
// HitTest and delivered as a toolkit EventClick, so a Button depresses + fires
// through the toolkit rather than a hand-written rect test. An open panel is
// modal: it consumes every click over the body so the scene beneath it is inert.
// Returns whether it consumed the click.
func (v *collabView) handleClick(x, y int) bool {
	v.layout()
	v.ensureWidgets()
	if !v.open {
		v.launcherBtn.SetBounds(v.launcher)
		if v.launcherBtn.HitTest(x, y) {
			v.launcherBtn.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - v.launcher.X, Y: y - v.launcher.Y})
			return true
		}
		return false
	}
	// Modal: swallow everything, routing a control hit through the widget's HitTest.
	if v.iceRect.W > 0 {
		v.iceEntry.SetBounds(v.iceRect)
		if v.iceEntry.HitTest(x, y) {
			v.nameFocused = false
			v.iceFocused = true
			v.refresh()
			return true
		}
	}
	if v.nameRect.W > 0 {
		v.nameEntry.SetBounds(v.nameRect)
		if v.nameEntry.HitTest(x, y) {
			v.blurICE() // commit the ICE field if focus is leaving it
			v.nameFocused = true
			v.refresh()
			return true
		}
	}
	v.blurICE()
	v.nameFocused = false
	for _, it := range v.buttons {
		b := v.btn(it.role)
		b.SetBounds(it.rect)
		if b.HitTest(x, y) {
			b.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: x - it.rect.X, Y: y - it.rect.Y})
			return true
		}
	}
	v.refresh()
	return true
}

// handleMove routes a pointer move over the open modal panel to its buttons so
// each raises or clears its hover face through the toolkit. A no-op while closed.
func (v *collabView) handleMove(x, y int) {
	if !v.open {
		return
	}
	v.layout()
	v.ensureWidgets()
	for _, it := range v.buttons {
		b := v.btn(it.role)
		b.SetBounds(it.rect)
		b.OnEvent(toolkit.Event{Kind: toolkit.EventMouseMove, X: x - it.rect.X, Y: y - it.rect.Y})
	}
	v.s.dirty = true
}

// handleRelease ends a press: the launcher and every panel button clear their
// pressed face on the mouseup, so a depress is momentary. Called whenever this
// view captured the preceding press (even if the action closed the panel).
func (v *collabView) handleRelease(x, y int) {
	v.ensureWidgets()
	v.launcherBtn.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
	for _, b := range v.btns {
		b.OnEvent(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
	}
	v.refresh()
}

// dispatch runs the action of a clicked panel button.
func (v *collabView) dispatch(role collabRole) {
	switch role {
	case roleClose:
		v.open = false
	case roleHost:
		v.s.CollabHost(nil)
	case roleJoin:
		v.phase = phaseGuestOffer
	case rolePasteOffer:
		v.readClipboard(func(text string) { v.s.CollabJoin(text, nil) })
	case rolePasteAnswer:
		v.readClipboard(func(text string) { v.s.CollabAcceptAnswer(text, nil) })
	case roleCopyOffer:
		v.writeClipboard(v.offer)
	case roleCopyAnswer:
		v.writeClipboard(v.answer)
	case roleShuffle:
		v.name, v.color = v.randomName(), v.randomColor()
	case roleCancel, roleDisconnect:
		v.s.CollabDisconnect()
	}
	v.refresh()
}

// handleChar edits the display name when its field is focused. Returns whether
// it consumed the character.
func (v *collabView) handleChar(code string) bool {
	if !v.open {
		return false
	}
	if v.iceFocused {
		if r := []rune(code); len(r) == 1 {
			v.iceText.Set(v.iceText.Get() + code)
			v.refresh()
		}
		return true
	}
	if !v.nameFocused {
		return false
	}
	if r := []rune(code); len(r) == 1 && len(v.name) < 24 {
		v.name += code
		v.refresh()
	}
	return true
}

// handleKey handles editing keys in the focused name field (Backspace, Enter,
// Escape). Returns whether it consumed the key.
func (v *collabView) handleKey(code string) bool {
	if !v.open {
		return false
	}
	if code == "Escape" {
		switch {
		case v.iceFocused:
			v.blurICE()
		case v.nameFocused:
			v.nameFocused = false
		default:
			v.open = false
		}
		v.refresh()
		return true
	}
	if v.iceFocused {
		switch code {
		case "Backspace":
			if r := []rune(v.iceText.Get()); len(r) > 0 {
				v.iceText.Set(string(r[:len(r)-1]))
			}
		case "Enter", "Return":
			v.blurICE()
		}
		v.refresh()
		return true
	}
	if !v.nameFocused {
		return false
	}
	switch code {
	case "Backspace":
		if r := []rune(v.name); len(r) > 0 {
			v.name = string(r[:len(r)-1])
		}
	case "Enter", "Return":
		v.nameFocused = false
	}
	v.refresh()
	return true
}

// readClipboard / writeClipboard reach the OS clipboard through the host hooks
// the wasm driver installs; a native build has no clipboard, so both are no-ops.
func (v *collabView) readClipboard(cb func(string)) {
	if v.clipRead != nil {
		v.clipRead(cb)
	}
}

func (v *collabView) writeClipboard(text string) {
	if v.clipWrite != nil {
		v.clipWrite(text)
	}
}

// clipRead / clipWrite are the host clipboard hooks (installed by the wasm
// driver). Kept as fields so the panel stays free of syscall/js.
type clipboardHooks struct {
	clipRead  func(cb func(string))
	clipWrite func(text string)
}

// --- colour helpers ----------------------------------------------------------

// hexColor renders an opaque RGBA as "#rrggbb".
func hexColor(c toolkit.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// parseHex reads "#rrggbb" (or "#rrggbbaa") back into an opaque RGBA. A blank or
// malformed string yields the zero colour and ok=false.
func parseHex(s string) (toolkit.RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return toolkit.RGBA{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s[:6], "%02x%02x%02x", &r, &g, &b); err != nil {
		return toolkit.RGBA{}, false
	}
	return toolkit.RGBA{R: r, G: g, B: b, A: 0xFF}, true
}

// --- the native no-op backend ------------------------------------------------

// nopBackend is the backend a native build (and every test that does not inject
// its own) gets: collaboration needs a browser, so each handshake step reports
// that and nothing connects.
type nopBackend struct{}

func (nopBackend) Host(_ string, _ toolkit.RGBA, done func(string, error)) {
	done("", errNoBrowser)
}

func (nopBackend) Join(_ string, _ toolkit.RGBA, _ string, done func(string, error)) {
	done("", errNoBrowser)
}

func (nopBackend) AcceptAnswer(_ string, done func(error)) { done(errNoBrowser) }
func (b nopBackend) Disconnect()                           { _ = b } // no session to tear down
func (nopBackend) Connected() bool                         { return false }
func (nopBackend) PeerCount() int                          { return 0 }
func (b nopBackend) SetOnChange(func())                    { _ = b } // nothing ever changes

// errNoBrowser explains why the no-op backend never connects.
var errNoBrowser = fmt.Errorf("collab: live collaboration needs a browser")
