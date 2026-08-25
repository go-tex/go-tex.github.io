// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// TestCollabEnvelopeRoundTrip proves an identity envelope encodes and decodes back
// to the same {id, name, colour, inner sdp}, tolerating surrounding whitespace.
func TestCollabEnvelopeRoundTrip(t *testing.T) {
	env := encodeEnvelope("A3F9", "Swift Fox", "#e53935", "v=0\r\no=- 1 2 IN IP4 0.0.0.0")
	// A real signalling blob has no place in a bare word: the envelope is base64, so
	// pasting it back with stray whitespace still decodes.
	id, name, color, inner, err := decodeEnvelope("  \n" + env + "\t ")
	if err != nil {
		t.Fatalf("decodeEnvelope(round-trip) err = %v", err)
	}
	if id != "A3F9" || name != "Swift Fox" || color != "#e53935" || inner != "v=0\r\no=- 1 2 IN IP4 0.0.0.0" {
		t.Fatalf("round-trip = %q/%q/%q/%q, want the originals", id, name, color, inner)
	}
	// The envelope is not the raw SDP — the blob format changed, deliberately.
	if env == inner {
		t.Fatal("the envelope must wrap (not equal) the inner sdp")
	}
}

// TestDecodeEnvelopeErrors covers every rejection path: a blob that is not base64,
// base64 that is not this JSON, the wrong version, and a missing inner sdp. Each
// must yield errBadEnvelope so the panel can show a clear message; an old
// pre-envelope blob (a raw SDP) hits the first of these.
func TestDecodeEnvelopeErrors(t *testing.T) {
	cases := []struct {
		name string
		blob string
	}{
		{"empty", ""},
		{"not base64 (raw SDP, old format)", "v=0\r\no=- 42 IN IP4 0.0.0.0"},
		{"base64 but not JSON", base64.StdEncoding.EncodeToString([]byte("not json at all"))},
		{"wrong version", encodeEnvelopeVersioned(2, "id", "n", "#000000", "sdp")},
		{"missing inner sdp", encodeEnvelope("id", "n", "#000000", "")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, _, _, err := decodeEnvelope(c.blob); err != errBadEnvelope {
				t.Fatalf("decodeEnvelope(%q) err = %v, want errBadEnvelope", c.blob, err)
			}
		})
	}
}

// encodeEnvelopeVersioned builds an envelope with an explicit version, so a test can
// exercise decodeEnvelope's version check.
func encodeEnvelopeVersioned(v int, id, name, colorHex, inner string) string {
	buf, _ := json.Marshal(collabEnvelope{V: v, ID: id, Name: name, Color: colorHex, SDP: inner})
	return base64.StdEncoding.EncodeToString(buf)
}

// TestCollabPeerLabel covers all three formatting branches.
func TestCollabPeerLabel(t *testing.T) {
	if got := collabPeerLabel("Alice", "A3F9"); got != "Alice #A3F9" {
		t.Fatalf("named+id label = %q", got)
	}
	if got := collabPeerLabel("", "A3F9"); got != "(anonymous) #A3F9" {
		t.Fatalf("anonymous label = %q", got)
	}
	if got := collabPeerLabel("Bob", ""); got != "Bob" {
		t.Fatalf("no-id label = %q, want the bare name", got)
	}
}

// TestCollabRandomIDShape proves the id is deterministic under a fixed seed, has the
// documented length, and draws only from the id alphabet.
func TestCollabRandomIDShape(t *testing.T) {
	v1 := &collabView{rng: rand.New(rand.NewSource(1234))}
	v2 := &collabView{rng: rand.New(rand.NewSource(1234))}
	id1, id2 := v1.randomID(), v2.randomID()
	if id1 != id2 {
		t.Fatalf("same seed produced different ids %q vs %q", id1, id2)
	}
	if len(id1) != idLength {
		t.Fatalf("id %q length = %d, want %d", id1, len(id1), idLength)
	}
	for _, r := range id1 {
		if !strings.ContainsRune(idAlphabet, r) {
			t.Fatalf("id %q contains %q, outside the alphabet", id1, r)
		}
	}
}

// TestCollabLocalIDStableAndShown proves the local id is non-empty, is shown in the
// identity row as "#<id>", and — unlike the name/colour — survives roleShuffle.
func TestCollabLocalIDStableAndShown(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	id := s.CollabLocalID()
	if id == "" {
		t.Fatal("CollabLocalID is empty")
	}

	// The identity row shows the id next to the name.
	v.open = true
	if !panelHasLabel(s, "#"+id) {
		t.Fatalf("the identity row does not show the id label %q", "#"+id)
	}

	// roleShuffle re-rolls the cosmetic name/colour but NEVER the id.
	for i := 0; i < 30; i++ {
		v.dispatch(roleShuffle)
		if s.CollabLocalID() != id {
			t.Fatalf("roleShuffle changed the id from %q to %q", id, s.CollabLocalID())
		}
	}
}

// TestCollabPeerColorHexUnknown proves the peer colour accessor is empty before any
// peer is decoded, and reflects a decoded peer's colour afterwards — including a
// malformed colour, which falls back to the zero swatch.
func TestCollabPeerColorHexUnknown(t *testing.T) {
	s := newTestState(t, false)
	if got := s.CollabPeerColorHex(); got != "" {
		t.Fatalf("CollabPeerColorHex before any peer = %q, want empty", got)
	}
	if got := s.CollabPeerName(); got != "" {
		t.Fatalf("CollabPeerName before any peer = %q, want empty", got)
	}

	// A decoded peer with a malformed colour keeps the identity but falls back to the
	// zero swatch (#000000) rather than rejecting the whole envelope.
	s2, f2, _ := withFake(t)
	f2.answer = "A"
	s2.CollabJoin(encodeEnvelope("P33R", "Peer", "not-a-colour", "HOST-SDP"), nil)
	if s2.CollabPeerName() != "Peer" || s2.CollabPeerID() != "P33R" {
		t.Fatalf("peer identity = %q/%q, want Peer/P33R", s2.CollabPeerName(), s2.CollabPeerID())
	}
	if got := s2.CollabPeerColorHex(); got != "#000000" {
		t.Fatalf("malformed peer colour = %q, want the zero-swatch fallback #000000", got)
	}
}

// TestCollabPeerPreviewInvalidPaste proves that with a non-envelope blob in the
// paste field the guest panel shows NO "Invitation from" preview line (pastedPeer
// returns false) — only a valid envelope previews the sender.
func TestCollabPeerPreviewInvalidPaste(t *testing.T) {
	s := newTestState(t, false)
	v := s.collab
	v.open = true
	v.phase = phaseGuestOffer
	v.pasteText.Set("this is not an envelope")
	v.layout()
	for _, l := range v.labels {
		if strings.HasPrefix(l.text, "Invitation from ") {
			t.Fatalf("a non-envelope paste should not preview a peer, but saw %q", l.text)
		}
	}

	// A valid envelope previews the sender.
	v.pasteText.Set(encodeEnvelope("Z9Q1", "Zed", "#1e88e5", "SDP"))
	if !panelHasLabel(s, "Invitation from "+collabPeerLabel("Zed", "Z9Q1")) {
		t.Fatal("a valid envelope paste did not preview the sender")
	}
}
