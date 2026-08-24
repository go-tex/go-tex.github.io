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

// TestCollabTwoTabConvergence is the real user's flow: TWO INDEPENDENT browser
// tabs collaborating. It builds the REAL playground app (./cmd/playground-wasm)
// to wasm, serves it, and runs browsertest/twotab-driver.cjs, which opens two
// separate pages (two page contexts, two Go/wasm instances, two real
// RTCPeerConnections), drives the ACTUAL Collaborate panel with genuine pointer
// clicks (Host / Join / Copy / Paste) and carries the SDP blob between the tabs
// through the real OS clipboard, then proves text and remote carets sync BOTH
// ways — over the app's default (public STUN) ICE configuration, no mDNS-disable
// crutch, i.e. the environment a user actually has.
//
// This is the lane that catches the reported regression: the older one-page
// proof (TestCollabBrowserConvergence) shares one JS context and forces raw host
// candidates, so it never exercised two page contexts nor the empty-ICE +
// mDNS-only path that failed for users.
//
// It needs a browser (skips otherwise; GOTEX_REQUIRE_BROWSER makes a missing one
// a failure) and, for the default public STUN to yield a reflexive candidate,
// network egress to a STUN server. It reuses the browser/puppeteer discovery and
// the wasm-MIME + copyFile helpers from collab_browser_test.go.
//
// Environment caveat: STUN alone connects two peers only when their host or
// server-reflexive candidates can actually reach each other. Two tabs on ONE
// machine that sits behind a symmetric NAT, or whose default route is a
// full-tunnel VPN (a POINTOPOINT utun interface), gather only a host candidate
// that cannot hairpin and a reflexive candidate on a shared public address that
// the NAT will not hairpin either — so the ICE check reaches "checking" and then
// "failed" with no relay to fall back on, and this test's connect step times out.
// That is the network, not the panel: the copy-paste handshake through the panel
// widgets (Host / Copy / Join / Paste / Accept) still completes, and
// TestCollabRealPanelWidgetHandshake proves that path deterministically offline.
// To force a real connection on such a machine, point both tabs at any reachable
// relay via the driver's ICE_SERVERS env, e.g. a loopback TURN server:
// ICE_SERVERS="turn:127.0.0.1:3478|user|pass" — the relay candidate hairpins on
// loopback and the two tabs connect + converge regardless of NAT/VPN.
func TestCollabTwoTabConvergence(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the two-tab collaboration proof", what)
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
	copyFile(t, "browsertest/twotab-index.html", filepath.Join(root, "index.html"))

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
	shot, err := filepath.Abs(filepath.Join(shotDir, "collab-twotab-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	cmd := exec.Command(node, "browsertest/twotab-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
		// Expose raw host candidates. Two tabs on ONE machine sit behind one NAT,
		// so a server-reflexive (STUN) candidate cannot hairpin back to the peer,
		// and a headless Chrome has no resolver for the mDNS ".local" candidates it
		// otherwise hides local IPs behind — so a same-machine pair can only meet on
		// host candidates. This is a headless test accommodation (the one-page proof
		// does the same); the default public-STUN configuration is still in effect
		// and is what lets peers on DIFFERENT networks connect for real users.
		"DISABLE_MDNS=1",
	)
	out, err := cmd.CombinedOutput()
	t.Logf("two-tab driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the two-tab collaboration proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"tab A reached hostWait after clicking Host",
		"Copy invitation put a",
		"tab B reached guestWait after pasting the invitation",
		"connection=connected", // iceConnectionState reached connected (ICE probe log)
		"both independent tabs connected over WebRTC",
		"tab B converged on tab A's edit",
		"tab B paints tab A's remote caret",
		"tab A converged on tab B's edit",
		"tab A paints tab B's remote caret",
		"both tabs hold identical, fully-merged buffers",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the two-tab driver did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshots written to %s-A.png / %s-B.png", strings.TrimSuffix(shot, ".png"), strings.TrimSuffix(shot, ".png"))
}
