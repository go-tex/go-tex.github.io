// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "github.com/go-crdt/collab"

// roomConfig is what a shared room's server is built with.
//
// It has no build tag on purpose. The servers that use it are in collab_js.go,
// which only compiles for js/wasm and which the test lanes therefore never
// build; a decision that lives only there is a decision nothing checks.
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
	}
}
