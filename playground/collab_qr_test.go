// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-gfx/qr"
	"github.com/go-widgets/toolkit"
)

// sampleSDP is a representative WebRTC offer SDP — the kind pion/webrtc produces —
// so the QR-size measurement and the envelope round-trips run against a realistic
// (highly compressible) signalling blob rather than a toy string.
const sampleSDP = "v=0\r\n" +
	"o=- 4611731400430051336 2 IN IP4 127.0.0.1\r\n" +
	"s=-\r\nt=0 0\r\n" +
	"a=group:BUNDLE 0\r\n" +
	"a=extmap-allow-mixed\r\n" +
	"a=msid-semantic: WMS\r\n" +
	"m=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n" +
	"c=IN IP4 0.0.0.0\r\n" +
	"a=ice-ufrag:F7gI\r\n" +
	"a=ice-pwd:x9cml/YzichV2+XlhiMu8g\r\n" +
	"a=ice-options:trickle\r\n" +
	"a=fingerprint:sha-256 " +
	"12:34:56:78:9A:BC:DE:F0:12:34:56:78:9A:BC:DE:F0:12:34:56:78:9A:BC:DE:F0:12:34:56:78:9A:BC:DE:F0\r\n" +
	"a=setup:actpass\r\n" +
	"a=mid:0\r\n" +
	"a=sctp-port:5000\r\n" +
	"a=max-message-size:262144\r\n" +
	"a=candidate:1 1 UDP 2130706431 192.168.1.42 51820 typ host\r\n" +
	"a=candidate:2 1 UDP 1694498815 203.0.113.7 51820 typ srflx raddr 192.168.1.42 rport 51820\r\n"

// sampleEnvelope builds a realistic identity envelope wrapping sampleSDP, the input
// the scan-to-connect URL/QR helpers actually compress and encode.
func sampleEnvelope() string {
	return encodeEnvelope("A3F9", "Bright Wren", "#1e88e5", sampleSDP)
}

// TestCompressEnvelopeRoundTrip proves the brotli+base64url payload is URL-safe,
// smaller than the raw envelope, and decompresses back byte-for-byte.
func TestCompressEnvelopeRoundTrip(t *testing.T) {
	env := sampleEnvelope()
	payload := compressEnvelope(env)

	// base64url (no padding) is URL-fragment-safe: only [A-Za-z0-9_-].
	for _, r := range payload {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			t.Fatalf("payload has non-base64url rune %q", r)
		}
	}
	if len(payload) >= len(env) {
		t.Fatalf("payload (%d) not smaller than the envelope (%d) — compression did nothing", len(payload), len(env))
	}
	got, err := decompressEnvelope(payload)
	if err != nil {
		t.Fatalf("decompressEnvelope: %v", err)
	}
	if got != env {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, env)
	}
}

// TestDecompressEnvelopeMalformed covers both error lanes: a non-base64url payload
// and a well-encoded but non-brotli byte stream.
func TestDecompressEnvelopeMalformed(t *testing.T) {
	if _, err := decompressEnvelope("not base64url !!!"); !errors.Is(err, errBadSignalURL) {
		t.Fatalf("bad base64url: err = %v, want errBadSignalURL", err)
	}
	// "AAAA" is valid base64url (4 zero-ish bytes) but not a valid brotli stream.
	if _, err := decompressEnvelope("AAAAAAAA"); !errors.Is(err, errBadSignalURL) {
		t.Fatalf("non-brotli bytes: err = %v, want errBadSignalURL", err)
	}
}

// TestSignalURLRoundTrip proves buildSignalURL/parseSignalURL are inverses for both
// fragment kinds and that the URL has the expected shape.
func TestSignalURLRoundTrip(t *testing.T) {
	const base = "https://go-tex.github.io/playground/"
	env := sampleEnvelope()

	for _, frag := range []string{fragInvite, fragAnswer} {
		url := buildSignalURL(base, frag, env)
		if !strings.HasPrefix(url, base+"#"+frag+"=") {
			t.Fatalf("URL %q missing expected #%s= prefix", url, frag)
		}
		kind, got, err := parseSignalURL(url)
		if err != nil {
			t.Fatalf("parseSignalURL(%s): %v", frag, err)
		}
		if kind != frag {
			t.Fatalf("parsed kind = %q, want %q", kind, frag)
		}
		if got != env {
			t.Fatalf("parsed envelope mismatch for %s", frag)
		}
	}
}

