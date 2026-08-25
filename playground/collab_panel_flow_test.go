// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// TestCollabRealPanelWidgetHandshake drives the WHOLE copy-paste handshake the
// way a user does — clicking the REAL panel widgets at their laid-out rects
// through the app's own HandleClick/HandleRelease — and asserts that each click
// actually FIRED its action (a backend call, a clipboard write/read, a phase
// advance), not merely that the click was consumed.
//
// This is the lane the older in-process tests missed: TestCollabDispatchRoles
// calls v.dispatch(role) directly (bypassing the Button's OnEvent path), and
// TestCollabClickRouting only checks that a button click is *consumed*, never
// that it dispatched. A regression that rebuilt the panel from toolkit widgets
// but wired the click routing wrong — a HitTest that misses, an OnClick that
// never fires, a Copy button that writes the wrong blob — would leave both of
// those green while breaking the real two-tab flow. This test would go red.
//
// The transport is a fakeBackend and the clipboard a pair of in-memory hooks, so
// the whole handshake is exercised deterministically with no browser and no
// network; the live browser proof (TestCollabTwoTabConvergence) covers the WebRTC
// transport on top of the same panel widgets.
func TestCollabRealPanelWidgetHandshake(t *testing.T) {
	// clickWidget performs a full user click (press + release, the app's real
	// pointer path) at the centre of the named Collaborate control, resolving its
	// rect the same way the headless two-tab harness does — through the exported
	// CollabButtonRects, which lays the panel out and returns each widget's bounds.
	clickWidget := func(t *testing.T, s *State, name string) {
		t.Helper()
		r, ok := s.CollabButtonRects()[name]
		if !ok {
			t.Fatalf("no Collaborate control %q is visible", name)
		}
		cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
		if !s.HandleClick(cx, cy) {
			t.Fatalf("HandleClick at %q (%d,%d) was not consumed", name, cx, cy)
		}
		s.HandleRelease(cx, cy)
	}

	// labelShown lays the panel out and reports whether a static text line with the
	// exact text is currently displayed — how the test asserts an acknowledgement or
	// prompt is actually on screen, not merely that a flag flipped.
	labelShown := func(s *State, want string) bool {
		s.collab.layout()
		for _, l := range s.collab.labels {
			if l.text == want {
				return true
			}
		}
		return false
	}

	// --- HOST tab -------------------------------------------------------------
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-BLOB"
	var hostClip string
	host.collab.clipWrite = func(text string) { hostClip = text }
	host.collab.clipRead = func(onText func(string), _ func(error)) { onText(hostClip) }

	// Open the panel by clicking the launcher pill (a real Button).
	clickWidget(t, host, "launcher")
	if !host.CollabActive() {
		t.Fatal("clicking the launcher widget did not open the panel")
	}

	// Click Host → the click must reach backend.Host and land the offer + phase.
	clickWidget(t, host, "host")
	if hf.name != host.CollabName() {
		t.Fatalf("clicking Host did not call backend.Host (recorded name %q, want %q)", hf.name, host.CollabName())
	}
	if host.CollabPhase() != int(phaseHostWait) {
		t.Fatalf("clicking Host left phase=%d, want phaseHostWait=%d", host.CollabPhase(), phaseHostWait)
	}
	// The surfaced offer is now an identity envelope wrapping the backend's raw SDP.
	if _, _, _, inner, err := decodeEnvelope(host.CollabOffer()); err != nil || inner != hf.offer {
		t.Fatalf("clicking Host did not surface an envelope wrapping the offer: got %q (inner %q, err %v)", host.CollabOffer(), inner, err)
	}

	// Click Copy invitation → the envelope must actually reach the clipboard.
	clickWidget(t, host, "copyOffer")
	if hostClip != host.CollabOffer() {
		t.Fatalf("Copy invitation wrote %q to the clipboard, want the offer envelope %q", hostClip, host.CollabOffer())
	}

	// --- GUEST tab ------------------------------------------------------------
	guest, gf, _ := withFake(t)
	gf.answer = "GUEST-ANSWER-BLOB"
	var guestClip string
	guest.collab.clipWrite = func(text string) { guestClip = text }
	// The guest's "Paste from clipboard" reads whatever the host put on the shared
	// clipboard, dropping it into the visible field.
	guest.collab.clipRead = func(onText func(string), _ func(error)) { onText(hostClip) }

	clickWidget(t, guest, "launcher")
	clickWidget(t, guest, "join")
	if guest.CollabPhase() != int(phaseGuestOffer) {
		t.Fatalf("clicking Join left phase=%d, want phaseGuestOffer=%d", guest.CollabPhase(), phaseGuestOffer)
	}

	// "Paste from clipboard" fills the VISIBLE field — proof the blob was taken —
	// and does NOT connect on its own.
	clickWidget(t, guest, "pasteOffer")
	if guest.CollabPasteText() != host.CollabOffer() {
		t.Fatalf("Paste from clipboard filled the field with %q, want the host offer envelope %q", guest.CollabPasteText(), host.CollabOffer())
	}
	if gf.gotOffer != "" {
		t.Fatalf("Paste from clipboard should not connect yet, but backend.Join was called with %q", gf.gotOffer)
	}
	// The moment a valid invitation is in the field, the panel previews WHO it is
	// from — the host's name and short id, decoded from the envelope.
	if !labelShown(guest, "Invitation from "+collabPeerLabel(host.CollabName(), host.CollabLocalID())) {
		t.Fatalf("the guest panel does not preview the inviting peer %q", collabPeerLabel(host.CollabName(), host.CollabLocalID()))
	}

	// The primary Connect button joins using the FIELD text, and the panel must
	// acknowledge the attempt (phase advances + the "Connecting…" line shows).
	clickWidget(t, guest, "connectOffer")
	if gf.gotOffer != hf.offer {
		t.Fatalf("Connect handed backend.Join %q, want the field text (host offer) %q", gf.gotOffer, hf.offer)
	}
	if guest.CollabPhase() != int(phaseGuestWait) {
		t.Fatalf("after Connect phase=%d, want phaseGuestWait=%d", guest.CollabPhase(), phaseGuestWait)
	}
	if !guest.CollabConnecting() {
		t.Fatal("after Connect the guest panel should report the connecting acknowledgement")
	}
	// The guest captured the host's identity from the pasted envelope, and the
	// connecting acknowledgement now NAMES the host it is reaching.
	if guest.CollabPeerName() != host.CollabName() || guest.CollabPeerID() != host.CollabLocalID() {
		t.Fatalf("guest captured peer %q/#%q, want the host %q/#%q",
			guest.CollabPeerName(), guest.CollabPeerID(), host.CollabName(), host.CollabLocalID())
	}
	wantGuestConnecting := "Connecting to " + collabPeerLabel(host.CollabName(), host.CollabLocalID()) + "…"
	if !labelShown(guest, wantGuestConnecting) {
		t.Fatalf("the guest panel does not show the peer-named acknowledgement %q", wantGuestConnecting)
	}
	// The surfaced answer is an envelope wrapping the backend's raw reply.
	if _, _, _, inner, err := decodeEnvelope(guest.CollabAnswer()); err != nil || inner != gf.answer {
		t.Fatalf("Join did not surface an envelope wrapping the answer: got %q (inner %q, err %v)", guest.CollabAnswer(), inner, err)
	}

	// Click Copy reply → the answer envelope must reach the guest's clipboard.
	clickWidget(t, guest, "copyAnswer")
	if guestClip != guest.CollabAnswer() {
		t.Fatalf("Copy reply wrote %q, want the answer envelope %q", guestClip, guest.CollabAnswer())
	}

	// --- HOST accepts the reply ----------------------------------------------
	// The host fills its field from the clipboard (the guest's answer), which must
	// NOT connect by itself; then the Connect button accepts the field text.
	hostClip = guestClip
	clickWidget(t, host, "pasteAnswer")
	if host.CollabPasteText() != guest.CollabAnswer() {
		t.Fatalf("Paste from clipboard filled the host field with %q, want the guest answer envelope %q", host.CollabPasteText(), guest.CollabAnswer())
	}
	// The host previews WHO the reply is from (the guest's name + short id).
	if !labelShown(host, "Reply from "+collabPeerLabel(guest.CollabName(), guest.CollabLocalID())) {
		t.Fatalf("the host panel does not preview the replying peer %q", collabPeerLabel(guest.CollabName(), guest.CollabLocalID()))
	}
	if hf.gotAnswer != "" {
		t.Fatalf("Paste from clipboard should not accept yet, but backend.AcceptAnswer got %q", hf.gotAnswer)
	}
	clickWidget(t, host, "connectAnswer")
	if hf.gotAnswer != gf.answer {
		t.Fatalf("Connect handed backend.AcceptAnswer %q, want the field envelope's inner sdp (guest answer) %q", hf.gotAnswer, gf.answer)
	}
	if !host.CollabConnecting() {
		t.Fatal("after Connect the host panel should report the connecting acknowledgement")
	}
	// The host captured the guest's identity and its acknowledgement names the guest.
	if host.CollabPeerName() != guest.CollabName() || host.CollabPeerID() != guest.CollabLocalID() {
		t.Fatalf("host captured peer %q/#%q, want the guest %q/#%q",
			host.CollabPeerName(), host.CollabPeerID(), guest.CollabName(), guest.CollabLocalID())
	}
	wantHostConnecting := "Connecting to " + collabPeerLabel(guest.CollabName(), guest.CollabLocalID()) + "…"
	if !labelShown(host, wantHostConnecting) {
		t.Fatalf("the host panel does not show the peer-named acknowledgement %q", wantHostConnecting)
	}

	// The channel opening is reported by the backend, not the click: flip the
	// fake to connected and fire its change hook, exactly as the WebRTC backend
	// does, and the panel must advance to the live phase.
	hf.connected = true
	hf.peers = 1
	hf.onChange()
	if host.CollabPhase() != int(phaseConnected) {
		t.Fatalf("after the channel opened phase=%d, want phaseConnected=%d", host.CollabPhase(), phaseConnected)
	}
	if !host.CollabConnected() {
		t.Fatal("CollabConnected() is false after the backend reported a live channel")
	}

	// Disconnect through the real widget too, so the whole session lifecycle is
	// driven by clicks.
	clickWidget(t, host, "disconnect")
	if !hf.disconnected {
		t.Fatal("clicking Disconnect did not tear the backend session down")
	}
	if host.CollabPhase() != int(phaseIdle) {
		t.Fatalf("after Disconnect phase=%d, want phaseIdle=%d", host.CollabPhase(), phaseIdle)
	}
}

