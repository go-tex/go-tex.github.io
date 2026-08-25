// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-gfx/qr"
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
	// phaseFailed is a handshake that will not complete: ICE found no candidate
	// pair, so no direct path to the peer exists (the common full-tunnel-VPN /
	// symmetric-NAT case, where STUN alone cannot traverse). The panel shows
	// [collabFailedMsg] pointing at the TURN fix and offers a reset to idle,
	// rather than waiting silently forever.
	phaseFailed
)

// collabFailedMsg is the guidance shown when a connection fails with no path to
// the peer: it names the likely cause (a full-tunnel VPN or a strict/symmetric
// NAT that STUN cannot traverse) and the fix (a TURN relay in the ICE field).
// layout splits it across Label lines (the toolkit Label does not wrap); the
// join of those lines is this exact string, which [State.CollabFailureMessage]
// also returns for the headless proof.
const collabFailedMsg = "Connection failed — no direct path to the peer. " +
	"If you're on a VPN or behind a strict NAT, add a TURN server in the ICE servers field above and retry."

// collabConnectingMsg is the acknowledgement shown the instant a Connect step is
// accepted (the guest's answer built, or the host's AcceptAnswer taken) and the
// WebRTC channel is being opened. It is deliberately distinct from the generic
// "Working…" (an in-flight handshake RPC) so the user sees their paste was taken
// and a connection is being attempted, rather than the panel appearing inert
// while ICE negotiates. It clears when the channel opens ([phaseConnected]), the
// attempt fails ([phaseFailed]) or the session is torn down.
const collabConnectingMsg = "Connecting to your peer…"

// collabClipReadErrMsg is shown when the browser refuses navigator.clipboard
// .readText() — Firefox blocks it for web content outright, and Chrome rejects it
// for a background/unfocused tab or without a permission grant. Before, that
// rejection was silent (readText's promise had no catch), so "Paste from
// clipboard" looked like it did nothing; now the panel points the user at the
// reliable path — a manual ⌘V/Ctrl+V into the visible field.
const collabClipReadErrMsg = "Couldn't read the clipboard — paste with ⌘V/Ctrl+V into the field."

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
	// ConnFailed reports whether the connection attempt failed with no usable path
	// to the peer — ICE reached "failed" (or lingered in "disconnected"), or the
	// channel never opened within the deadline. When true (and not Connected), the
	// view moves the panel to [phaseFailed] and shows [collabFailedMsg].
	ConnFailed() bool
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

	open         bool
	phase        collabPhase
	busy         bool   // an async handshake step is in flight
	connecting   bool   // a connect step was accepted; the peer channel is being opened
	errMsg       string // last error, shown in the panel
	offer        string // the host's offer blob, to copy to the peer
	answer       string // the guest's answer blob, to copy back to the host
	name         string // this participant's display name
	color        toolkit.RGBA
	nameFocused  bool // the name field has keyboard focus (typing edits the name)
	pasteFocused bool // the visible paste field has keyboard focus (⌘V / typing edits it)

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

	// pasteText is the editable value of the visible signalling-blob field the user
	// pastes into — the host's invitation on the guest side (phaseGuestOffer), the
	// peer's reply on the host side (phaseHostWait). Held as an observable (the app's
	// MVVM contract) so a ⌘V/Ctrl+V paste, a "Paste from clipboard" fill or typing
	// all mutate one source of truth the field renders; the primary Connect button
	// reads it. Its meaning is phase-specific and only one such field is ever shown,
	// so a single observable/entry is reused across the two phases and cleared on
	// every transition into them.
	pasteText *mvvm.Observable[string]

	// id is this participant's stable short identity: a handful of base32 characters
	// minted once from rng at construction (see [collabView.randomID]) and shown
	// beside the name in the identity row (You: <name> #A3F9). Unlike the cosmetic
	// name/colour — which roleShuffle re-randomises — the id is fixed for the whole
	// page load, so a peer can always tell WHICH client an invitation/reply came from.
	id string

	// baseURL is the origin+path a scan-to-connect QR/URL primes (see
	// [buildSignalURL]). It defaults to [signalBaseURL]; the js layer overrides it at
	// startup with the live location so a scanned code opens the right deployment.
	baseURL string

	// The scan-to-connect QR, computed ONCE when the offer/answer envelope is produced
	// (see [collabView.buildQR]) — never per frame. qrPixels is the RGBA raster the
	// persistent qrImage widget paints, qrW/qrH its source dimensions. qrShown is set
	// when a scannable code was produced; qrTooLarge is set when the payload exceeded
	// the version cap, so the panel shows the copy-paste fallback line instead of an
	// unscannably dense code.
	qrPixels   []byte
	qrW, qrH   int
	qrShown    bool
	qrTooLarge bool

	// The pasted peer's identity, decoded from the signalling envelope the user
	// pastes (see [decodeEnvelope]). peerKnown is set once a valid envelope is
	// consumed by CollabJoin / CollabAcceptAnswer, so the panel can name who it is
	// connecting to; it is cleared on teardown. peerColor is the peer's caret hue,
	// shown as a small swatch beside its name.
	peerID    string
	peerName  string
	peerColor toolkit.RGBA
	peerKnown bool

	rng *rand.Rand

	// geometry, recomputed by layout() before every draw and hit-test.
	launcher  toolkit.Rect
	panel     toolkit.Rect
	buttons   []collabItem
	labels    []collabLabel
	swatches  []collabSwatch
	nameRect  toolkit.Rect
	iceRect   toolkit.Rect
	pasteRect toolkit.Rect
	qrRect    toolkit.Rect // the panel square the scan-to-connect QR is drawn into

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
	pasteEntry  *toolkit.Entry   // the visible signalling-blob paste field
	qrImage     *toolkit.Image   // the persistent scan-to-connect QR image widget
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
	roleConnectOffer
	roleConnectAnswer
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
		baseURL:    signalBaseURL,
		rng:        rand.New(rand.NewSource(collabSeed())),
	}
	v.name = v.randomName()
	v.color = v.randomColor()
	// The id is drawn AFTER the name/colour so those two rng draws keep the exact
	// stream position seeded tests depend on; it is fixed for the page's lifetime.
	v.id = v.randomID()
	// The ICE field is pre-filled with the effective configuration — the public
	// STUN default out of the box — so the user sees (and can edit) what the peers
	// will actually use.
	v.iceText = mvvm.NewObservable(iceConfigString(v.iceServers))
	v.pasteText = mvvm.NewObservable("")
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