// TestParseSignalURLForms covers the accepted input forms (full URL, bare fragment,
// leading-hash fragment, surrounding whitespace) and every rejection lane.
func TestParseSignalURLForms(t *testing.T) {
	env := sampleEnvelope()
	payload := compressEnvelope(env)

	// Accepted: bare "key=payload" (no '#', so the hash branch is skipped).
	if k, got, err := parseSignalURL(fragInvite + "=" + payload); err != nil || k != fragInvite || got != env {
		t.Fatalf("bare fragment: k=%q err=%v env-ok=%v", k, err, got == env)
	}
	// Accepted: leading-hash fragment with surrounding whitespace.
	if k, _, err := parseSignalURL("  #" + fragAnswer + "=" + payload + "  "); err != nil || k != fragAnswer {
		t.Fatalf("hash fragment: k=%q err=%v", k, err)
	}

	// Rejections, all -> errBadSignalURL:
	bad := map[string]string{
		"empty":       "",
		"no equals":   "#invite",
		"unknown key": "#foo=" + payload,
		"junk base64": "#invite=notbase64!!!",
		"non-brotli":  "#answer=AAAAAAAA",
	}
	for name, in := range bad {
		if _, _, err := parseSignalURL(in); !errors.Is(err, errBadSignalURL) {
			t.Fatalf("%s: err = %v, want errBadSignalURL", name, err)
		}
	}
}

// TestBuildSignalQRTypical proves a realistic invitation encodes to a scannable QR
// under the version cap, and REPORTS the measured version so a reviewer can see the
// headroom against the cap.
func TestBuildSignalQRTypical(t *testing.T) {
	url := buildSignalURL(signalBaseURL, fragInvite, sampleEnvelope())

	m, err := qr.Encode([]byte(url), qr.WithLevel(qr.Low), qr.WithVersionRange(qr.MinVersion, qrMaxVersion))
	if err != nil {
		t.Fatalf("typical invitation did not fit under the v%d cap: %v", qrMaxVersion, err)
	}
	t.Logf("typical invitation: URL %d bytes, payload %d bytes, QR version %d (cap %d), dimension %d modules",
		len(url), len(compressEnvelope(sampleEnvelope())), m.Version, qrMaxVersion, m.Dimension())
	if m.Version > qrMaxVersion {
		t.Fatalf("QR version %d exceeds cap %d", m.Version, qrMaxVersion)
	}

	px, w, h, ok := buildSignalQR(url)
	if !ok || w <= 0 || h != w || len(px) != w*h*4 {
		t.Fatalf("buildSignalQR: ok=%v w=%d h=%d len=%d", ok, w, h, len(px))
	}
}

// TestBuildSignalQRTooLong proves an oversized payload fails the cap (not a broken
// QR): buildSignalQR reports ok=false so the caller can fall back to copy-paste.
func TestBuildSignalQRTooLong(t *testing.T) {
	huge := "https://go-tex.github.io/playground/#invite=" + strings.Repeat("A", 4000)
	if _, _, _, ok := buildSignalQR(huge); ok {
		t.Fatal("buildSignalQR accepted a payload beyond the version cap; want ok=false")
	}
}

// TestQRMatrixRGBA covers the rasteriser: a real code produces both black and white
// pixels at the right buffer size, and the scale/quiet guards clamp their arguments.
func TestQRMatrixRGBA(t *testing.T) {
	m, err := qr.Encode([]byte("scan-to-connect"), qr.WithLevel(qr.Low))
	if err != nil {
		t.Fatalf("qr.Encode: %v", err)
	}

	px, w, h := qrMatrixRGBA(m, 3, 2)
	wantSide := (m.Dimension() + 2*2) * 3
	if w != wantSide || h != wantSide || len(px) != w*h*4 {
		t.Fatalf("dims: w=%d h=%d len=%d, want side %d", w, h, len(px), wantSide)
	}
	var black, white bool
	for i := 0; i+3 < len(px); i += 4 {
		if px[i+3] != 0xFF {
			t.Fatalf("pixel %d not opaque (alpha %d)", i/4, px[i+3])
		}
		switch px[i] {
		case 0x00:
			black = true
		case 0xFF:
			white = true
		}
	}
	if !black || !white {
		t.Fatalf("raster missing modules: black=%v white=%v", black, white)
	}

	// The guards clamp scale<1 to 1 and quiet<0 to 0, so the side is exactly the
	// code dimension in modules.
	px0, w0, h0 := qrMatrixRGBA(m, 0, -1)
	if w0 != m.Dimension() || h0 != m.Dimension() || len(px0) != w0*h0*4 {
		t.Fatalf("clamped dims: w=%d h=%d, want %d", w0, h0, m.Dimension())
	}
}

