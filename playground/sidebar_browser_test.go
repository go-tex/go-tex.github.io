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

// TestSidebarLayoutBrowser is the real-browser render proof for the Git workspace
// sidebar. A headless Chrome (devicePixelRatio 2) loads the REAL playground app
// (./cmd/playground-wasm) inside the SAME full-height flex host page the zones
// proof uses — a blue header, then the canvas stage flexing to fill the rest of
// the viewport, exactly like the Hugo playground page — and proves, only through
// the gotexSidebar hook + real canvas pixels:
//
//   - before opening, the canvas fills the viewport height below the header AND
//     its full width (the #46 layout the sidebar must not break);
//   - opening the sidebar reserves a sane left column while the canvas CSS box is
//     unchanged (the column paints INSIDE the canvas) and the editor body shrinks
//     to its right;
//   - the column actually paints a non-background band on the left.
//
// It reuses the browser/puppeteer discovery + wasm-MIME helpers from
// collab_browser_test.go, and the zones host page. It needs a browser, which CI
// does not have, so it skips unless one is found (GOTEX_REQUIRE_BROWSER turns a
// missing browser into a failure).
func TestSidebarLayoutBrowser(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the sidebar layout proof", what)
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

	// Assemble the served directory: the loader, the full-height host page (reused
	// from the zones proof), and the REAL playground app.
	root := t.TempDir()
	copyFile(t, wasmExec, filepath.Join(root, "wasm_exec.js"))
	copyFile(t, "browsertest/zones-index.html", filepath.Join(root, "index.html"))

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
	shot, err := filepath.Abs(filepath.Join(shotDir, "sidebar-layout-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/sidebar-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("sidebar driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless sidebar layout proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"canvas fills the height below the header",
		"canvas fills the full viewport width",
		"sidebar reports open",
		"sidebar column is anchored to the left",
		"sidebar column has a sane device width",
		"sidebar does not swallow the whole canvas",
		"editor body shrank to the right of the column",
		"opening the sidebar did not resize the canvas box",
		"sidebar column painted a non-background band on the left",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
