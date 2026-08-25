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

// TestZonesFullHeightBrowser is the real-browser render proof for the full-height
// canvas and the two moved-in HTML bands (topZone status line + bottomZone
// description/footer with links). A headless Chrome (devicePixelRatio 2) loads the
// REAL playground app (./cmd/playground-wasm) inside a full-height flex page — a
// blue header, then the canvas stage flexing to fill the rest of the viewport,
// exactly like the Hugo playground page — and, only through the gotex* hooks + real
// canvas pixels + a real pointer click, proves: the canvas fills the height below
// the header, the topZone status band (with its ready dot) paints, the bottomZone
// lays out and paints its three links, and clicking a link's device rect navigates
// the browser to that link's url. It writes a screenshot of the full-height app.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestZonesFullHeightBrowser(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the full-height zones proof", what)
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

	// Assemble the served directory: the loader, the full-height host page, and the
	// REAL playground app built from ./cmd/playground-wasm.
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
	shot, err := filepath.Abs(filepath.Join(shotDir, "zones-fullheight-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/zones-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("zones driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless full-height zones proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"canvas sits below the blue header",
		"canvas reaches the viewport bottom",
		"canvas fills the height below the header",
		"topZone status reads 'engine ready'",
		"bottomZone laid out 3 links",
		"a bottomZone link targets https://github.com/go-tex/engine",
		"a bottomZone link targets https://github.com/go-tex/brand",
		"topZone ready dot painted",
		"bottomZone link text painted for https://github.com/go-tex/engine",
		"hovering a bottomZone link draws its underline",
		"moving off the link clears its underline",
		"clicking the go-tex/engine link navigated to it",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