// TestCollabViewBuildQR exercises the view's buildQR/clearQR bookkeeping: an empty
// blob clears, a normal blob produces a scannable raster, and an oversized URL sets
// the too-large fallback flag instead of a broken raster.
func TestCollabViewBuildQR(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab

	// A prior raster is cleared by an empty blob.
	v.qrShown, v.qrTooLarge = true, true
	v.buildQR(fragInvite, "")
	if v.qrShown || v.qrTooLarge || v.qrPixels != nil {
		t.Fatalf("empty blob did not clear the QR: shown=%v tooLarge=%v", v.qrShown, v.qrTooLarge)
	}

	// A normal envelope yields a scannable raster.
	v.buildQR(fragInvite, sampleEnvelope())
	if !v.qrShown || v.qrTooLarge || len(v.qrPixels) != v.qrW*v.qrH*4 {
		t.Fatalf("normal blob: shown=%v tooLarge=%v len=%d", v.qrShown, v.qrTooLarge, len(v.qrPixels))
	}

	// An enormous base URL pushes the payload past the cap -> too-large fallback.
	v.baseURL = "https://example.test/" + strings.Repeat("x", 5000) + "/"
	v.buildQR(fragAnswer, sampleEnvelope())
	if v.qrShown || !v.qrTooLarge {
		t.Fatalf("oversized URL: shown=%v tooLarge=%v, want the too-large fallback", v.qrShown, v.qrTooLarge)
	}

	v.clearQR()
	if v.qrShown || v.qrTooLarge || v.qrPixels != nil {
		t.Fatal("clearQR left QR state behind")
	}
}

// TestSetCollabBaseURL proves a non-empty base is adopted and an empty one is
// ignored (keeping the built-in default).
func TestSetCollabBaseURL(t *testing.T) {
	s := newTestState(t, false)
	if s.collab.baseURL != signalBaseURL {
		t.Fatalf("default baseURL = %q, want %q", s.collab.baseURL, signalBaseURL)
	}
	s.SetCollabBaseURL("https://example.test/pg/")
	if s.collab.baseURL != "https://example.test/pg/" {
		t.Fatalf("base not adopted: %q", s.collab.baseURL)
	}
	s.SetCollabBaseURL("")
	if s.collab.baseURL != "https://example.test/pg/" {
		t.Fatalf("empty base overwrote the value: %q", s.collab.baseURL)
	}
}

// TestCollabApplySignalFragmentInvite proves the one-tap join: opening the app from
// an "#invite=…" link opens the panel and joins the host exactly as a pasted
// invitation would, capturing the host's identity.
func TestCollabApplySignalFragmentInvite(t *testing.T) {
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-SDP"
	host.SetCollabOpen(true)
	host.CollabHost(nil) // fakeBackend completes synchronously -> phaseHostWait + envelope

	url := buildSignalURL(signalBaseURL, fragInvite, host.CollabOffer())

	guest, gf, _ := withFake(t)
	gf.answer = "GUEST-ANSWER-SDP"
	kind, ok := guest.CollabApplySignalFragment(url)
	if !ok || kind != fragInvite {
		t.Fatalf("apply invite: ok=%v kind=%q", ok, kind)
	}
	if !guest.CollabActive() {
		t.Fatal("applying an invite did not open the Collaborate panel")
	}
	if guest.CollabPasteText() != host.CollabOffer() {
		t.Fatal("the decoded invitation was not dropped into the visible paste field")
	}
	if gf.gotOffer != hf.offer {
		t.Fatalf("backend.Join got %q, want the host's inner SDP %q", gf.gotOffer, hf.offer)
	}
	if guest.CollabPhase() != int(phaseGuestWait) {
		t.Fatalf("phase = %d, want phaseGuestWait", guest.CollabPhase())
	}
	if guest.CollabPeerName() != host.CollabName() || guest.CollabPeerID() != host.CollabLocalID() {
		t.Fatalf("guest captured %q/#%q, want host %q/#%q",
			guest.CollabPeerName(), guest.CollabPeerID(), host.CollabName(), host.CollabLocalID())
	}
}

