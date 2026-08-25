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

// TestFindReplaceBrowserWiring is the real-browser proof of the regex
// find-and-replace wiring. A headless Chrome (devicePixelRatio 2) loads the REAL
// playground app (./cmd/playground-wasm) compiled to wasm, seeds a document with
// three "foo" occurrences, and drives the feature through genuine keyboard
// events only: Ctrl+F opens the bar, typing "foo" runs the regexp, Enter steps
// to the next match. It asserts the count read-out ("1 of 3" → "2 of 3"), proves
// the match HIGHLIGHTS paint on the code by sampling the canvas (each match
// region differs with the query typed vs cleared), proves the current-match
// EMPHASIS moves on Enter (both match regions change colour), and proves the
// full-size layout is unbroken (backing store = CSS box × dpr, editor spans a
// real width). It writes a screenshot of the bar open over the highlights.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestFindReplaceBrowserWiring(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser find/replace proof", what)
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

	// Assemble the served directory: the loader, the host page, and the REAL
	// playground app built from ./cmd/playground-wasm.
	root := t.TempDir()
	copyFile(t, wasmExec, filepath.Join(root, "wasm_exec.js"))
	copyFile(t, "browsertest/find-index.html", filepath.Join(root, "index.html"))

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
	shot, err := filepath.Abs(filepath.Join(shotDir, "find-replace-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/find-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless find/replace proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"Ctrl+F opened the find bar",
		"typing the regex found 3 matches",
		`count read-out is "1 of 3"`,
		"3 highlight points reported",
		"each match region shows highlight pixels (band present only while matched)",
		"Enter advanced to the next match",
		`count read-out advanced to "2 of 3"`,
		"the current-match emphasis moved ONTO match 1 (region changed)",
		"the current-match emphasis moved OFF match 0 (region changed)",
		"canvas backing store is the CSS box × dpr",
		"editor pane spans a real width with the bar open",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
