// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// panelHasLabel lays the panel out and reports whether a static text line with
// the exact text is currently shown.
func panelHasLabel(s *State, want string) bool {
	s.collab.layout()
	for _, l := range s.collab.labels {
		if l.text == want {
			return true
		}
	}
	return false
}

// TestCollabPasteFieldEditing drives the visible paste field the way the host
// delivers input: focus it, type into it, ⌘V into it, edit it, and blur it. It is
// the proof that a pasted blob shows up in the field immediately (the "it was
// taken" acknowledgement the user asked for) and that the field owns keyboard
// focus like the name / ICE fields do.
func TestCollabPasteFieldEditing(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.open = true
	v.dispatch(roleJoin) // phaseGuestOffer, paste field auto-focused
	if v.phase != phaseGuestOffer {
		t.Fatalf("roleJoin phase=%d, want guestOffer", v.phase)
	}
	if !v.pasteFocused {
		t.Fatal("entering guestOffer should focus the paste field")
	}

	// Typing a printable rune lands in the field; a key-name (multi-rune) does not.
	if !v.handleChar("A") || !v.handleChar("b") {
		t.Fatal("focused paste field did not consume characters")
	}
	if v.handleChar("Shift"); v.pasteText.Get() != "Ab" {
		t.Fatalf("paste field = %q, want Ab (a key-name must not be inserted)", v.pasteText.Get())
	}

	// A real OS paste (⌘V) routes through the app's HandlePaste into the field.
	if !s.HandlePaste("CDE") {
		t.Fatal("HandlePaste into the focused paste field was not consumed")
	}
	if v.pasteText.Get() != "AbCDE" {
		t.Fatalf("after ⌘V the field = %q, want AbCDE", v.pasteText.Get())
	}

	// Backspace edits; ArrowLeft is swallowed (a focused field is modal).
	s.HandleKeyDown("Backspace")
	if v.pasteText.Get() != "AbCD" {
		t.Fatalf("after Backspace the field = %q, want AbCD", v.pasteText.Get())
	}
	if !v.handleKey("ArrowLeft") {
		t.Fatal("a focused paste field should swallow other keys")
	}

	// Enter blurs the field; Escape re-focus then defocus keeps the panel open.
	if !v.handleKey("Enter") || v.pasteFocused {
		t.Fatal("Enter should blur the paste field")
	}
	v.pasteFocused = true
	if !v.handleKey("Return") || v.pasteFocused {
		t.Fatal("Return should blur the paste field")
	}
	v.pasteFocused = true
	if !v.handleKey("Escape") || v.pasteFocused || !v.open {
		t.Fatal("Escape while the paste field is focused should defocus it but keep the panel open")
	}

	// Backspace on an empty field is a safe no-op.
	v.pasteFocused = true
	v.pasteText.Set("")
	v.handleKey("Backspace")
	if v.pasteText.Get() != "" {
		t.Fatalf("Backspace on an empty field = %q", v.pasteText.Get())
	}
}

// TestCollabHandlePasteRouting covers HandlePaste routing into each focusable
// panel field and its fall-through when nothing there is focused.
func TestCollabHandlePasteRouting(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab

	// Closed panel: the paste is not consumed by the collab view.
	if v.handlePaste("x") {
		t.Fatal("a closed panel should not consume a paste")
	}

	v.open = true
	// ICE field focused: the paste appends to the ICE text.
	v.iceFocused, v.nameFocused, v.pasteFocused = true, false, false
	before := v.iceText.Get()
	if !v.handlePaste("stun:relay") || v.iceText.Get() != before+"stun:relay" {
		t.Fatalf("paste into the ICE field = %q", v.iceText.Get())
	}
	// Name field focused: the paste appends to the name.
	v.iceFocused, v.nameFocused = false, true
	v.name = "Ann"
	if !v.handlePaste("ie") || v.name != "Annie" {
		t.Fatalf("paste into the name field = %q", v.name)
	}
	// Open panel, nothing focused: not consumed (so the editor can take it).
	v.nameFocused = false
	if v.handlePaste("y") {
		t.Fatal("an open panel with no focused field should not consume a paste")
	}
}

// TestCollabConnectEmptyFieldShowsError proves an empty-field Connect is NOT a
// silent no-op: it surfaces the "paste …first" guidance on both sides and does
// not touch the backend or claim to be connecting.
func TestCollabConnectEmptyFieldShowsError(t *testing.T) {
	// Guest side.
	g, gf, _ := withFake(t)
	g.collab.open = true
	g.collab.dispatch(roleJoin) // phaseGuestOffer, empty field
	g.collab.dispatch(roleConnectOffer)
	if g.collab.errMsg == "" {
		t.Fatal("empty guest Connect was silent; want the 'paste the invitation first' error")
	}
	if gf.gotOffer != "" || g.collab.connecting {
		t.Fatalf("empty guest Connect touched the backend (gotOffer=%q) or claimed connecting=%v", gf.gotOffer, g.collab.connecting)
	}
	if g.collab.phase != phaseGuestOffer {
		t.Fatalf("empty guest Connect changed phase to %d", g.collab.phase)
	}

	// Host side.
	h, hf, _ := withFake(t)
	h.collab.open = true
	h.collab.phase = phaseHostWait
	h.collab.dispatch(roleConnectAnswer)
	if h.collab.errMsg == "" {
		t.Fatal("empty host Connect was silent; want the 'paste your peer's reply first' error")
	}
	if hf.gotAnswer != "" || h.collab.connecting {
		t.Fatalf("empty host Connect touched the backend (gotAnswer=%q) or claimed connecting=%v", hf.gotAnswer, h.collab.connecting)
	}
}