// TestCollabApplySignalFragmentAnswer proves the reply direction: a host opened from
// an "#answer=…" link hands the decoded reply to the backend (AcceptAnswer).
func TestCollabApplySignalFragmentAnswer(t *testing.T) {
	// Build a real guest answer envelope first.
	guest, gf, _ := withFake(t)
	gf.answer = "GUEST-ANSWER-SDP"
	guest.SetCollabOpen(true)
	guest.collab.phase = phaseGuestOffer
	guest.CollabJoin(encodeEnvelope("H0ST", "Host", "#e53935", "HOST-OFFER-SDP"), nil)
	answerEnvelope := guest.CollabAnswer()
	if answerEnvelope == "" {
		t.Fatal("guest produced no answer envelope")
	}

	url := buildSignalURL(signalBaseURL, fragAnswer, answerEnvelope)

	host, hf, _ := withFake(t)
	kind, ok := host.CollabApplySignalFragment(url)
	if !ok || kind != fragAnswer {
		t.Fatalf("apply answer: ok=%v kind=%q", ok, kind)
	}
	if hf.gotAnswer != gf.answer {
		t.Fatalf("backend.AcceptAnswer got %q, want the guest's inner SDP %q", hf.gotAnswer, gf.answer)
	}
	if host.CollabPeerName() != guest.CollabName() {
		t.Fatalf("host captured peer %q, want guest %q", host.CollabPeerName(), guest.CollabName())
	}
}

// labelShownQR lays the panel out and reports whether an exact static text line is
// currently displayed.
func labelShownQR(s *State, want string) bool {
	s.collab.layout()
	for _, l := range s.collab.labels {
		if l.text == want {
			return true
		}
	}
	return false
}

// qrBoxHasCode samples the laid-out QR rectangle in a rendered frame and reports
// whether it holds BOTH near-black and near-white pixels — the signature of a real
// QR raster rather than a blank box (the panel surface alone is neither pure black
// nor pure white).
func qrBoxHasCode(t *testing.T, s *State, buf []byte) bool {
	t.Helper()
	r, ok := s.CollabButtonRects()["qr"]
	if !ok {
		t.Fatal("no QR rectangle is laid out")
	}
	x0, y0, w, h := r[0], r[1], r[2], r[3]
	var black, white bool
	for y := y0; y < y0+h && y < testH; y++ {
		for x := x0; x < x0+w && x < testW; x++ {
			i := (y*testW + x) * 4
			switch {
			case buf[i] < 0x30 && buf[i+1] < 0x30 && buf[i+2] < 0x30:
				black = true
			case buf[i] > 0xD0 && buf[i+1] > 0xD0 && buf[i+2] > 0xD0:
				white = true
			}
		}
	}
	return black && white
}

// TestCollabPanelRendersHostWaitQR drives the host to phaseHostWait through the real
// backend and asserts the invitation QR actually PAINTS (black+white modules in its
// box) under its "Scan to join from a phone:" caption, while the #41 copy-invitation
// flow stands unchanged beside it.
func TestCollabPanelRendersHostWaitQR(t *testing.T) {
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-SDP-BLOB"
	host.SetCollabOpen(true)
	host.CollabHost(nil)
	if host.CollabPhase() != int(phaseHostWait) {
		t.Fatalf("phase = %d, want phaseHostWait", host.CollabPhase())
	}
	if !host.collab.qrShown {
		t.Fatal("no invitation QR was produced in host-wait")
	}
	if !labelShownQR(host, "Scan to join from a phone:") {
		t.Fatal("the invitation QR caption is not shown")
	}
	// The #41 copy-invitation control and prompt still coexist with the QR.
	if _, ok := host.CollabButtonRects()["copyOffer"]; !ok {
		t.Fatal("the Copy invitation button was displaced by the QR")
	}
	if !labelShownQR(host, "1. Send this invitation to your peer:") {
		t.Fatal("the invitation prompt was displaced by the QR")
	}

	buf := make([]byte, testW*testH*4)
	host.Draw(buf)
	if !qrBoxHasCode(t, host, buf) {
		t.Fatal("the invitation QR box did not render a code (no black+white modules)")
	}
}

