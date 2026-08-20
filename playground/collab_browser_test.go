// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCollabBrowserConvergence is the real-browser proof of the playground's
// live collaborative editing. A headless Chrome loads ./cmd/collab-browsertest
// compiled to wasm, which builds two playground editor sessions in one page,
// hands the WebRTC offer/answer across in process (the copy-paste a person
// does), types LaTeX into the host, and asserts the guest's editor converges on
// the same text AND paints a remote Decoration for the host — over a genuine
// RTCPeerConnection, with no server.
//
// It needs a browser, which CI does not have, so it skips unless one is found.
// GOTEX_REQUIRE_BROWSER turns a missing browser into a failure, for a machine
// meant to have one. Paths are discovered but overridable:
//
//	CHROME                   the Chrome (for Testing) binary
//	GOTEX_BROWSER_NODE_PATH  a node_modules directory holding puppeteer-core
//	GOTEX_SCREENSHOT_DIR     where to write the proof PNG (default: the repo dir)
func TestCollabBrowserConvergence(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the real-browser collaboration proof", what)
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

	// Assemble the directory the page is served from: the loader, the page, and
	// the wasm built from ./cmd/collab-browsertest.
	root := t.TempDir()
	copyFile(t, wasmExec, filepath.Join(root, "wasm_exec.js"))
	copyFile(t, "browsertest/index.html", filepath.Join(root, "index.html"))

	build := exec.Command("go", "build", "-o", filepath.Join(root, "client.wasm"), "./cmd/collab-browsertest")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the browser wasm failed: %v\n%s", err, out)
	}

	srv := httptest.NewServer(wasmMIME(http.FileServer(http.Dir(root))))
	defer srv.Close()

	shotDir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if shotDir == "" {
		shotDir = "."
	}
	shot, err := filepath.Abs(filepath.Join(shotDir, "collab-webrtc-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("browser driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the headless collaboration proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"both peers connected over WebRTC",
		"guest buffer converged",
		"guest shows the host's remote caret",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the browser did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)
}

// wasmMIME serves .wasm as application/wasm, which instantiateStreaming insists
// on and http.FileServer does not always guess.
func wasmMIME(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		next.ServeHTTP(w, r)
	})
}

// locateChrome finds a Chrome (for Testing) binary: the CHROME env if set, else
// the newest one puppeteer has downloaded into its cache.
func locateChrome() (string, error) {
	if env := os.Getenv("CHROME"); env != "" {
		_, err := os.Stat(env)
		return env, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	patterns := []string{
		filepath.Join(home, ".cache/puppeteer/chrome/*/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"),
		filepath.Join(home, ".cache/puppeteer/chrome/*/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"),
		filepath.Join(home, ".cache/puppeteer/chrome/*/chrome-linux64/chrome"),
	}
	return newestMatch(patterns)
}

// locatePuppeteer finds a node_modules with puppeteer-core in it: the override
// env if set, else the one the marp-vscode extension ships.
func locatePuppeteer() (string, error) {
	if env := os.Getenv("GOTEX_BROWSER_NODE_PATH"); env != "" {
		_, err := os.Stat(filepath.Join(env, "puppeteer-core"))
		return env, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, err := newestMatch([]string{
		filepath.Join(home, ".vscode/extensions/marp-team.marp-vscode-*/node_modules/puppeteer-core"),
	})
	if err != nil {
		return "", err
	}
	return filepath.Dir(matches), nil
}

// newestMatch returns the lexically greatest path any pattern matches, which for
// version-stamped directories is the newest.
func newestMatch(patterns []string) (string, error) {
	best := ""
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		for _, m := range matches {
			if m > best {
				best = m
			}
		}
	}
	if best == "" {
		return "", os.ErrNotExist
	}
	return best, nil
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s to %s: %v", src, dst, err)
	}
}