// idAlphabet is the character set randomID draws from: Crockford base32 (no I, L,
// O, U), so a short id stays unambiguous when a user reads it off the panel and
// compares it to a peer's.
const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// idLength is how many characters a client id has. Four base32 characters give ~1M
// distinct ids — plenty to tell two collaborators in a call apart at a glance.
const idLength = 4

// randomID mints this view's stable short identity from rng. It is deterministic
// under a fixed seed (so seeded tests are reproducible) and drawn once, at
// construction; roleShuffle never touches it.
func (v *collabView) randomID() string {
	b := make([]byte, idLength)
	for i := range b {
		b[i] = idAlphabet[v.rng.Intn(len(idAlphabet))]
	}
	return string(b)
}

// --- the peer-identity signalling envelope -----------------------------------

// collabEnvelope wraps a raw WebRTC signalling blob with the sender's identity, so
// that when a client pastes the other side's invitation/reply the panel can show
// WHO it came from before connecting. It is the on-the-wire shape of a "Copy
// invitation" / "Copy reply" blob (base64 of this JSON); the inner SDP the backend
// consumes is carried unchanged in SDP.
type collabEnvelope struct {
	V     int    `json:"v"`     // envelope version (currently 1)
	ID    string `json:"id"`    // the sender's short client id
	Name  string `json:"name"`  // the sender's display name
	Color string `json:"color"` // the sender's caret colour as "#rrggbb"
	SDP   string `json:"sdp"`   // the raw signalling blob the backend produced/consumes
}

// envelopeVersion is the only collabEnvelope.V decodeEnvelope accepts; a different
// (or absent) version is treated as an unrecognised blob.
const envelopeVersion = 1

// encodeEnvelope wraps inner (a raw signalling blob) together with the sender's
// identity into the base64-JSON envelope that "Copy invitation"/"Copy reply" put on
// the clipboard. Marshalling a struct of strings/ints cannot fail, so the error is
// intentionally discarded.
func encodeEnvelope(id, name, colorHex, inner string) string {
	buf, _ := json.Marshal(collabEnvelope{V: envelopeVersion, ID: id, Name: name, Color: colorHex, SDP: inner})
	return base64.StdEncoding.EncodeToString(buf)
}

// decodeEnvelope is the inverse of [encodeEnvelope]: it tolerates surrounding
// whitespace, then returns the sender's {id, name, colour} and the inner signalling
// blob. A blob that is not base64, not this JSON, the wrong version, or missing its
// inner SDP — an old pre-envelope blob, or garbage — yields [errBadEnvelope] so the
// panel can tell the user it is not a valid invitation.
func decodeEnvelope(s string) (id, name, colorHex, inner string, err error) {
	buf, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if derr != nil {
		return "", "", "", "", errBadEnvelope
	}
	var e collabEnvelope
	if jerr := json.Unmarshal(buf, &e); jerr != nil {
		return "", "", "", "", errBadEnvelope
	}
	if e.V != envelopeVersion || e.SDP == "" {
		return "", "", "", "", errBadEnvelope
	}
	return e.ID, e.Name, e.Color, e.SDP, nil
}

// errBadEnvelope is returned when a pasted blob is not a recognisable identity
// envelope (old format, corrupted, or not a signalling blob at all).
var errBadEnvelope = fmt.Errorf("collab: unrecognised invitation blob")

// collabBadEnvelopeMsg is the user-facing panel message for a pasted blob that is
// not a valid envelope — the malformed-paste lane, shown through errMsg.
const collabBadEnvelopeMsg = "That doesn't look like a valid invitation."

// --- the QR "scan to connect" signalling URL ---------------------------------

// signalBaseURL is the canonical playground URL a scan-to-connect QR/link primes.
// It is the tagless default (keeping the URL helpers testable off a browser and
// giving a native build a sensible value); the js layer overrides it at startup
// with the live location (origin + path) via [State.SetCollabBaseURL].
const signalBaseURL = "https://go-tex.github.io/playground/"

