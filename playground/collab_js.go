// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

package playground

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"syscall/js"

	"github.com/go-crdt/collab"
	"github.com/go-crdt/crdt"
	"github.com/go-widgets/toolkit"
)

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
//     over an IN-PAGE loopback WebRTC pair (the pattern collab's own browsertest
//     uses — there is no in-process pipe transport, so a loopback connection is
//     the supported way for a page to be a client of a server it also runs). The
//     host seeds the document with the current editor source, then serves the
//     remote guest over the real WebRTC data channel.
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

// iceServers is the STUN/TURN configuration for the WebRTC peers. Empty is a
// working configuration for two browsers on the same network and for the in-page
// loopback + headless proof; a deployment that needs to cross NATs sets a STUN
// server here.
var iceServers []string

// EnableCollab installs the real WebRTC backend and the OS-clipboard hooks on
// the playground's Collaborate affordance. The wasm driver calls it once, after
// building the State, passing its repaint function.
func (s *State) EnableCollab(repaint func()) {
	b := &webrtcBackend{s: s, repaint: repaint}
	s.collab.attach(b, repaint)
	s.collab.clipRead = readClipboard
	s.collab.clipWrite = writeClipboard
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
	loopPeers  []*collab.Peer      // host: the in-page loopback pair, closed on teardown
	client     *collab.Client      // the local editor's client (host: its loopback client)
	ct         *toolkit.CollabText // the binding of s.editor to the shared text part

	connected bool
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

// session (re)creates the per-session context.
func (b *webrtcBackend) session() {
	b.ctx, b.cancel = context.WithCancel(context.Background())
}

// Host starts a hosting session: it runs a server, joins it over a loopback pair
// so the host's own editor is a client, seeds the document with the current
// source, and makes the offer to hand to the guest.
func (b *webrtcBackend) Host(name string, color toolkit.RGBA, done func(string, error)) {
	go func() {
		b.session()
		src := b.s.Source()

		b.server = collab.NewServer(collab.Config{Store: collab.NewMemoryStore()})
		joinCh, serveCh, peers, err := connectLoopback(b.ctx)
		if err != nil {
			done("", err)
			return
		}
		b.loopPeers = peers
		go func() { _ = b.server.ServeDataChannel(b.ctx, serveCh) }()

		client, err := collab.Join(b.ctx, collab.DataChannel(joinCh),
			collab.ClientConfig{Document: docName, Site: randSite()})
		if err != nil {
			done("", err)
			return
		}
		if err := b.bind(client, name, color); err != nil {
			done("", err)
			return
		}
		// NewCollabText emptied the editor to the (empty) shared text; refill it,
		// which flows the seed into the CRDT as this participant's first edit.
		b.s.editor.SetText(src)

		peer, err := collab.NewPeer(collab.PeerConfig{ICEServers: iceServers})
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
		ch, err := b.remotePeer.DataChannel(b.ctx)
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
		peer, err := collab.NewPeer(collab.PeerConfig{ICEServers: iceServers})
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

		ch, err := peer.DataChannel(b.ctx) // waits for the host to accept
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
	for _, p := range b.loopPeers {
		_ = p.Close()
	}
	b.loopPeers = nil
	if b.server != nil {
		_ = b.server.Close(context.Background())
		b.server = nil
	}
	if b.cancel != nil {
		b.cancel()
	}
	b.connected = false
}

// Connected reports whether the remote channel is open.
func (b *webrtcBackend) Connected() bool { return b.connected }

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

// fail records a post-handshake failure on the panel and drops the connection.
func (b *webrtcBackend) fail(err error) {
	b.connected = false
	if b.s.collab != nil {
		b.s.collab.errMsg = err.Error()
	}
	b.notify()
}

// connectLoopback opens one WebRTC connection entirely inside this page: a host
// Peer offers, a guest Peer answers, and the offer/answer are handed across in
// process. It returns the channel to JOIN on (the client side), the channel to
// SERVE on (the server side), and the two peers to close on teardown.
func connectLoopback(ctx context.Context) (joinCh, serveCh js.Value, peers []*collab.Peer, err error) {
	h, err := collab.NewPeer(collab.PeerConfig{})
	if err != nil {
		return js.Value{}, js.Value{}, nil, err
	}
	g, err := collab.NewPeer(collab.PeerConfig{})
	if err != nil {
		_ = h.Close()
		return js.Value{}, js.Value{}, nil, err
	}
	peers = []*collab.Peer{h, g}
	offer, err := h.Offer(ctx, "loopback")
	if err != nil {
		return js.Value{}, js.Value{}, peers, err
	}
	answer, err := g.Answer(ctx, offer)
	if err != nil {
		return js.Value{}, js.Value{}, peers, err
	}
	if err := h.AcceptAnswer(answer); err != nil {
		return js.Value{}, js.Value{}, peers, err
	}
	if serveCh, err = h.DataChannel(ctx); err != nil {
		return js.Value{}, js.Value{}, peers, err
	}
	if joinCh, err = g.DataChannel(ctx); err != nil {
		return js.Value{}, js.Value{}, peers, err
	}
	return joinCh, serveCh, peers, nil
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

// readClipboard reads the OS clipboard asynchronously and hands the text to cb.
func readClipboard(cb func(string)) {
	clip := clipboardAPI()
	if !clip.Truthy() {
		return
	}
	promise := clip.Call("readText")
	if !promise.Truthy() {
		return
	}
	var fn js.Func
	fn = js.FuncOf(func(_ js.Value, a []js.Value) any {
		text := ""
		if len(a) > 0 && a[0].Type() == js.TypeString {
			text = a[0].String()
		}
		fn.Release()
		cb(text)
		return nil
	})
	promise.Call("then", fn)
}
