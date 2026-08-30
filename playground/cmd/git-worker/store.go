// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

// The parts of the saved-workspace store that are decisions rather than browser
// calls, kept tagless so they are covered by the native suite. The Cache API
// plumbing that uses them lives in store_js.go.

import (
	"crypto/sha256"
	"encoding/hex"
)

// keptWorkspaces is how many saved repositories are kept. Every distinct
// remote+branch a reader opens leaves a snapshot, and without a bound they
// accumulate for the life of the origin — a browser evicts the whole cache
// under pressure, which would take the workspace the reader is actually using
// along with the ones they have forgotten. Keeping the few most recent bounds
// the store while covering the way the panel is used: one sample repository,
// and whatever the reader has opened lately.
const keptWorkspaces = 4

// workspacePath is the cache key's path for one repository at one branch.
//
// The remote is HASHED rather than embedded. A remote URL can carry a
// credential — a reader who pastes https://user:token@host/repo.git into the
// panel does exactly that — and a key is a stored string that outlives the
// session. Hashing means the secret cannot survive in the store even by
// accident. The branch is part of the hash, so the same repository at two
// branches is two workspaces.
func workspacePath(url, branch string) string {
	sum := sha256.Sum256([]byte(url + "\n" + branch))
	return "/__gotex-workspace/" + hex.EncodeToString(sum[:])
}

// pruneCount is how many of the oldest entries to drop so that at most keep
// remain, given total entries. It never returns a negative count, and a
// non-positive keep is treated as "keep nothing" rather than as a licence to
// delete a negative number of entries.
func pruneCount(total, keep int) int {
	if keep < 0 {
		keep = 0
	}
	if total <= keep {
		return 0
	}
	return total - keep
}