// fragInvite / fragAnswer are the two fragment keys a scan-to-connect URL carries:
// an invitation (the host's offer, scanned by the guest's phone → "#invite=…") or a
// reply (the guest's answer, scanned by the host's phone → "#answer=…").
const (
	fragInvite = "invite"
	fragAnswer = "answer"
)

// errBadSignalURL is returned when a fragment is not a well-formed scan-to-connect
// link — an unknown key, a non-payload fragment, not base64url, or not a brotli
// stream (junk or a truncated scan). The one-tap loader treats it as "no link".
var errBadSignalURL = fmt.Errorf("collab: not a scan-to-connect link")

// collabQRTooLargeMsg is shown in place of a QR when the signalling payload will
// not fit under the version cap: a code that dense does not scan off a phone, so the
// panel keeps the copy-paste flow instead of rendering an unscannable square.
const collabQRTooLargeMsg = "Too large to QR — use Copy/paste."

// compressEnvelope brotli-compresses an identity envelope (the base64-JSON blob
// [encodeEnvelope] produces) and base64url-encodes it WITHOUT padding, for a compact
// QR/URL payload. The SDP inside the envelope is highly repetitive, so brotli shrinks
// it several-fold; base64url (no padding) keeps the result safe in a URL fragment. It
// reuses the same andybalholm/brotli the collab document store compresses with, not a
// second compressor. A bytes.Buffer write cannot fail, so those errors are discarded.
func compressEnvelope(envelope string) string {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotli.BestCompression)
	_, _ = w.Write([]byte(envelope))
	_ = w.Close()
	return base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

// decompressEnvelope is the inverse of [compressEnvelope]: base64url-decode then
// brotli-decompress back to the envelope blob. A payload that is not base64url or not
// a brotli stream (junk, a truncated scan) yields [errBadSignalURL], never a panic.
func decompressEnvelope(payload string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", errBadSignalURL
	}
	out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return "", errBadSignalURL
	}
	return string(out), nil
}

// buildSignalURL builds the scan-to-connect URL that primes the app for a peer:
// baseURL + "#invite=<payload>" for the host's invitation, "#answer=<payload>" for
// the guest's reply, where payload is the compressed envelope. baseURL is a parameter
// (not read from location) so the builder is testable off a browser; the js layer
// passes the live origin+path.
func buildSignalURL(baseURL, frag, envelope string) string {
	return baseURL + "#" + frag + "=" + compressEnvelope(envelope)
}

// parseSignalURL extracts the {kind, envelope} a scan-to-connect fragment carries. It
// accepts a bare fragment ("invite=…"), a leading-hash fragment ("#invite=…") or a
// whole URL ("https://…/#answer=…"), tolerates surrounding whitespace, and requires a
// known key and a decompressible payload. Anything else — an unknown key, a
// non-payload fragment, junk or a truncated scan — yields [errBadSignalURL] so the
// caller can ignore it silently. kind is [fragInvite] or [fragAnswer].
func parseSignalURL(s string) (kind, envelope string, err error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[i+1:] // keep only the fragment, whether it came from a bare frag or a full URL
	}
	key, payload, ok := strings.Cut(s, "=")
	if !ok || (key != fragInvite && key != fragAnswer) {
		return "", "", errBadSignalURL
	}
	env, derr := decompressEnvelope(payload)
	if derr != nil {
		return "", "", errBadSignalURL
	}
	return key, env, nil
}

// qrMaxVersion caps the QR version a scan-to-connect code may reach. EC level Low
// keeps the version as low as the payload allows; past this the modules get too dense
// to scan reliably off a phone, so a payload that would need a bigger code falls back
// to copy-paste rather than showing an unscannable QR.
const qrMaxVersion = 22

// qrModuleScale is the pixel size of one QR module in the generated raster, and
// qrQuietModules the white quiet-zone margin (in modules) a scanner needs to lock on.
// The toolkit Image nearest-neighbour-scales the raster again to its on-screen box, so
// these only set the SOURCE resolution.
const (
	qrModuleScale  = 4
	qrQuietModules = 4
)

// buildSignalQR encodes url into a QR raster (RGBA pixels + square dimensions) at EC
// level Low, capped at [qrMaxVersion]. ok is false when the payload will not fit under
// the cap ([qr.ErrTooLong], or any other encode error): the caller then shows the
// copy-paste fallback instead of an unscannably dense code.
func buildSignalQR(url string) (pixels []byte, w, h int, ok bool) {
	m, err := qr.Encode([]byte(url), qr.WithLevel(qr.Low), qr.WithVersionRange(qr.MinVersion, qrMaxVersion))
	if err != nil {
		return nil, 0, 0, false
	}
	pixels, w, h = qrMatrixRGBA(m, qrModuleScale, qrQuietModules)
	return pixels, w, h, true
}

