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

// TestToolbarBrowserWiring is the real-browser proof of the RichEditorToolbar
// wiring. A headless Chrome (devicePixelRatio 2) loads the REAL playground app
// (./cmd/playground-wasm) compiled to wasm, seeds LaTeX, switches to the WYSIWYG
// tab and drives the formatting toolbar through genuine pointer clicks — proving
// the whole app path (canvas pointer -> HandleClick -> wysiwygClick -> toolbar ->
// editor verb): Bold adds a Strong, H2 changes the caret block, a list button
// wraps it, the toolbar sits above the RichEditor, and it disappears on Source
// where the bold has round-tripped to \textbf. It writes a screenshot of the
// WYSIWYG tab with the toolbar.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestToolbarBrowserWiring(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser toolbar proof", what)
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
	copyFile(t, "browsertest/toolbar-index.html", filepath.Join(root, "index.html"))

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
	shot, err := filepath.Abs(filepath.Join(shotDir, "richeditor-toolbar-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/toolbar-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless toolbar proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"toolbar visible above the RichEditor",
		"toolbar has 12 buttons",
		"all button rects fall inside the toolbar strip",
		"clicking Bold added a Strong to the richdoc tree",
		"Bold button shows pressed after bolding",
		"H2 click changed the caret block to a heading",
		"Bullet button pressed after wrapping in a list",
		"toolbar hidden after returning to Source",
		"written-back LaTeX source contains \\textbf",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
