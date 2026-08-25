// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

// The real-browser proof of the independent per-file edit-buffer model. A headless
// Chrome loads the ACTUAL playground.wasm + the off-thread git-worker.wasm, clones
// a two-file repo over the worker, then drives the Git workspace sidebar: open file
// A → type → open file B → type → click A again. It proves, only through the
// gotexSidebar* hooks and the real editor, that A's edits (not B's) come back and
// that BOTH files are dirty ("M") in the tree at once — the regression this feature
// fixes (opening another file used to discard the current file's unsaved edits).
//
// It reuses the two-wasm git harness (seedGitPanelRepo / startCORSGitOrigin /
// buildWasm / the shared browser-discovery helpers) from
// git_two_wasm_browser_test.go + collab_browser_test.go, and the git host page. It
// needs a browser + the js/wasm toolchain + git, which the plain CI lanes lack, so
// it SKIPS unless a browser is found; GOTEX_REQUIRE_BROWSER=1 (set in the
// browser-proofs CI job) turns a missing browser into a failure.
//
//	CHROME                   the Chrome (for Testing) binary
//	GOTEX_BROWSER_NODE_PATH  a node_modules directory holding puppeteer-core
//	GOTEX_SCREENSHOT_DIR     where to write the proof PNG (default: the repo dir)

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSidebarBuffersBrowser(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the per-file edit-buffer proof", what)
		}
		return path
	}

	nodeBin, nodeErr := exec.LookPath("node")
	node := need("node", nodeBin, nodeErr)
	chromeBin, chromeErr := locateChrome()
	chrome := need("a Chrome binary", chromeBin, chromeErr)
	puppeteerDir, puppeteerErr := locatePuppeteer()
	nodePath := need("puppeteer-core", puppeteerDir, puppeteerErr)
	wasmExec := filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(wasmExec); err != nil {
		need("wasm_exec.js", "", err)
	}

	// 1. Seed + serve a CORS-enabled git origin with TWO files the sidebar opens.
	originRoot := t.TempDir()
	seedGitPanelRepo(t, originRoot, "buffers", map[string]string{
		"main.tex":              "\\documentclass{article}\n% MAIN-SEED\n\\begin{document}Hi\\end{document}\n",
		"sections/appendix.tex": "% APPENDIX-SEED\n\\section{Appendix}\n",
	})
	origin := startCORSGitOrigin(t, originRoot)
	repoURL := origin.URL + "/buffers.git"

	// 2. Assemble the served directory: the git host page, the worker bootstrap, the
	//    Go wasm loader, and BOTH freshly-built wasm binaries.
	served := t.TempDir()
	if err := os.MkdirAll(filepath.Join(served, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, wasmExec, filepath.Join(served, "js", "wasm_exec.js"))
	copyFile(t, "browsertest/git-index.html", filepath.Join(served, "git-index.html"))
	copyFile(t, "../static/git-worker.js", filepath.Join(served, "git-worker.js"))

	buildWasm(t, served, "playground.wasm", "./cmd/playground-wasm")
	buildWasm(t, served, "git-worker.wasm", "./cmd/git-worker")

	srv := httptest.NewServer(wasmMIME(http.FileServer(http.Dir(served))))
	defer srv.Close()

	shotDir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if shotDir == "" {
		shotDir = "."
	}
	shot, err := filepath.Abs(filepath.Join(shotDir, "sidebar-buffers-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	// 3. Run the headless driver.
	cmd := exec.Command(node, "browsertest/sidebar-buffers-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/git-index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
		"REPO_URL="+repoURL,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("sidebar-buffers driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the per-file edit-buffer proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"CLONE_OK",
		"EDIT_A_OK",
		"EDIT_B_OK",
		"ROUND_TRIP_OK",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the driver did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