// qrMatrixRGBA rasterises a QR matrix into the RGBA byte buffer the toolkit Image
// widget paints: each module becomes a scale×scale block, dark modules black and light
// modules white, with a quiet-zone margin of quiet modules on every side. It builds
// the buffer directly (no image/png round-trip) so it stays pure and native-testable.
func qrMatrixRGBA(m *qr.Matrix, scale, quiet int) (pixels []byte, w, h int) {
	if scale < 1 {
		scale = 1
	}
	if quiet < 0 {
		quiet = 0
	}
	dim := m.Dimension()
	modules := dim + 2*quiet
	w = modules * scale
	h = w
	pixels = make([]byte, w*h*4)
	for i := range pixels {
		pixels[i] = 0xFF // start all-white (QR light), then paint the dark modules
	}
	q := m.QuietZone() // Module() coordinates include the matrix's own quiet zone
	for my := 0; my < dim; my++ {
		for mx := 0; mx < dim; mx++ {
			if !m.Module(mx+q, my+q) {
				continue
			}
			x0 := (mx + quiet) * scale
			y0 := (my + quiet) * scale
			for dy := 0; dy < scale; dy++ {
				base := ((y0+dy)*w + x0) * 4
				for dx := 0; dx < scale; dx++ {
					o := base + dx*4
					pixels[o], pixels[o+1], pixels[o+2] = 0x00, 0x00, 0x00 // black; alpha already 0xFF
				}
			}
		}
	}
	return pixels, w, h
}

// buildQR recomputes the scan-to-connect QR for the current handshake step: the
// host's invitation ([fragInvite]) in phaseHostWait, the guest's reply ([fragAnswer])
// in phaseGuestWait. It runs ONCE per step (from the CollabHost/CollabJoin completion
// that produced blob), never per frame, and records either a scannable raster
// (qrShown) or the too-large fallback flag (qrTooLarge). An empty blob clears both.
func (v *collabView) buildQR(frag, blob string) {
	v.clearQR()
	if blob == "" {
		return
	}
	url := buildSignalURL(v.baseURL, frag, blob)
	if px, w, h, ok := buildSignalQR(url); ok {
		v.qrPixels, v.qrW, v.qrH, v.qrShown = px, w, h, true
	} else {
		v.qrTooLarge = true
	}
}

// clearQR forgets any generated scan-to-connect QR (on teardown / phase reset).
func (v *collabView) clearQR() {
	v.qrPixels, v.qrW, v.qrH, v.qrShown, v.qrTooLarge = nil, 0, 0, false, false
}

// collabPeerLabel formats a peer's identity for the panel: "<name> #<id>", with an
// "(anonymous)" fallback for a blank name and the bare name when no id is carried.
func collabPeerLabel(name, id string) string {
	if name == "" {
		name = "(anonymous)"
	}
	if id == "" {
		return name
	}
	return name + " #" + id
}

// setPeer records the decoded peer identity so the panel can name who it is
// connecting to. A blank/malformed colour falls back to the zero swatch.
func (v *collabView) setPeer(id, name, colorHex string) {
	v.peerID, v.peerName = id, name
	c, ok := parseHex(colorHex)
	if !ok {
		c = toolkit.RGBA{}
	}
	v.peerColor = c
	v.peerKnown = true
}

// clearPeer forgets the decoded peer identity on teardown.
func (v *collabView) clearPeer() {
	v.peerID, v.peerName, v.peerColor, v.peerKnown = "", "", toolkit.RGBA{}, false
}

// pastedPeer decodes the current paste-field text if it is a valid envelope,
// returning the sender's id/name/colour so the panel can preview WHO an
// invitation/reply is from the instant it is pasted, before Connect. ok is false
// for an empty field or a non-envelope blob (no preview line, no error tone).
func (v *collabView) pastedPeer() (id, name string, color toolkit.RGBA, ok bool) {
	pid, pname, colorHex, _, derr := decodeEnvelope(v.pasteText.Get())
	if derr != nil {
		return "", "", toolkit.RGBA{}, false
	}
	c, _ := parseHex(colorHex)
	return pid, pname, c, true
}

// connectingLine is the peer-named "Connecting…" acknowledgement
// (Connecting to <name> #<id>…). It is used only once the peer's envelope has been
// decoded; the generic pre-peer fallback is [collabConnectingMsg], chosen by
// addConnectingRow.
func (v *collabView) connectingLine() string {
	return "Connecting to " + collabPeerLabel(v.peerName, v.peerID) + "…"
}

// refresh repaints if a host hook is installed.
func (v *collabView) refresh() {
	v.s.dirty = true
	if v.repaint != nil {
		v.repaint()
	}
}

// onBackendChange advances the phase when a live connection appears, fails or
// drops, and repaints. It is the backend's "something moved" signal (connect,
// connection failure, peer join or leave, a remote edit).
func (v *collabView) onBackendChange() {
	switch {
	case v.backend.Connected():
		if v.phase != phaseConnected {
			v.phase = phaseConnected
			v.busy = false
			v.connecting = false
			v.errMsg = ""
		}
	case v.backend.ConnFailed():
		// ICE found no candidate pair (or the channel never opened): the wait would
		// otherwise be silent forever. Surface the failure with the TURN guidance.
		if v.phase != phaseFailed {
			v.connectionFailed()
		}
	case v.phase == phaseConnected:
		// The peer left or the channel dropped; fall back to idle.
		v.phase = phaseIdle
		v.offer, v.answer = "", ""
		v.connecting = false
		v.errMsg = "the peer disconnected"
	}
	v.refresh()
}

// connectionFailed moves the panel into [phaseFailed]: it clears the in-flight
// busy flag and the stale signalling blobs and shows [collabFailedMsg] with a
// "Try again" reset back to idle. The browser backend routes an ICE failure here
// through onBackendChange; a native test drives it directly.
func (v *collabView) connectionFailed() {
	v.busy = false
	v.connecting = false
	v.phase = phaseFailed
	v.offer, v.answer = "", ""
	// The failed panel renders collabFailedMsg on its own lines; keep errMsg clear
	// so the guidance is not also repeated as a one-line ⚠ overflow.
	v.errMsg = ""
	v.refresh()
}

