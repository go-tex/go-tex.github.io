// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

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

// TestCollabLocalTabConvergence is the definitive proof of the zero-config "In
// this browser (instant)" mode: TWO INDEPENDENT tabs of ONE browser collaborating
// over a BroadcastChannel, with nothing carried between them. It builds the REAL
// playground app (./cmd/playground-wasm) to wasm, serves it, and runs
// browsertest/localtab-driver.cjs, which opens two separate pages (two page
// contexts, two Go/wasm instances, two real BroadcastChannels on one origin),
// clicks the ACTUAL "In this browser (instant)" button in each, then proves text
// and remote carets sync BOTH ways.
//
// Unlike the WebRTC two-tab proof (TestCollabTwoTabConvergence), this needs NO
// STUN/ICE and NO OS clipboard: two same-origin pages share a BroadcastChannel by
// construction, so there is no network path to traverse and nothing to flake on.
// That is exactly why it is a reliable CI-gated browser test and why it works on
// a machine that can form no WebRTC path even to itself (a full-tunnel VPN, a
// strict NAT) — there is no wire to form.
//
// It needs a browser (skips otherwise; GOTEX_REQUIRE_BROWSER makes a missing one
// a failure) and no network at all. It reuses the browser/puppeteer discovery and
// the wasm-MIME + copyFile helpers from collab_browser_test.go.
func TestCollabLocalTabConvergence(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the in-browser collaboration proof", what)
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

	root := t.TempDir()
	copyFile(t, wasmExec, filepath.Join(root, "wasm_exec.js"))
	copyFile(t, "browsertest/localtab-index.html", filepath.Join(root, "index.html"))

	build := exec.Command("go", "build", "-o", filepath.Join(root, "client.wasm"), "./cmd/playground-wasm")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the playground wasm failed: %v\n%s", err, out)
	}

	srv := httptest.NewServer(wasmMIME(http.FileServer(http.Dir(root))))
	defer srv.Close()

	shotDir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if shotDir == "" {
		shotDir = "."
	}
	shot, err := filepath.Abs(filepath.Join(shotDir, "collab-localtab-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/localtab-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("in-browser two-tab driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the in-browser collaboration proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"tab A reached a live in-browser session after clicking 'In this browser (instant)'",
		"tab B joined the same in-browser session with no blob to paste",
		"tab B converged on tab A's edit",
		"tab B paints tab A's remote caret",
		"tab A converged on tab B's edit",
		"tab A paints tab B's remote caret",
		"both tabs hold identical, fully-merged buffers",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the in-browser two-tab driver did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshots written to %s-A.png / %s-B.png", strings.TrimSuffix(shot, ".png"), strings.TrimSuffix(shot, ".png"))
}
