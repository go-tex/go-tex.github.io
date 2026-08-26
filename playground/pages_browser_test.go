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

// TestRenderIsSearchableInABrowser is the real-browser proof that the rendered
// page is TEXT to the browser — and that gaining that took nothing away.
//
// A headless Chrome (devicePixelRatio 2) loads the REAL playground app compiled
// to wasm, seeds a document and then asks the browser questions only a browser
// can answer: does window.find match a phrase that crosses a space and an ffi
// ligature, does the selection copy back verbatim, do the invisible runs land
// inside the card the canvas drew, and — the regression that would matter most —
// does a click on a glyph still reach the canvas and move the caret to that
// source line.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestRenderIsSearchableInABrowser(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the searchable-render proof", what)
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
	copyFile(t, "browsertest/pages-index.html", filepath.Join(root, "index.html"))

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
	shot, err := filepath.Abs(filepath.Join(shotDir, "searchable-render-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/pages-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the searchable-render proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"find(phrase)           : true",
		`selection copied       : "office of the typesetter"`,
		// The word boundaries an interruption used to swallow, and the formula
		// that used to say nothing at all (engine v0.169.0 through v0.172.0).
		"boundary phrase        : true | The rest mass is E = mc^2 exactly",
		"boundary phrase        : true | E = mc^2",
		"boundary phrase        : true | alpha beta",
		"boundary phrase        : true | every word of it can be found",
		"pointer-events         : none",
		"outside the card: 0",
		"element under a glyph  : canvas",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
