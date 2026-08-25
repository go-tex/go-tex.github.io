// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCollabLocalTabSequentialConvergence drives the zero-config "In this browser
// (instant)" mode the way the REPORTING USER did — two side-by-side windows,
// clicking one and then the other SEQUENTIALLY with a human-like gap, WITHOUT
// waiting for the first to report connected. That order lands the second click in
// the host's serve-gap: the window between electing host and answering hellos.
//
// This is the regression guard for the same-browser "both Connected but no sync"
// bug. Under the old election the second tab, clicking into that gap, elected
// ITSELF host — two in-memory documents, both "Connected", neither seeing the
// other's edits. The gap-free election (collab.OpenBroadcastSession: an elected
// host answers from the instant it wins) closes it, so the two converge for any
// click timing. The convergence assertions here are the split-brain detector:
// two hosts on two documents never see each other's edits.
//
// It runs the driver at several gap sizes — a short gap around the serve-gap and
// long human gaps — each an independent two-tab session, and every one must
// converge both ways. It needs a browser (skips otherwise; GOTEX_REQUIRE_BROWSER
// makes a missing one a failure) and no network at all, reusing the browser /
// puppeteer discovery and the wasm-MIME + copyFile helpers from
// collab_browser_test.go.
func TestCollabLocalTabSequentialConvergence(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the sequential in-browser collaboration proof", what)
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

	// A short gap around the serve-gap and two long human gaps: each an independent
	// sequential two-tab session that must converge.
	for _, gap := range []int{300, 900, 1200} {
		t.Run(fmt.Sprintf("gap-%dms", gap), func(t *testing.T) {
			cmd := exec.Command(node, "browsertest/localtab-sequential-driver.cjs")
			cmd.Env = append(os.Environ(),
				"PAGE_URL="+srv.URL+"/index.html",
				"CHROME="+chrome,
				"NODE_PATH="+nodePath,
				fmt.Sprintf("CLICK_GAP_MS=%d", gap),
			)
			out, err := cmd.CombinedOutput()
			t.Logf("sequential two-tab driver (gap %dms) output:\n%s", gap, out)
			if err != nil {
				t.Fatalf("the sequential in-browser collaboration proof failed at gap %dms: %v", gap, err)
			}
			log := string(out)
			for _, want := range []string{
				"both tabs reached a live in-browser session",
				"tab B converged on tab A's edit",
				"tab B paints tab A's remote caret",
				"tab A converged on tab B's edit",
				"tab A paints tab B's remote caret",
				"both tabs hold identical, fully-merged buffers",
				"RESULT ",
				`"ok":true`,
			} {
				if !strings.Contains(log, want) {
					t.Fatalf("the sequential two-tab driver did not report %q at gap %dms in:\n%s", want, gap, out)
				}
			}
		})
	}
}
