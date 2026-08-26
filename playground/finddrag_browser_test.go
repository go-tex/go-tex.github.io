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

// TestFindModalDragInABrowser is the real-browser proof that the find-and-replace
// modal follows the pointer when its title bar is dragged: real mouse events on
// the canvas, and the verdict is where the panel ENDED UP, read back from the app.
//
// The unit test (TestFindModalDragsByItsTitleBar) covers the routing inside the
// app. This one covers the rest of the chain — that the host's DOM listeners
// deliver the moves and the release at all — because the drag was reported
// broken in the real application while every test in the repository passed.
func TestFindModalDragInABrowser(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the modal-drag proof", what)
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
	shot, err := filepath.Abs(filepath.Join(shotDir, "find-modal-drag-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/finddrag-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the modal-drag proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"stays put after release: true",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
