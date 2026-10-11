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

// TestLayoutIsTheSameAtEveryDevicePixelRatio is the real-browser proof of the one
// claim the HiDPI path makes: the canvas backing store is the CSS box × dpr and
// the toolkit renders at that device scale (cmd/playground-wasm/main.go,
// SetupText), so a reader on a Retina panel sees the SAME layout as a reader on a
// 1× panel — crisper, not bigger.
//
// It loads the REAL app twice in the same CSS viewport, at deviceScaleFactor 1 and
// 2, and compares every device-pixel rect the app reports once divided back by the
// ratio, plus the rendered page's own CSS box and a run of the document's text.
//
// Anything that forgets the scale shows up here as a factor of two, which is what
// a reader reports as "the font size is wrong on the Mac screen": glyphs keeping
// their 1× size inside boxes that doubled, or the reverse. A pixel of rounding is
// allowed; a forgotten scale is not a pixel.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestLayoutIsTheSameAtEveryDevicePixelRatio(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the HiDPI layout proof", what)
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

	cmd := exec.Command(node, "browsertest/dpr-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	log := string(out)
	// The premise first: a run that did not really get the two ratios proves
	// nothing about them, and would otherwise pass by comparing 1 with 1.
	if !strings.Contains(log, "PASS the two runs really had ratio 1 and 2") {
		t.Fatalf("the driver did not obtain both device-pixel ratios")
	}
	if !strings.Contains(log, "PASS the app reported some rects to compare") {
		t.Fatalf("no rect was compared: an empty comparison is not a passing one")
	}
	if err != nil {
		t.Fatalf("the HiDPI layout proof failed: %v", err)
	}
	if !strings.Contains(log, "DRIVER_OK") {
		t.Fatalf("the driver did not finish")
	}
}