// TestCollabLocalConnectWidgetFlow drives the zero-config "In this browser
// (instant)" mode the way a user does — clicking the REAL launcher then the
// primary local button at their laid-out rects — and proves the click reaches
// [collabBackend.LocalConnect], the panel acknowledges with the
// phaseLocalConnecting "Connecting in this browser…" line, and the live session
// reported through the change hook (exactly as the backend fires it) advances the
// panel to phaseConnected with the peer summary. It uses the fakeBackend, so it is
// deterministic and browser-free; the live BroadcastChannel transport is proven by
// the two-tab headless test (TestCollabLocalTabConvergence).
func TestCollabLocalConnectWidgetFlow(t *testing.T) {
	clickWidget := func(t *testing.T, s *State, name string) {
		t.Helper()
		r, ok := s.CollabButtonRects()[name]
		if !ok {
			t.Fatalf("no Collaborate control %q is visible", name)
		}
		cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
		if !s.HandleClick(cx, cy) {
			t.Fatalf("HandleClick at %q (%d,%d) was not consumed", name, cx, cy)
		}
		s.HandleRelease(cx, cy)
	}
	labelShown := func(s *State, want string) bool {
		s.collab.layout()
		for _, l := range s.collab.labels {
			if l.text == want {
				return true
			}
		}
		return false
	}

	s, f, _ := withFake(t)

	// Open the panel and click the primary "In this browser (instant)" button.
	clickWidget(t, s, "launcher")
	clickWidget(t, s, "localConnect")

	if !f.localCalled {
		t.Fatal("clicking the local button did not call backend.LocalConnect")
	}
	if f.localName != s.CollabName() {
		t.Fatalf("LocalConnect got name %q, want %q", f.localName, s.CollabName())
	}
	if s.CollabPhase() != int(phaseLocalConnecting) {
		t.Fatalf("after the local click phase=%d, want phaseLocalConnecting=%d", s.CollabPhase(), phaseLocalConnecting)
	}
	if !s.CollabConnecting() {
		t.Fatal("the local mode should report the connecting acknowledgement")
	}
	if !labelShown(s, collabLocalConnectingMsg) {
		t.Fatalf("the panel does not show %q while connecting in this browser", collabLocalConnectingMsg)
	}

	// The live session is reported by the backend (the change hook), not the click:
	// flip the fake to connected with one peer and fire onChange, exactly as the
	// BroadcastChannel backend does when the other tab joins.
	f.connected = true
	f.peers = 1
	f.onChange()
	if s.CollabPhase() != int(phaseConnected) {
		t.Fatalf("after the session came up phase=%d, want phaseConnected=%d", s.CollabPhase(), phaseConnected)
	}
	if s.CollabConnecting() {
		t.Fatal("connecting acknowledgement should clear once connected")
	}
	if !labelShown(s, "Connected — 1 peer editing with you:") {
		t.Fatal("the connected panel does not show the peer summary")
	}

	// Disconnect through the real widget, ending the session cleanly.
	clickWidget(t, s, "disconnect")
	if !f.disconnected {
		t.Fatal("clicking Disconnect did not tear the local session down")
	}
	if s.CollabPhase() != int(phaseIdle) {
		t.Fatalf("after Disconnect phase=%d, want phaseIdle=%d", s.CollabPhase(), phaseIdle)
	}
}

// TestCollabLocalConnectSetupError covers the seam's failure lane: when the
// backend reports a setup error (no BroadcastChannel in this environment), the
// panel clears the connecting acknowledgement, surfaces the message and falls
// back to idle rather than hanging on the acknowledgement forever.
func TestCollabLocalConnectSetupError(t *testing.T) {
	s, f, _ := withFake(t)
	f.localErr = errNoBrowser

	var gotErr error
	s.CollabLocalConnect(func(err error) { gotErr = err })

	if gotErr == nil {
		t.Fatal("CollabLocalConnect done did not receive the setup error")
	}
	if s.CollabPhase() != int(phaseIdle) {
		t.Fatalf("after a setup error phase=%d, want phaseIdle=%d", s.CollabPhase(), phaseIdle)
	}
	if s.CollabConnecting() {
		t.Fatal("a setup error must clear the connecting acknowledgement")
	}
	if s.collab.errMsg == "" {
		t.Fatal("a setup error should surface a message in the errMsg lane")
	}
}
