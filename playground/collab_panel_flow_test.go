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

	// --- HOST tab -------------------------------------------------------------
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-BLOB"
	var hostClip string
	host.collab.clipWrite = func(text string) { hostClip = text }
	host.collab.clipRead = func(cb func(string)) { cb(hostClip) }

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
	if host.CollabOffer() != hf.offer {
		t.Fatalf("clicking Host did not surface the offer: got %q", host.CollabOffer())
	}

	// Click Copy invitation → the offer must actually reach the clipboard.
	clickWidget(t, host, "copyOffer")
	if hostClip != hf.offer {
		t.Fatalf("Copy invitation wrote %q to the clipboard, want the offer %q", hostClip, hf.offer)
	}

	// --- GUEST tab ------------------------------------------------------------
	guest, gf, _ := withFake(t)
	gf.answer = "GUEST-ANSWER-BLOB"
	var guestClip string
	guest.collab.clipWrite = func(text string) { guestClip = text }
	// The guest's Paste button reads whatever the host put on the shared clipboard.
	guest.collab.clipRead = func(cb func(string)) { cb(hostClip) }

	clickWidget(t, guest, "launcher")
	clickWidget(t, guest, "join")
	if guest.CollabPhase() != int(phaseGuestOffer) {
		t.Fatalf("clicking Join left phase=%d, want phaseGuestOffer=%d", guest.CollabPhase(), phaseGuestOffer)
	}

	// Click Paste invitation → backend.Join must be called WITH the host's offer,
	// and the answer + phase must land.
	clickWidget(t, guest, "pasteOffer")
	if gf.gotOffer != hf.offer {
		t.Fatalf("Paste invitation handed backend.Join %q, want the host offer %q", gf.gotOffer, hf.offer)
	}
	if guest.CollabPhase() != int(phaseGuestWait) {
		t.Fatalf("after pasting the invitation phase=%d, want phaseGuestWait=%d", guest.CollabPhase(), phaseGuestWait)
	}
	if guest.CollabAnswer() != gf.answer {
		t.Fatalf("Join did not surface the answer: got %q", guest.CollabAnswer())
	}

	// Click Copy reply → the answer must reach the guest's clipboard.
	clickWidget(t, guest, "copyAnswer")
	if guestClip != gf.answer {
		t.Fatalf("Copy reply wrote %q, want the answer %q", guestClip, gf.answer)
	}

	// --- HOST accepts the reply ----------------------------------------------
	// The host pastes the guest's answer (now on the shared clipboard).
	hostClip = guestClip
	clickWidget(t, host, "pasteAnswer")
	if hf.gotAnswer != gf.answer {
		t.Fatalf("Paste reply handed backend.AcceptAnswer %q, want the guest answer %q", hf.gotAnswer, gf.answer)
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
