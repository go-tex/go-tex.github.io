// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"testing"

	"github.com/go-crdt/collab"
	"github.com/go-crdt/crdt"
)

// A guest may not write as another site.
//
// The servers that use this config are js/wasm only, so this is the lane that
// can ask. What it checks is the wiring: collab tests the policy itself.
func TestARoomRefusesOperationsMadeByAnotherSite(t *testing.T) {
	cfg := roomConfig(collab.NewMemoryStore())
	if cfg.AuthorizeOperations == nil {
		t.Fatal("a room takes any site's operations from any peer")
	}
	made := func(site crdt.SiteID) []crdt.PartOps {
		t.Helper()
		c := crdt.NewComposite(site)
		body, err := c.Text("body")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := body.Insert(0, "typed by somebody"); err != nil {
			t.Fatal(err)
		}
		return c.OpsSince(nil)
	}
	if err := cfg.AuthorizeOperations(t.Context(), "room", 3, made(3)); err != nil {
		t.Errorf("a guest's own operations were refused: %v", err)
	}
	if err := cfg.AuthorizeOperations(t.Context(), "room", 3, made(4)); err == nil {
		t.Error("a guest wrote as another site")
	}
}

// And a bound on what one message may ask this browser to reserve.
//
// Same lane and same reason as the test above: the servers that use this config
// are js/wasm only, so this is where the wiring can be asked about. collab tests
// the bound itself; what is checked here is that a room sets one at all, and
// that the one it sets admits an honest document.
//
// The number is the point rather than the mechanism. Zero is collab's default
// and means unlimited, which on a carrier whose far end is another person's
// browser is the ceiling collab documents for one message: a gibibyte, decoding
// into something no tab survives.
func TestARoomBoundsWhatOneMessageMayReserve(t *testing.T) {
	cfg := roomConfig(collab.NewMemoryStore())
	if cfg.MaxOperations <= 0 {
		t.Fatal("a room lets one message ask for as much as it likes")
	}
	// Far above any honest catch-up: a document holds roughly one operation per
	// character ever typed into it, and a hundred thousand characters is a long
	// paper. A bound that a real document could reach would refuse the session
	// it exists to protect.
	if cfg.MaxOperations < 100_000 {
		t.Errorf("MaxOperations = %d, which an ordinary document would reach", cfg.MaxOperations)
	}
}
