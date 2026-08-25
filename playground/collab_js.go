// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall/js"
	"time"

	"github.com/go-crdt/collab"
	"github.com/go-crdt/crdt"
	"github.com/go-widgets/toolkit"
)

// connectTimeout bounds the wait for the WebRTC data channel to open after the
// handshake blobs are swapped. When ICE finds no candidate pair — the common
// full-tunnel-VPN / symmetric-NAT case, where STUN-only cannot traverse — the
// channel never opens and never errors, so the wait would otherwise hang
// forever. On expiry the backend reports a connection failure so the panel shows
// the TURN-server guidance instead of a silent "waiting". It is generous enough
// that slow-but-working ICE (relayed candidates, a distant peer) still connects.
// It is a var, not a const, only so the headless failure proof
// (cmd/collab-failtest) can shorten it via [SetCollabConnectTimeout]; production
// never changes it.
var connectTimeout = 25 * time.Second

// SetCollabConnectTimeout overrides the data-channel open-wait deadline. It is a
// test/debug seam for the headless failure proof, which shortens it so an
// unreachable-peer failure surfaces in seconds instead of the production default;
// ordinary use never calls it.
func SetCollabConnectTimeout(d time.Duration) { connectTimeout = d }

// This is the browser half of the collaborative-editing feature — the
// syscall/js glue behind the [collabBackend] seam in collab.go. It opens the
// real WebRTC connection, runs the collab Server/Client and binds the shared
// CRDT document to the editor with toolkit.CollabText. It is excluded from the
// native `go test` coverage gate; its proof is the headless browser test in
// cmd/collab-browsertest, driven by collab_browser_test.go.
//
// # Who runs what
//
// The two peers are asymmetric, because collab needs one document holder:
//
//   - The HOST runs a [collab.Server] with an in-memory store and, so its OWN
//     editor sees the shared document, joins that server as an ordinary client
//     over an in-process [collab.Pipe] — two Go channels, no carrier — rather than
//     a loopback WebRTC connection to itself. This is the honest way for a page to
//     be a client of a server it also runs, with no second RTCPeerConnection and
//     no STUN gathering on the host's critical path. The host seeds the document
//     with the current editor source, then serves the remote guest over the real
//     WebRTC data channel.
//   - The GUEST is a plain [collab.Client] joined over the real data channel; its
//     editor adopts the shared document.
//
// Both editors are bound with [toolkit.CollabText], so local edits flow into the
// CRDT and remote edits + carets flow back — identical wiring on both sides.
//
// # Threading
//
// WebAssembly runs Go on the page's single thread and holds it until it blocks,
// so JavaScript and Go never run at once and goroutines interleave only at
// blocking points. The setup goroutines here, the CollabText Updates() drain
// loop and the UI event callbacks therefore never touch the editor at the same
// instant, which is the one-goroutine contract CollabText asks for.

// iceStorageKey is the localStorage entry the Collaborate ICE (STUN/TURN)
// configuration is persisted under, so a user's chosen servers survive a reload.
const iceStorageKey = "gotex-collab-ice"

// EnableCollab installs the real WebRTC backend and the OS-clipboard hooks on
// the playground's Collaborate affordance, and restores any persisted ICE
// (STUN/TURN) configuration. The wasm driver calls it once, after building the
// State, passing its repaint function.
func (s *State) EnableCollab(repaint func()) {
	b := &webrtcBackend{s: s, repaint: repaint}
	s.collab.attach(b, repaint)
	s.collab.clipRead = readClipboard
	s.collab.clipWrite = writeClipboard
	// Persist the ICE field's committed value so a user's chosen STUN/TURN servers
	// survive a reload; it lands under the same key EnableCollab restores from below
	// and the gotexSetICEServers hook writes.
	s.collab.icePersist = func(csv string) {
		if ls := js.Global().Get("localStorage"); ls.Truthy() {
			ls.Call("setItem", iceStorageKey, csv)
		}
	}

	// A persisted ICE configuration overrides the built-in public-STUN default.
	if ls := js.Global().Get("localStorage"); ls.Truthy() {
		if v := ls.Call("getItem", iceStorageKey); v.Type() == js.TypeString && v.String() != "" {
			s.SetCollabICEServers(v.String())
		}
	}
}