// TestCollabPasteFromClipboardRejection is the regression proof for the confirmed
// primary bug: a rejected navigator.clipboard.readText() is no longer silent. The
// in-memory clipboard hook invokes the rejection path, and the panel must surface
// the "couldn't read the clipboard" guidance rather than doing nothing.
func TestCollabPasteFromClipboardRejection(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.open = true
	v.dispatch(roleJoin) // phaseGuestOffer

	// The clipboard read is DENIED (as Firefox does for web content, or Chrome for
	// a background tab): the hook fires onErr, never onText.
	v.clipRead = func(_ func(string), onErr func(error)) { onErr(errNoBrowser) }
	before := v.pasteText.Get()
	v.dispatch(rolePasteOffer)

	if v.errMsg != collabClipReadErrMsg {
		t.Fatalf("after a denied clipboard read errMsg=%q, want %q", v.errMsg, collabClipReadErrMsg)
	}
	if v.pasteText.Get() != before {
		t.Fatalf("a denied clipboard read should leave the field untouched, got %q", v.pasteText.Get())
	}
}

// TestCollabPasteFieldClickFocus drives the app's real pointer path onto the
// paste field and the other panel controls, proving click focus moves between the
// paste field, the name field, the ICE field and a button (each clears the
// others).
func TestCollabPasteFieldClickFocus(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.open = true
	v.phase = phaseGuestOffer
	v.layout()

	// Click the visible paste field: it gains focus.
	if !v.handleClick(v.pasteRect.X+2, v.pasteRect.Y+2) || !v.pasteFocused {
		t.Fatal("clicking the paste field did not focus it")
	}
	// Click the ICE field: focus moves off the paste field.
	v.layout()
	if !v.handleClick(v.iceRect.X+2, v.iceRect.Y+2) || !v.iceFocused || v.pasteFocused {
		t.Fatal("clicking the ICE field did not move focus off the paste field")
	}
	// Re-focus the paste field, then click the name field: focus moves again.
	v.pasteFocused, v.iceFocused = true, false
	v.layout()
	if !v.handleClick(v.nameRect.X+2, v.nameRect.Y+2) || !v.nameFocused || v.pasteFocused {
		t.Fatal("clicking the name field did not move focus off the paste field")
	}
	// Re-focus the paste field, then click a button (Cancel): the fall-through
	// clears the paste focus.
	v.pasteFocused, v.nameFocused = true, false
	v.layout()
	var cancel [4]int
	for _, b := range v.buttons {
		if b.role == roleCancel {
			cancel = [4]int{b.rect.X, b.rect.Y, b.rect.W, b.rect.H}
		}
	}
	if !v.handleClick(cancel[0]+cancel[2]/2, cancel[1]+cancel[3]/2) {
		t.Fatal("the Cancel click was not consumed")
	}
	if v.pasteFocused {
		t.Fatal("clicking a button should clear the paste-field focus")
	}
}

// TestCollabPasteFieldRendersOnScreen proves the visible paste field is actually
// painted (not just laid out): in phaseGuestOffer the field's exposed rect must be
// non-empty and the pixel at its centre must differ from the modal scrim behind
// the panel — i.e. the card + Entry were drawn over the dim.
func TestCollabPasteFieldRendersOnScreen(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.open = true
	v.phase = phaseGuestOffer
	v.pasteText.Set("HOST-INVITATION-BLOB")

	buf := make([]byte, testW*testH*4)
	s.Draw(buf)

	pr, ok := s.CollabButtonRects()["paste"]
	if !ok || pr[2] <= 0 || pr[3] <= 0 {
		t.Fatalf("the paste field rect is not exposed while pasting an invitation: %v ok=%v", pr, ok)
	}
	// A point over the scrim but outside the panel card (just inside the body, hard
	// left) versus the field's centre: the field must paint something else. The
	// body now begins at bodyTop() (below the moved-in topZone band + the toolbar),
	// so the scrim reference point is sampled there — a layout-shift recalibration,
	// not a behaviour change.
	scrim := samplePixel(buf, 2, s.bodyTop()+2)
	field := samplePixel(buf, pr[0]+pr[2]/2, pr[1]+pr[3]/2)
	if field == scrim {
		t.Fatalf("the paste field pixel %+v equals the scrim %+v — the field was not painted", field, scrim)
	}
}

// TestCollabHostWaitConnectingDraw renders phaseHostWait while connecting, so the
// panel's "Connecting…" acknowledgement branch (which replaces the paste prompt)
// is exercised and shows the message.
func TestCollabHostWaitConnectingDraw(t *testing.T) {
	s, _, _ := withFake(t)
	v := s.collab
	v.open = true
	v.phase = phaseHostWait
	v.connecting = true
	v.offer = "OFFER"
	if !panelHasLabel(s, collabConnectingMsg) {
		t.Fatalf("a connecting host panel does not show %q", collabConnectingMsg)
	}
	// The paste field is hidden while connecting.
	if v.pasteRect.W != 0 {
		t.Fatal("the paste field should be hidden while the host is connecting")
	}
	s.Draw(make([]byte, testW*testH*4)) // must not panic
}