// TestCollabPanelRendersGuestWaitQR drives the guest to phaseGuestWait and asserts
// the reply QR paints under its "Scan to reply from a phone:" caption.
func TestCollabPanelRendersGuestWaitQR(t *testing.T) {
	guest, gf, _ := withFake(t)
	gf.answer = "GUEST-ANSWER-SDP-BLOB"
	guest.SetCollabOpen(true)
	guest.collab.phase = phaseGuestOffer
	guest.CollabJoin(encodeEnvelope("H0ST", "Host", "#e53935", "HOST-OFFER-SDP"), nil)
	if guest.CollabPhase() != int(phaseGuestWait) {
		t.Fatalf("phase = %d, want phaseGuestWait", guest.CollabPhase())
	}
	if !guest.collab.qrShown || !labelShownQR(guest, "Scan to reply from a phone:") {
		t.Fatal("the reply QR / caption is not shown in guest-wait")
	}

	buf := make([]byte, testW*testH*4)
	guest.Draw(buf)
	if !qrBoxHasCode(t, guest, buf) {
		t.Fatal("the reply QR box did not render a code")
	}
}

// TestCollabPanelQRNarrowClamp proves the QR box is clamped to the panel's inner
// width on a narrow viewport, rather than overflowing the card.
func TestCollabPanelQRNarrowClamp(t *testing.T) {
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-SDP-BLOB"
	host.SetCollabOpen(true)
	host.CollabHost(nil)
	if !host.collab.qrShown {
		t.Fatal("no QR produced")
	}
	// Shrink the viewport so the panel's inner width is narrower than the QR's
	// natural box — the clamp path.
	host.Resize(170, 600)
	host.collab.layout()
	innerW := host.collab.panel.W - 2*toolkit.Scaled(8)
	if innerW <= 0 || innerW >= toolkit.Scaled(150) {
		t.Fatalf("test viewport not narrow enough: innerW=%d, want 0 < innerW < %d", innerW, toolkit.Scaled(150))
	}
	if host.collab.qrRect.W != innerW {
		t.Fatalf("QR box width = %d, want it clamped to innerW = %d", host.collab.qrRect.W, innerW)
	}
}

// TestCollabPanelQRTooLargeFallback proves that when the payload is too large for a
// scannable code, the panel shows the copy-paste fallback line and reserves NO QR
// box, rather than painting a broken/unscannable square.
func TestCollabPanelQRTooLargeFallback(t *testing.T) {
	host, hf, _ := withFake(t)
	hf.offer = "HOST-OFFER-SDP-BLOB"
	host.SetCollabBaseURL("https://example.test/" + strings.Repeat("x", 5000) + "/")
	host.SetCollabOpen(true)
	host.CollabHost(nil)

	if host.collab.qrShown || !host.collab.qrTooLarge {
		t.Fatalf("want too-large fallback: shown=%v tooLarge=%v", host.collab.qrShown, host.collab.qrTooLarge)
	}
	if !labelShownQR(host, collabQRTooLargeMsg) {
		t.Fatalf("the too-large fallback line %q is not shown", collabQRTooLargeMsg)
	}
	if _, ok := host.CollabButtonRects()["qr"]; ok {
		t.Fatal("a QR box was reserved despite the too-large fallback")
	}
	// The copy-paste flow is intact.
	if _, ok := host.CollabButtonRects()["copyOffer"]; !ok {
		t.Fatal("the Copy invitation flow was lost in the fallback")
	}
}

// TestCollabApplySignalFragmentMalformed proves a junk hash is ignored: no panel
// opens and nothing is dispatched.
func TestCollabApplySignalFragmentMalformed(t *testing.T) {
	s, f, _ := withFake(t)
	if kind, ok := s.CollabApplySignalFragment("#invite=not-valid!!!"); ok || kind != "" {
		t.Fatalf("malformed fragment applied: kind=%q ok=%v", kind, ok)
	}
	if s.CollabActive() {
		t.Fatal("a malformed fragment opened the panel")
	}
	if f.gotOffer != "" || f.gotAnswer != "" {
		t.Fatal("a malformed fragment reached the backend")
	}
}
