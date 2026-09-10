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

// TestDeadKeyBrowserWiring is the real-browser proof that an accent can be
// typed.
//
// A dead key — the ^ of a French keyboard, the ¨ of a German one — does not
// arrive as a keydown carrying its character. The browser reports a COMPOSITION,
// and it starts one only when something EDITABLE holds the focus. A canvas app
// has nothing editable, so the app keeps a transparent one-pixel textarea
// focused for exactly this; without it the ^ is dropped and the space that
// usually forces a bare one arrives as a space.
//
// The driver does not fake the events: it drives Chrome's own input-method path
// over CDP (Input.imeSetComposition then Input.insertText), the same road a
// macOS dead key and a CJK candidate take. On a build with no focused editable
// element, nothing reaches the app and the proof fails.
//
// It needs a browser, which CI does not have, so it skips unless one is found
// (GOTEX_REQUIRE_BROWSER turns a missing browser into a failure). It reuses the
// browser/puppeteer discovery + wasm-MIME helpers from collab_browser_test.go.
func TestDeadKeyBrowserWiring(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser dead-key proof", what)
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
	copyFile(t, "browsertest/deadkey-index.html", filepath.Join(root, "index.html"))
	build := exec.Command("go", "build", "-o", filepath.Join(root, "client.wasm"), "./cmd/playground-wasm")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the playground wasm failed: %v\n%s", err, out)
	}

	srv := httptest.NewServer(wasmMIME(http.FileServer(http.Dir(root))))
	defer srv.Close()

	cmd := exec.Command(node, "browsertest/deadkey-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless dead-key proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"the page holds an element a composition can happen in",
		"and it has the keyboard focus",
		"a dead-key circumflex reached the document",
		"a composition in flight leaves the document alone",
		"committing it replaces the preview with the letter",
		"a multi-character commit lands whole",
		"i, then a dead key, then 2 gives i^2",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
}
