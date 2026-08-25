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

// runFindBrowserProof builds the REAL playground app (./cmd/playground-wasm) to
// wasm, serves it, runs the given node driver against it in a headless Chrome
// (devicePixelRatio 2), and asserts every wanted substring appears in the driver's
// output. It is the shared harness for the Source and WYSIWYG find/replace modal
// proofs, so both drive the SAME app the same way — only the driver script (and its
// seed + target editor) differs.
//
// The proofs need a browser, which CI's unit lanes do not have, so they SKIP unless
// one is found (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). They
// reuse the browser/puppeteer discovery + wasm-MIME helpers from
// collab_browser_test.go.
func runFindBrowserProof(t *testing.T, label, driver, shotName string, wants []string) {
	t.Helper()
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser %s proof", what, label)
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
	shot, err := filepath.Abs(filepath.Join(shotDir, shotName))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, driver)
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless %s proof failed: %v", label, err)
	}
	log := string(out)
	for _, want := range wants {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}

// TestFindReplaceBrowserWiring is the real-browser proof of the regex
// find-and-replace MODAL over the Source editor. A headless Chrome
// (devicePixelRatio 2) loads the REAL playground app, seeds a document with three
// "foo" occurrences, and drives the feature through genuine keyboard events only:
// Ctrl+F opens the modal, typing "foo" into its input bar runs the regexp, Enter
// steps to the next match, Escape closes it. It asserts the count read-out
// ("1 of 3" → "2 of 3"), proves the match HIGHLIGHTS paint on the code (through the
// modal's dimming scrim) by sampling the canvas (each match region differs with the
// query typed vs cleared), proves the current-match EMPHASIS moves on Enter (both
// match regions change colour), proves the full-size layout is unbroken (backing
// store = CSS box × dpr, editor spans a real width), and proves Escape closes the
// modal and clears the highlights. It writes a screenshot of the modal open over
// the highlights.
func TestFindReplaceBrowserWiring(t *testing.T) {
	runFindBrowserProof(t, "find/replace", "browsertest/find-driver.cjs", "find-replace-proof.png", []string{
		"Ctrl+F opened the find modal",
		"typing the regex found 3 matches",
		`count read-out is "1 of 3"`,
		"3 highlight points reported",
		"each match region shows highlight pixels (band present only while matched)",
		"Enter advanced to the next match",
		`count read-out advanced to "2 of 3"`,
		"the current-match emphasis moved ONTO match 1 (region changed)",
		"the current-match emphasis moved OFF match 0 (region changed)",
		"canvas backing store is the CSS box × dpr",
		"editor pane spans a real width with the modal open",
		"Escape closed the modal",
		"closing the modal cleared the highlights (0 match points)",
		"RESULT ",
		`"ok":true`,
	})
}

// TestFindReplaceWysiwygBrowserWiring is the real-browser proof that the SAME find
// modal drives the WYSIWYG editor (the RichEditor). A headless Chrome
// (devicePixelRatio 2) loads the REAL playground app, seeds LaTeX whose body parses
// into two paragraphs holding three "foo" occurrences, switches to the WYSIWYG tab,
// then drives the modal by keyboard: Ctrl+F opens it over the RichEditor, typing
// "foo" runs the regexp over the block text, Enter steps, Escape closes. It asserts
// the count read-out, proves the RichEditor's match HIGHLIGHTS paint (through the
// scrim) by sampling the canvas, proves Next advances, and proves closing clears
// the RichEditor highlights — exercising the active-editor abstraction end to end.
func TestFindReplaceWysiwygBrowserWiring(t *testing.T) {
	runFindBrowserProof(t, "WYSIWYG find/replace", "browsertest/find-wysiwyg-driver.cjs", "find-replace-wysiwyg-proof.png", []string{
		"WYSIWYG tab active",
		"Ctrl+F opened the find modal over the RichEditor",
		"typing the regex found 3 matches in the RichEditor",
		`count read-out is "1 of 3"`,
		"3 RichEditor highlight points reported",
		"each RichEditor match region shows highlight pixels (band present only while matched)",
		"Enter advanced to the next match in the RichEditor",
		`count read-out advanced to "2 of 3"`,
		"Escape closed the modal",
		"closing the modal cleared the RichEditor highlights (0 match points)",
		"RESULT ",
		`"ok":true`,
	})
}