// --- the copy-paste handshake, driven by the panel and the headless proof ----

// CollabHost starts a hosting session: it seeds the shared document with the
// current editor source, binds the editor to it and makes the offer blob to hand
// to the peer. The raw SDP the backend produces is wrapped in an identity envelope
// (see [encodeEnvelope]) so the peer's panel can name who invited it; that envelope
// is what v.offer holds and "Copy invitation" puts on the clipboard. done
// (optional) receives the envelope or the error; the panel passes nil and reads
// v.offer, the headless proof passes a chained handler that relays it to the peer.
func (s *State) CollabHost(done func(offer string, err error)) {
	v := s.collab
	v.busy, v.errMsg = true, ""
	v.backend.Host(v.name, v.color, func(offer string, err error) {
		v.busy = false
		if err != nil {
			v.errMsg = err.Error()
		} else {
			v.offer, v.phase = encodeEnvelope(v.id, v.name, hexColor(v.color), offer), phaseHostWait
			// Compute the scan-to-connect QR once, now the invitation exists — not per
			// frame. A guest scans it to join with one tap (see [State.CollabApplySignalFragment]).
			v.buildQR(fragInvite, v.offer)
		}
		if done != nil {
			done(v.offer, err)
		}
		v.refresh()
	})
}

// CollabJoin joins a hosting peer from its pasted invitation: it decodes the host's
// identity envelope (capturing WHO invited it, for the panel), binds the editor to
// the shared document and makes the answer blob to hand back. The raw SDP inside the
// envelope is what the backend receives; the answer it produces is itself wrapped in
// this client's envelope before it lands in v.answer. The live connection follows
// once the host accepts (reported via the change hook). An empty field or a blob
// that is not a valid envelope (an old-format or garbage paste) is refused before
// the backend is touched, with a clear message in the errMsg lane.
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
	peerID, peerName, peerColor, inner, err := decodeEnvelope(offer)
	if err != nil {
		v.errMsg, v.connecting = collabBadEnvelopeMsg, false
		v.refresh()
		if done != nil {
			done("", err)
		}
		return
	}
	v.setPeer(peerID, peerName, peerColor)
	v.busy, v.errMsg, v.connecting = true, "", true
	v.backend.Join(v.name, v.color, inner, func(answer string, err error) {
		v.busy = false
		if err != nil {
			v.errMsg, v.connecting = err.Error(), false
		} else {
			v.answer, v.phase = encodeEnvelope(v.id, v.name, hexColor(v.color), answer), phaseGuestWait
			// Compute the reply QR once, now the answer exists — the host scans it to
			// complete the handshake (see [State.CollabApplySignalFragment]).
			v.buildQR(fragAnswer, v.answer)
		}
		if done != nil {
			done(v.answer, err)
		}
		v.refresh()
	})
}

