// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestGitPanelScreenshot renders the REAL Git panel (the toolkit widgets, over
// the live editor + render scene) to an RGBA buffer and encodes it to PNG. It
// always exercises the render+encode path; it writes the file only when
// GOTEX_SCREENSHOT_DIR is set, so a plain `go test` lane never litters the tree.
// Run it with the env set to refresh playground/git-panel.png:
//
//	GOTEX_SCREENSHOT_DIR=. go test -run TestGitPanelScreenshot ./...
func TestGitPanelScreenshot(t *testing.T) {
	const w, h = 1200, 1000
	SetupText(2) // crisp at deviceScaleFactor 2, matching the browser driver
	defer SetupText(1)

	s := NewState(w, h, false)
	s.CompilePending()

	// A fake backend in a freshly cloned, populated state so the panel shows a
	// real status line, a loaded file and a short log.
	f := &fakeGitBackend{
		fileData: map[string]string{"main.tex": string(SampleLaTeX)},
		files:    []string{"README.md", "main.tex", "chapters/intro.tex"},
		statusOK: true,
		status:   gitStatus{Branch: "main", Ahead: 1, Behind: 0, Clean: false, DirtyFile: 1},
		log: []GitCommitInfo{
			{Hash: "9f1c0aa2b3", Subject: "Draft the introduction", Author: "Ada Lovelace"},
			{Hash: "3b7e1d4c55", Subject: "Initial import", Author: "Ada Lovelace"},
		},
	}
	s.git.attach(f, func() {})
	s.SetGitURL("https://sources.mesocentre.plateau-de-saclay.net/ada/thesis.git")
	s.SetGitAuthor("Ada Lovelace")
	s.SetGitEmail("ada@go-tex.local")
	s.SetGitCommitMessage("Revise the introduction")
	s.GitClone(nil) // loads main.tex, sets up the >1-.tex picker + log
	s.SetGitOpen(true)

	buf := make([]byte, w*h*4)
	s.Draw(buf)

	img := &image.RGBA{Pix: buf, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}

	dir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if dir == "" {
		return // render+encode proven above; skip writing on the plain lane
	}
	out := filepath.Join(dir, "git-panel.png")
	fp, err := os.Create(out)
	if err != nil {
		t.Fatalf("create %s: %v", out, err)
	}
	defer func() { _ = fp.Close() }()
	if err := png.Encode(fp, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	t.Logf("wrote Git panel screenshot to %s", out)
}
