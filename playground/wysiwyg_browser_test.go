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

// TestWysiwygV02BrowserProof is the real-browser proof of the go-richdoc v0.2.0
// reference nodes in the playground's WYSIWYG mode. A headless Chrome
// (devicePixelRatio 2) loads the REAL playground app (./cmd/playground-wasm)
// compiled to wasm, seeds LaTeX carrying a \footnote, a \ref, a \cite and a
// \section{...}\label{...}, switches to the WYSIWYG tab and proves — through the
// app's own gotex* debug hooks, no back door — that the RichEditor adopted the
// nodes it renders (a superscript footnote marker, two accent crossref runs and a
// heading whose \label folded into its anchor id). It writes a screenshot of the
// WYSIWYG tab showing the footnote marker, then switches back to Source and checks
// every construct round-tripped unchanged.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestWysiwygV02BrowserProof(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser WYSIWYG v0.2 proof", what)
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

	// Assemble the served directory: the loader, the host page (reused from the
	// toolbar proof — it only boots the app), and the REAL playground app.
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
	shot, err := filepath.Abs(filepath.Join(shotDir, "wysiwyg-v02-footnote-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/wysiwyg-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless WYSIWYG v0.2 proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"WYSIWYG tab active",
		"RichEditor holds 1 footnote marker",
		"RichEditor holds 2 crossref runs",
		`heading anchor id folded from \label = "sec:i"`,
		"written-back LaTeX keeps \\footnote{",
		"written-back LaTeX keeps \\label{sec:i}",
		"written-back LaTeX keeps \\ref{sec:i}",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}
