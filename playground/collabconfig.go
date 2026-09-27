// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-crdt/collab"
	"github.com/go-crdt/crdt"
)

// roomConfig is what a shared room's server is built with.
//
// It has no build tag on purpose. The servers that use it are in collab_js.go,
// which only compiles for js/wasm and which the test lanes therefore never
// build; a decision that lives only there is a decision nothing checks.
// maxRoomOperations bounds what ONE message may ask a browser to reserve, in
// operations because bytes are the sender's choice: crdt allocates a flat 80
// bytes an operation, 6.2 times a realistic single-character insert and 20
// times the four-byte floor one can encode in.
//
// A million is about 80 MB. It is chosen to sit far above any honest catch-up --
// a document holds roughly one operation per character ever typed into it, and
// one too large for this is one too large to edit in a browser tab -- because a
// bound a real document could reach would refuse the session it exists to
// protect.
const maxRoomOperations = 1_000_000

func roomConfig(store collab.Store) collab.Config {
	return collab.Config{
		Store: store,
		// A guest speaks for itself. Without this a peer may hand the host
		// operations made by ANOTHER site, and two writers on one site identity
		// make characters that share an ID -- which replicas that saw both
		// resolve differently, silently and for good.
		//
		// collab cannot refuse it by default: a federation link is a session
		// too and legitimately carries other sites' work, and nothing on the
		// wire tells the two apart. A room federates nothing, so here the
		// answer is simple.
		AuthorizeOperations: collab.OwnSiteOnly,
		// And a bound on what ONE message may ask this browser to reserve.
		//
		// The guest is another person. It is not this tab's other window and
		// not a server the same operator runs -- the offer is handed over out
		// of band precisely so somebody else can join -- so what arrives is
		// whatever their browser chose to send.
		//
		// The unit is operations rather than bytes because bytes are the
		// sender's choice: crdt allocates a flat 80 bytes per operation, which
		// is 6.2 times a realistic single-character insert and 20 times the
		// four-byte floor an operation can encode in. A limit written in bytes
		// would be a different limit for every peer.
		//
		// A million of them is about 80 MB, and it is chosen to be far above
		// any honest catch-up: a document has roughly one operation per
		// character ever typed in it, and one too large for this is one too
		// large to edit in a browser tab. Without it the ceiling is the
		// message size collab documents, a gibibyte, which decodes into
		// something no tab survives.
		MaxOperations: maxRoomOperations,
	}
}

// sessionConfig is what every participant in a room joins with, host and guest
// alike.
//
// It exists for the same reason roomConfig does, and it has no build tag for the
// same reason: the joins that use it are in collab_js.go, which only compiles
// for js/wasm and which the test lanes never build, so a decision that lived
// only there would be a decision nothing checks.
//
// The bound is the other half of the one roomConfig sets, and it is the half
// that could not be set until collab v0.71.0. A room is symmetric in who may
// hurt whom: the host parses what the guest sends and the guest parses what the
// host sends, and on this carrier both are somebody's browser. Bounding one
// direction and not the other would leave the session exactly as expensive to
// attack as before, from the other end.
//
// Same number and same arithmetic as roomConfig: crdt allocates a flat 80 bytes
// an operation, so a million of them is about 80 MB, far above any honest
// catch-up and far below what one unbounded message may claim.
func sessionConfig(document string, site crdt.SiteID, resume []byte) collab.ClientConfig {
	return collab.ClientConfig{
		Document:      document,
		Site:          site,
		Resume:        resume,
		MaxOperations: maxRoomOperations,
	}
}