// peerConfig builds the collab peer configuration from the view's ICE servers.
// Credential-free entries (STUN, the default) go in ICEServers; entries with a
// username or credential (a TURN relay) go in ICEServersAuth, which carries the
// long-term credentials the plain URL list cannot express.
func (b *webrtcBackend) peerConfig() collab.PeerConfig {
	var cfg collab.PeerConfig
	for _, sv := range b.s.CollabICEConfig() {
		if sv.Username == "" && sv.Credential == "" {
			cfg.ICEServers = append(cfg.ICEServers, sv.URL)
			continue
		}
		cfg.ICEServersAuth = append(cfg.ICEServersAuth, collab.ICEServerAuth{
			URLs:       []string{sv.URL},
			Username:   sv.Username,
			Credential: sv.Credential,
		})
	}
	return cfg
}

// webrtcBackend is the live [collabBackend]: one collaborative session, host or
// guest.
type webrtcBackend struct {
	s        *State
	repaint  func()
	onChange func()

	ctx    context.Context
	cancel context.CancelFunc

	server     *collab.Server
	remotePeer *collab.Peer        // the WebRTC peer to the remote participant
	client     *collab.Client      // the local editor's client (host: its in-process Pipe client)
	ct         *toolkit.CollabText // the binding of s.editor to the shared text part

	connected bool
	failed    bool // the connection attempt failed with no path to the peer
}

// SetOnChange installs the view's change hook; the backend fires it on connect,
// disconnect, a peer change or a remote edit.
func (b *webrtcBackend) SetOnChange(f func()) { b.onChange = f }

// notify wakes the view so it advances the phase and repaints.
func (b *webrtcBackend) notify() {
	if b.onChange != nil {
		b.onChange()
	} else if b.repaint != nil {
		b.repaint()
	}
}

// session (re)creates the per-session context and clears the previous attempt's
// terminal state, so a "Try again" after a failure starts clean.
func (b *webrtcBackend) session() {
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.connected, b.failed = false, false
}

// openChannel waits for the peer's data channel to open, but no longer than
// [connectTimeout]: an unreachable peer (no ICE candidate pair) opens no channel
// and fires no error, so an unbounded wait would hang the panel silently. A
// deadline hit is reported to the caller as [context.DeadlineExceeded], which the
// handshake paths turn into a [webrtcBackend.fail].
func (b *webrtcBackend) openChannel(peer *collab.Peer) (js.Value, error) {
	ctx, cancel := context.WithTimeout(b.ctx, connectTimeout)
	defer cancel()
	return peer.DataChannel(ctx)
}

// Host starts a hosting session: it runs a server, joins it over an in-process
// [collab.Pipe] so the host's own editor is a client, seeds the document with the
// current source, and makes the offer to hand to the guest.
func (b *webrtcBackend) Host(name string, color toolkit.RGBA, done func(string, error)) {
	go func() {
		b.session()
		src := b.s.Source()

		b.server = collab.NewServer(collab.Config{Store: collab.NewMemoryStore()})
		// The host's own editor is an ordinary participant of the document it
		// serves, so it joins over an in-process [collab.Pipe] — a Transport backed
		// by two Go channels — rather than a loopback WebRTC connection to itself.
		// Nothing crosses a wire, so there is no second RTCPeerConnection to dial,
		// answer and encrypt and no STUN gathering on the host's critical path (the
		// remote guest still meets over the real data channel below).
		client, server := collab.Pipe()
		go func() { _ = b.server.ServePipe(b.ctx, server) }()

		hostClient, err := collab.Join(b.ctx, client,
			collab.ClientConfig{Document: docName, Site: randSite()})
		if err != nil {
			done("", err)
			return
		}
		if err := b.bind(hostClient, name, color); err != nil {
			done("", err)
			return
		}
		// NewCollabText emptied the editor to the (empty) shared text; refill it,
		// which flows the seed into the CRDT as this participant's first edit.
		b.s.editor.SetText(src)

		peer, err := collab.NewPeer(b.peerConfig())
		if err != nil {
			done("", err)
			return
		}
		b.remotePeer = peer
		offer, err := peer.Offer(b.ctx, "collab")
		if err != nil {
			done("", err)
			return
		}
		done(offer, nil)
	}()
}