// CollabAcceptAnswer completes the host's handshake with the guest's pasted reply:
// it decodes the guest's identity envelope (capturing WHO is joining, for the
// panel), and hands the raw SDP inside it to the backend. The live connection
// follows (reported via the change hook). An empty field or a blob that is not a
// valid envelope is refused before the backend is touched, with a clear message in
// the errMsg lane.
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
	peerID, peerName, peerColor, inner, err := decodeEnvelope(answer)
	if err != nil {
		v.errMsg, v.connecting = collabBadEnvelopeMsg, false
		v.refresh()
		if done != nil {
			done(err)
		}
		return
	}
	v.setPeer(peerID, peerName, peerColor)
	v.busy, v.errMsg, v.connecting = true, "", true
	v.backend.AcceptAnswer(inner, func(err error) {
		v.busy = false
		if err != nil {
			v.errMsg, v.connecting = err.Error(), false
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
	v.busy, v.connecting = false, false
	v.pasteText.Set("")
	v.pasteFocused = false
	v.clearPeer()
	v.clearQR()
	v.refresh()
}

// errEmptyBlob is returned when a paste step got nothing to work with.
var errEmptyBlob = fmt.Errorf("collab: nothing to paste")

// SetCollabBaseURL sets the origin+path a scan-to-connect QR/URL primes. The js layer
// passes the live [location] at startup so a scanned code opens the exact deployment
// it was generated on; an empty string is ignored, keeping the built-in default
// ([signalBaseURL]).
func (s *State) SetCollabBaseURL(base string) {
	if base != "" {
		s.collab.baseURL = base
	}
}

// CollabApplySignalFragment consumes a scan-to-connect URL fragment on load: an
// "#invite=…" opens the Collaborate panel and joins the host as the decoded invitation
// (exactly as if the guest had pasted it), an "#answer=…" hands the reply to the
// waiting host. The decoded envelope is dropped into the visible paste field first, so
// the flow is identical to a manual paste, then driven through [State.CollabJoin] /
// [State.CollabAcceptAnswer]. A malformed or non-signalling fragment is ignored (ok
// false, panel untouched) so a junk hash never disrupts a normal load. kind is
// [fragInvite]/[fragAnswer] on success.
func (s *State) CollabApplySignalFragment(fragment string) (kind string, ok bool) {
	k, envelope, err := parseSignalURL(fragment)
	if err != nil {
		return "", false
	}
	v := s.collab
	v.open = true
	v.pasteText.Set(envelope)
	switch k {
	case fragInvite:
		s.CollabJoin(envelope, nil)
	case fragAnswer:
		s.CollabAcceptAnswer(envelope, nil)
	}
	return k, true
}

// --- introspection for the headless proof and the DOM host -------------------

// CollabConnected reports whether a live peer session is up.
func (s *State) CollabConnected() bool { return s.collab.backend.Connected() }

// CollabPeerCount is how many remote participants are in the document.
func (s *State) CollabPeerCount() int { return s.collab.backend.PeerCount() }

// CollabPhase is the session phase as an int (0 idle … 4 connected, 5 failed),
// for a headless assertion.
func (s *State) CollabPhase() int { return int(s.collab.phase) }

// CollabConnecting reports whether a Connect step was accepted and the panel is
// showing the "Connecting…" acknowledgement while the peer channel opens — the
// signal that a pasted blob was taken and a connection is being attempted.
func (s *State) CollabConnecting() bool { return s.collab.connecting }

// CollabPasteText is the current text of the visible signalling-blob paste field
// (the host's invitation on the guest side, the peer's reply on the host side),
// for the DOM host and headless introspection.
func (s *State) CollabPasteText() string { return s.collab.pasteText.Get() }

// CollabFailureMessage is the panel's failure guidance when the connection could
// not be established ([phaseFailed]) — the TURN-server hint the user needs — and
// the empty string in every other phase. It is what the failure-path test and a
// gotex* debug hook read to prove the message is surfaced rather than the panel
// hanging silently.
func (s *State) CollabFailureMessage() string {
	if s.collab.phase == phaseFailed {
		return collabFailedMsg
	}
	return ""
}

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

// CollabLocalID is this participant's stable short client id (shown in the identity
// row next to the name), for the DOM host and headless introspection.
func (s *State) CollabLocalID() string { return s.collab.id }

// CollabPeerID / CollabPeerName / CollabPeerColorHex expose the identity decoded
// from the peer's pasted envelope once a valid invitation/reply has been consumed —
// who this client is connecting to. They are empty before a peer is known.
func (s *State) CollabPeerID() string   { return s.collab.peerID }
func (s *State) CollabPeerName() string { return s.collab.peerName }
func (s *State) CollabPeerColorHex() string {
	if !s.collab.peerKnown {
		return ""
	}
	return hexColor(s.collab.peerColor)
}

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
	case roleConnectOffer:
		return "connectOffer"
	case roleConnectAnswer:
		return "connectAnswer"
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
	if v.pasteRect.W > 0 {
		out["paste"] = rect(v.pasteRect)
	}
	if v.qrRect.W > 0 {
		out["qr"] = rect(v.qrRect)
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
	v.pasteRect = toolkit.Rect{}
	v.qrRect = toolkit.Rect{}
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

	// Identity row: "You:" + name field + stable id + colour swatch + Shuffle. The
	// id (e.g. #A3F9) sits between the name and the swatch, fixed for the page load,
	// so it reads "You: <name> #A3F9" — Shuffle re-rolls the name/colour, never the id.
	shuffleW := toolkit.Scaled(72)
	swatch := toolkit.Scaled(18)
	youW := toolkit.Scaled(34)
	idW := toolkit.Scaled(52)
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: youW, H: bh}, text: "You:"})
	nameX := innerX + youW
	nameW := innerW - youW - idW - gap - swatch - gap - shuffleW - 2*gap
	v.nameRect = toolkit.Rect{X: nameX, Y: cur, W: nameW, H: bh}
	idX := nameX + nameW + gap
	v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: idX, Y: cur, W: idW, H: bh}, text: "#" + v.id})
	v.swatches = append(v.swatches, collabSwatch{rect: toolkit.Rect{X: idX + idW + gap, Y: cur + (bh-swatch)/2, W: swatch, H: swatch}, color: v.color})
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
	// addPasteField reserves the full-width row for the visible signalling-blob
	// Entry the user pastes into. Only one is shown at a time (guest or host), so it
	// reuses the single v.pasteRect / v.pasteEntry.
	addPasteField := func() {
		v.pasteRect = toolkit.Rect{X: innerX, Y: cur, W: innerW, H: bh}
		cur += bh + gap
	}
	// addPeerRow shows one line naming a peer, with the peer's caret colour as a small
	// chip to its left — used to preview who a pasted invitation/reply is from and to
	// name who a Connect is reaching. It mirrors the connected-phase peer rows.
	addPeerRow := func(text string, c toolkit.RGBA) {
		chip := toolkit.Scaled(12)
		v.swatches = append(v.swatches, collabSwatch{rect: toolkit.Rect{X: innerX, Y: cur + (line-chip)/2, W: chip, H: chip}, color: c})
		v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX + toolkit.Scaled(18), Y: cur, W: innerW - toolkit.Scaled(18), H: line}, text: text})
		cur += line
	}
	// addQR reserves a captioned square for the scan-to-connect QR when one was
	// generated (qrShown): a phone's camera opens the encoded URL and joins with one
	// tap. When the payload was too large for a scannable code (qrTooLarge) it shows a
	// short fallback line instead, and the copy-paste flow stands unchanged. The QR
	// itself is a persistent [toolkit.Image] drawn from v.qrRect in draw().
	addQR := func(caption string) {
		switch {
		case v.qrShown:
			addLabel(caption)
			side := toolkit.Scaled(150)
			if side > innerW {
				side = innerW
			}
			v.qrRect = toolkit.Rect{X: innerX + (innerW-side)/2, Y: cur, W: side, H: side}
			cur += side + gap
		case v.qrTooLarge:
			addLabel(collabQRTooLargeMsg)
		}
	}
	// addConnectingRow shows the "Connecting…" acknowledgement, naming the decoded
	// peer (with its colour chip) once one is known and falling back to the generic
	// line otherwise.
	addConnectingRow := func() {
		if v.peerKnown {
			addPeerRow(v.connectingLine(), v.peerColor)
			return
		}
		addLabel(collabConnectingMsg)
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
		addQR("Scan to join from a phone:")
		if v.connecting {
			// The reply was accepted: acknowledge it and show the connection is being
			// attempted (naming the peer), instead of leaving the paste prompt untouched.
			addConnectingRow()
			addButtons(collabItem{role: roleCancel, label: "Cancel"})
		} else {
			addLabel("2. Paste their reply here to connect:")
			addPasteField()
			// The instant a valid reply is in the field, name who it is from.
			if id, name, c, ok := v.pastedPeer(); ok {
				addPeerRow("Reply from "+collabPeerLabel(name, id), c)
			}
			addButtons(
				collabItem{role: roleConnectAnswer, label: "Connect"},
				collabItem{role: rolePasteAnswer, label: "Paste from clipboard"},
			)
			addButtons(collabItem{role: roleCancel, label: "Cancel"})
		}
	case v.phase == phaseGuestOffer:
		addLabel("Paste your host's invitation:")
		addPasteField()
		// The instant a valid invitation is in the field, name who it is from.
		if id, name, c, ok := v.pastedPeer(); ok {
			addPeerRow("Invitation from "+collabPeerLabel(name, id), c)
		}
		addButtons(
			collabItem{role: roleConnectOffer, label: "Connect"},
			collabItem{role: rolePasteOffer, label: "Paste from clipboard"},
		)
		addButtons(collabItem{role: roleCancel, label: "Cancel"})
	case v.phase == phaseGuestWait:
		addLabel("Send this reply back to the host:")
		addButtons(collabItem{role: roleCopyAnswer, label: "Copy reply"})
		addQR("Scan to reply from a phone:")
		addConnectingRow()
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
	case v.phase == phaseFailed:
		// No candidate path to the peer. Show the guidance (wrapped, since the
		// toolkit Label does not) in the warning tone, and offer a reset to idle.
		warn := toolkit.RGBA{R: 0xE5, G: 0x39, B: 0x35, A: 0xFF}
		for _, ln := range wrapWords(collabFailedMsg, failedWrap) {
			v.labels = append(v.labels, collabLabel{rect: toolkit.Rect{X: innerX, Y: cur, W: innerW, H: line}, text: ln, ink: warn})
			cur += line
		}
		addButtons(collabItem{role: roleCancel, label: "Try again"})
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
	v.pasteEntry = toolkit.NewEntry("")
	// The scan-to-connect QR is a real Image widget: aspect-preserved (ScaleFit) so a
	// square code stays square in its box, with an accessible name for a reader.
	v.qrImage = &toolkit.Image{Scale: toolkit.ScaleFit, Alt: "Scan-to-connect QR code"}
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
	// Signalling-blob paste field: a real Entry the user pastes the invitation /
	// reply into (⌘V shows it — proof it was taken); the Connect button reads it.
	if v.pasteRect.W > 0 {
		v.pasteEntry.SetBounds(v.pasteRect)
		v.pasteEntry.SetText(v.pasteText.Get())
		v.pasteEntry.SetFocused(v.pasteFocused)
		v.pasteEntry.Draw(p, theme)
	}
	// Scan-to-connect QR: the raster was computed once when the offer/answer was
	// produced (buildQR); here it is only blitted through the persistent Image widget.
	if v.qrRect.W > 0 && v.qrShown {
		v.qrImage.Pixels, v.qrImage.W, v.qrImage.H = v.qrPixels, v.qrW, v.qrH
		v.qrImage.SetBounds(v.qrRect)
		v.qrImage.Draw(p, theme)
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
	if v.pasteRect.W > 0 {
		v.pasteEntry.SetBounds(v.pasteRect)
		if v.pasteEntry.HitTest(x, y) {
			v.blurICE()
			v.nameFocused = false
			v.pasteFocused = true
			v.refresh()
			return true
		}
	}
	if v.iceRect.W > 0 {
		v.iceEntry.SetBounds(v.iceRect)
		if v.iceEntry.HitTest(x, y) {
			v.nameFocused, v.pasteFocused = false, false
			v.iceFocused = true
			v.refresh()
			return true
		}
	}
	if v.nameRect.W > 0 {
		v.nameEntry.SetBounds(v.nameRect)
		if v.nameEntry.HitTest(x, y) {
			v.blurICE() // commit the ICE field if focus is leaving it
			v.pasteFocused = false
			v.nameFocused = true
			v.refresh()
			return true
		}
	}
	v.blurICE()
	v.nameFocused, v.pasteFocused = false, false
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
		// The host will need a field to paste the peer's reply into (phaseHostWait);
		// start it empty and focused so a ⌘V lands there immediately.
		v.pasteText.Set("")
		v.focusPaste()
		v.s.CollabHost(nil)
	case roleJoin:
		v.phase = phaseGuestOffer
		v.pasteText.Set("")
		v.focusPaste()
	case roleConnectOffer:
		// Primary guest action: join using the visible field, NOT the clipboard.
		v.pasteFocused = false
		v.s.CollabJoin(v.pasteText.Get(), nil)
	case roleConnectAnswer:
		// Primary host action: accept the reply from the visible field.
		v.pasteFocused = false
		v.s.CollabAcceptAnswer(v.pasteText.Get(), nil)
	case rolePasteOffer, rolePasteAnswer:
		// Convenience: fill the visible field from the OS clipboard (the primary
		// Connect button then reads the field). Errors surface via readClipboard.
		v.fillPasteFromClipboard()
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
	if v.pasteFocused {
		if r := []rune(code); len(r) == 1 {
			v.pasteText.Set(v.pasteText.Get() + code)
			v.refresh()
		}
		return true
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
		case v.pasteFocused:
			v.pasteFocused = false
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
	if v.pasteFocused {
		switch code {
		case "Backspace":
			if r := []rune(v.pasteText.Get()); len(r) > 0 {
				v.pasteText.Set(string(r[:len(r)-1]))
			}
		case "Enter", "Return":
			v.pasteFocused = false
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

// focusPaste gives the visible paste field keyboard focus and takes it away from
// the name / ICE fields, so a following ⌘V or keystroke lands in the paste field.
func (v *collabView) focusPaste() {
	v.pasteFocused = true
	v.nameFocused = false
	v.iceFocused = false
}

// fillPasteFromClipboard is the "Paste from clipboard" convenience: it reads the
// OS clipboard and drops the text into the visible field (focusing it), rather
// than connecting straight from the clipboard. If the browser refuses the read
// (readClipboard's rejection path), the field is left untouched and the panel
// shows [collabClipReadErrMsg] pointing the user at a manual ⌘V.
func (v *collabView) fillPasteFromClipboard() {
	v.readClipboard(func(text string) {
		v.pasteText.Set(strings.TrimSpace(text))
		v.focusPaste()
		v.refresh()
	})
}

// handlePaste routes an OS paste (⌘V/Ctrl+V, delivered by the host) into whichever
// panel field holds focus, so the pasted blob is visible immediately — the proof
// it was taken. Returns whether it consumed the paste.
func (v *collabView) handlePaste(text string) bool {
	if !v.open {
		return false
	}
	switch {
	case v.pasteFocused:
		v.pasteText.Set(v.pasteText.Get() + text)
	case v.iceFocused:
		v.iceText.Set(v.iceText.Get() + text)
	case v.nameFocused:
		v.name += text
	default:
		return false
	}
	v.refresh()
	return true
}

// readClipboard reaches the OS clipboard through the host hook the wasm driver
// installs. onText receives the text on success; a rejected read (Firefox blocks
// readText for web content, Chrome rejects a background/unfocused tab or a missing
// permission grant) routes to [clipReadFailed] so the blockage is never silent. A
// native build has no clipboard, so this is a no-op there.
func (v *collabView) readClipboard(onText func(string)) {
	if v.clipRead == nil {
		return
	}
	v.clipRead(onText, func(error) { v.clipReadFailed() })
}

// clipReadFailed surfaces a clear message when the browser refused a clipboard
// read, steering the user to the reliable manual ⌘V into the visible field.
func (v *collabView) clipReadFailed() {
	v.errMsg = collabClipReadErrMsg
	v.refresh()
}

func (v *collabView) writeClipboard(text string) {
	if v.clipWrite != nil {
		v.clipWrite(text)
	}
}

// clipRead / clipWrite are the host clipboard hooks (installed by the wasm
// driver). Kept as fields so the panel stays free of syscall/js. clipRead reports
// the text through onText or a refusal through onErr (a rejected readText promise).
type clipboardHooks struct {
	clipRead  func(onText func(string), onErr func(error))
	clipWrite func(text string)
}

// failedWrap is the per-line character budget wrapWords uses to fit
// [collabFailedMsg] inside the panel's inner width with the 5x7 bitmap font. It
// is comfortably under what the ICE hint line already occupies, so a wrapped
// failure line never overruns the card.
const failedWrap = 50

// wrapWords greedily wraps s into lines of at most max runes, breaking only at
// single spaces so the join of the returned lines with a space is s again. A word
// longer than max still goes on its own line unbroken (a URL is not chopped). It
// lets a long message be shown across several toolkit Labels, which do not wrap
// on their own.
func wrapWords(s string, max int) []string {
	var lines []string
	var line string
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= max:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
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
func (nopBackend) ConnFailed() bool                        { return false }
func (nopBackend) PeerCount() int                          { return 0 }
func (b nopBackend) SetOnChange(func())                    { _ = b } // nothing ever changes

// errNoBrowser explains why the no-op backend never connects.
var errNoBrowser = fmt.Errorf("collab: live collaboration needs a browser")
