//go:build tools

// Package site exists only so `go mod tidy` can see that this site uses the
// landing kit.
//
// ⛔ A Hugo site has no Go code, so tidy finds nothing that imports the module
// and DELETES the requirement. The fleet's Renovate presets set
// postUpdateOptions: ["gomodTidy"], so every bump would arrive as a pull request
// that silently removes the module — and the grouped dependency branch is
// auto-merged. The build tag keeps this file out of every build; it exists only
// to be seen by tidy. See the kit's README, step 3.
package site

import _ "github.com/go-fleettools/landingkit"