// AcceptAnswer completes the host handshake with the guest's answer, then opens
// the channel and serves the guest over it.
func (b *webrtcBackend) AcceptAnswer(answer string, done func(error)) {
	go func() {
		if b.remotePeer == nil {
			done(errNoBrowser)
			return
		}
		if err := b.remotePeer.AcceptAnswer(answer); err != nil {
			done(err)
			return
		}
		done(nil)
		ch, err := b.openChannel(b.remotePeer)
		if err != nil {
			b.fail(err)
			return
		}
		b.setConnected(true)
		_ = b.server.ServeDataChannel(b.ctx, ch) // blocks until the guest leaves
		b.setConnected(false)
	}()
}

// Join joins a hosting peer from its offer, makes the answer, and — once the
// host accepts and the channel opens — joins the shared document.
func (b *webrtcBackend) Join(name string, color toolkit.RGBA, offer string, done func(string, error)) {
	go func() {
		b.session()
		peer, err := collab.NewPeer(b.peerConfig())
		if err != nil {
			done("", err)
			return
		}
		b.remotePeer = peer
		answer, err := peer.Answer(b.ctx, offer)
		if err != nil {
			done("", err)
			return
		}
		done(answer, nil)

		ch, err := b.openChannel(peer) // waits for the host to accept, bounded
		if err != nil {
			b.fail(err)
			return
		}
		client, err := collab.Join(b.ctx, collab.DataChannel(ch),
			collab.ClientConfig{Document: docName, Site: randSite()})
		if err != nil {
			b.fail(err)
			return
		}
		if err := b.bind(client, name, color); err != nil {
			b.fail(err)
			return
		}
		b.setConnected(true)
		go func() { <-client.Done(); b.setConnected(false) }()
	}()
}

// LocalConnect runs the zero-config "in this browser" mode: it elects host or
// client on the fixed [collabLocalRoom] BroadcastChannel — the shared bus every
// tab of this origin is already on — and binds the editor either way. Nothing is
// relayed and no ICE is gathered, so this connects even where no WebRTC path can
// form (a full-tunnel VPN, a strict NAT). The live session is reported through the
// change hook exactly as the WebRTC path is; done receives only a setup error.
//
// The host and client are asymmetric for the same reason the WebRTC path is: one
// tab must hold the document. The elected host runs a [collab.Server], joins it
// over an in-process [collab.Pipe] so its OWN editor is a participant, seeds the
// document with the current source, then serves the other tabs over the bus with
// [collab.Server.ServeBroadcastChannel]. A client joins with
// [collab.JoinBroadcastChannel]. This reuses [webrtcBackend.bind] and the same
// Server+Pipe+editor wiring as [webrtcBackend.Host].
func (b *webrtcBackend) LocalConnect(name string, color toolkit.RGBA, done func(error)) {
	go func() {
		b.session()
		// OpenBroadcastSession elects AND goes live on one bus: an elected host is
		// already answering hellos before we build its Server below, so a second tab
		// clicking while this one is still wiring up is welcomed and joins rather than
		// electing itself a rival host. That closed serve-gap is the fix for the
		// same-browser "both Connected but no sync" split-brain — see
		// [collab.OpenBroadcastSession]. The old HostOrJoin-then-reopen-to-serve shape
		// left exactly that window open.
		bs, err := collab.OpenBroadcastSession(b.ctx, collabLocalRoom, collab.DefaultElectionWindow)
		if err != nil {
			b.reportLocalErr(done, err)
			return
		}
		if bs.Role() == collab.RoleHost {
			b.localHost(bs, name, color, done)
		} else {
			b.localJoin(bs, name, color, done)
		}
	}()
}

// localHost holds the document for the same-browser room: it runs a Server, joins
// it over an in-process Pipe so the host's own editor is a participant, seeds the
// shared text with the current source, then attaches that Server to the answerer
// bs has been running since the election and serves the other tabs over the bus
// until the session is torn down (ctx cancelled by Disconnect).
func (b *webrtcBackend) localHost(bs *collab.BroadcastSession, name string, color toolkit.RGBA, done func(error)) {
	src := b.s.Source()
	b.server = collab.NewServer(collab.Config{Store: collab.NewMemoryStore()})
	client, server := collab.Pipe()
	go func() { _ = b.server.ServePipe(b.ctx, server) }()

	hostClient, err := collab.Join(b.ctx, client,
		collab.ClientConfig{Document: docName, Site: randSite()})
	if err != nil {
		bs.Close()
		b.reportLocalErr(done, err)
		return
	}
	if err := b.bind(hostClient, name, color); err != nil {
		bs.Close()
		b.reportLocalErr(done, err)
		return
	}
	// NewCollabText emptied the editor to the (empty) shared text; refill it so the
	// seed flows into the CRDT as this participant's first edit.
	b.s.editor.SetText(src)

	if done != nil {
		done(nil)
	}
	b.setConnected(true)
	// Attach the Server to the already-answering bus and serve; every tab welcomed
	// during setup gets its session now, and any that join later get theirs at once.
	err = bs.Serve(b.server) // blocks until torn down
	b.setConnected(false)
	if errors.Is(err, collab.ErrHostSuperseded) && b.cancel != nil {
		// Another tab with priority took the room — a race the gap-free election
		// makes vanishingly unlikely, kept as a backstop. Abandon this duplicate host
		// so the room keeps exactly one document; cancelling tears the half-built host
		// down and the user can reconnect, which now joins the survivor.
		b.cancel()
	}
}

// localJoin joins the tab that is holding the document for the same-browser room
// over the connection bs already dialled during the election, and binds the
// editor to the shared document.
func (b *webrtcBackend) localJoin(bs *collab.BroadcastSession, name string, color toolkit.RGBA, done func(error)) {
	client, err := collab.Join(b.ctx, bs.Transport(),
		collab.ClientConfig{Document: docName, Site: randSite()})
	if err != nil {
		bs.Close()
		b.reportLocalErr(done, err)
		return
	}
	if err := b.bind(client, name, color); err != nil {
		bs.Close()
		b.reportLocalErr(done, err)
		return
	}
	if done != nil {
		done(nil)
	}
	b.setConnected(true)
	go func() { <-client.Done(); b.setConnected(false) }() // the host tab closed → back to idle
}

// reportLocalErr surfaces a same-browser setup error through done, unless the
// session was already torn down by the user (a Disconnect cancels b.ctx and the
// blocked election/dial returns context.Canceled — not a failure to show).
func (b *webrtcBackend) reportLocalErr(done func(error), err error) {
	if b.ctx != nil && b.ctx.Err() != nil {
		return
	}
	if done != nil {
		done(err)
	}
}

// bind wires the editor to a client's shared text part and starts draining the
// binding's remote updates.
func (b *webrtcBackend) bind(client *collab.Client, name string, color toolkit.RGBA) error {
	ct, err := toolkit.NewCollabText(b.s.editor, client, textName)
	if err != nil {
		return err
	}
	ct.Name, ct.Color = name, color
	b.client, b.ct = client, ct
	go func() {
		for apply := range ct.Updates() {
			apply()    // write the remote edit + rebuild remote carets (UI goroutine)
			b.notify() // repaint; the editor's own edit subscriber schedules a recompile
		}
	}()
	return nil
}

// Disconnect tears the whole session down. It runs on the UI goroutine (a panel
// button), which is where CollabText.Close must be called.
func (b *webrtcBackend) Disconnect() {
	if b.ct != nil {
		_ = b.ct.Close()
		b.ct = nil
	}
	if b.client != nil {
		_ = b.client.Close()
		b.client = nil
	}
	if b.remotePeer != nil {
		_ = b.remotePeer.Close()
		b.remotePeer = nil
	}
	if b.server != nil {
		_ = b.server.Close(context.Background())
		b.server = nil
	}
	if b.cancel != nil {
		b.cancel()
	}
	b.connected, b.failed = false, false
}

// Connected reports whether the remote channel is open.
func (b *webrtcBackend) Connected() bool { return b.connected }

// ConnFailed reports whether the last attempt failed with no path to the peer,
// so the view moves the panel to its failed state with the TURN guidance.
func (b *webrtcBackend) ConnFailed() bool { return b.failed }

// PeerCount is how many OTHER participants share the document (the presence
// registry includes this replica, which is excluded here).
func (b *webrtcBackend) PeerCount() int {
	if b.client == nil {
		return 0
	}
	self := b.client.Site()
	n := 0
	for _, p := range b.client.Peers() {
		if p.Site != self {
			n++
		}
	}
	return n
}

// setConnected records the connection state and wakes the view.
func (b *webrtcBackend) setConnected(v bool) {
	b.connected = v
	b.notify()
}

// fail records a connection failure (no path to the peer: the open-wait timed
// out, or the channel errored) and wakes the view, which moves the panel to its
// failed state with the TURN guidance. A user-initiated teardown cancels b.ctx
// and surfaces here as context.Canceled through the blocked open-wait; that is
// not a failure to show — the panel is already returning to idle — so it is
// ignored.
func (b *webrtcBackend) fail(err error) {
	_ = err // the specific transport error is not shown; the panel gives actionable guidance
	b.connected = false
	if b.ctx != nil && b.ctx.Err() != nil {
		return // torn down by the user, not a connection failure
	}
	b.failed = true
	b.notify()
}

// randSite mints a random replica identity. Site 0 is the server's own, so it is
// avoided; a 64-bit collision between participants is negligible.
func randSite() crdt.SiteID {
	var buf [8]byte
	_, _ = cryptorand.Read(buf[:])
	id := crdt.SiteID(binary.LittleEndian.Uint64(buf[:]))
	if id == 0 {
		id = 1
	}
	return id
}

// clipboardAPI returns navigator.clipboard, or a null js.Value.
func clipboardAPI() js.Value {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() {
		return js.Null()
	}
	return nav.Get("clipboard")
}

// writeClipboard copies text to the OS clipboard.
func writeClipboard(text string) {
	if clip := clipboardAPI(); clip.Truthy() {
		clip.Call("writeText", text)
	}
}

// readClipboard reads the OS clipboard asynchronously and hands the text to
// onText, or reports a refusal to onErr. navigator.clipboard.readText() is denied
// in very common cases — Firefox blocks it for web content entirely, and Chrome
// rejects it for a background/unfocused tab or without a permission grant — so the
// rejection MUST be handled: without a .catch/onFail the promise settled silently,
// the callback never fired, and the paste looked like a no-op. Both handlers
// release their js.Func and fire at most once.
func readClipboard(onText func(string), onErr func(error)) {
	clip := clipboardAPI()
	if !clip.Truthy() {
		onErr(errNoClipboard)
		return
	}
	promise := clip.Call("readText")
	if !promise.Truthy() {
		onErr(errNoClipboard)
		return
	}
	var onOK, onFail js.Func
	release := func() { onOK.Release(); onFail.Release() }
	onOK = js.FuncOf(func(_ js.Value, a []js.Value) any {
		text := ""
		if len(a) > 0 && a[0].Type() == js.TypeString {
			text = a[0].String()
		}
		release()
		onText(text)
		return nil
	})
	onFail = js.FuncOf(func(_ js.Value, a []js.Value) any {
		msg := "clipboard read rejected"
		if len(a) > 0 && a[0].Truthy() {
			msg = a[0].Call("toString").String()
		}
		release()
		onErr(fmt.Errorf("collab: %s", msg))
		return nil
	})
	// then(onOK, onFail) delivers BOTH settlements, so a rejected readText reaches
	// onFail instead of vanishing.
	promise.Call("then", onOK, onFail)
}

// errNoClipboard is the refusal reported when the Clipboard API is absent.
var errNoClipboard = fmt.Errorf("collab: clipboard unavailable")
