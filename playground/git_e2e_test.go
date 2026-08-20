// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

// Deterministic headless e2e that proves the whole Git-panel → browsergit →
// browser-Fetch → push path end-to-end, WITHOUT any production infra. It is
// opt-in (GOTEX_GIT_WASM_E2E=1) because it needs a Node runtime + the js/wasm
// toolchain + git; the default `go test` lane runs the native panel unit tests
// (100% coverage via the fake backend). When enabled it:
//
//  1. seeds + serves a CORS-enabled git origin (git-http-backend) with a
//     main.tex holding a SEED-TEX marker,
//  2. compiles the playground package's js cycle test to a wasm binary,
//  3. runs it under Node through the argv0 spoof from the browsergit study, so
//     Go's net/http takes the REAL WHATWG Fetch RoundTripper (the browser path),
//     driving the REAL Git panel: Clone → assert the .tex loaded into the source
//     editor → edit → Commit → Push,
//  4. witnesses the pushed commit by re-cloning the bare repo with a native
//     browsergit client and asserting the editor's edit landed on the origin.

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-tex/go-tex.github.io/playground/internal/browsergit"
)

// argv0 spoof: force Go's js/wasm net/http onto the real Fetch RoundTripper by
// wrapping process so its argv0 is not "node" (the browsergit study's technique).
const gitPanelRunFetchJS = `const real = process;
const wrapper = Object.create(Object.getPrototypeOf(real));
for (const k of Reflect.ownKeys(real)) {
  if (k === 'argv0') continue;
  Object.defineProperty(wrapper, k, Object.getOwnPropertyDescriptor(real, k));
}
Object.defineProperty(wrapper, 'argv0', { value: 'gitpanelproof', enumerable: true, configurable: true });
globalThis.process = wrapper;
require(process.env.WASM_EXEC);
`

func TestGitPanelWasmE2E(t *testing.T) {
	if os.Getenv("GOTEX_GIT_WASM_E2E") == "" {
		t.Skip("set GOTEX_GIT_WASM_E2E=1 to run the Node-Fetch Git-panel e2e (needs node + js/wasm toolchain + git)")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}
	wasmExec := filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec_node.js")
	if _, err := os.Stat(wasmExec); err != nil {
		t.Skipf("wasm_exec_node.js not found at %s: %v", wasmExec, err)
	}

	// 1. Seed + serve a CORS-enabled origin with a main.tex the panel will open.
	root := t.TempDir()
	seedGitPanelRepo(t, root, "panel", map[string]string{
		"main.tex": "\\documentclass{article}\n% SEED-TEX\n\\begin{document}Hello\\end{document}\n",
	})
	srv := startCORSGitOrigin(t, root)
	repoURL := srv.URL + "/panel.git"

	// 2. Compile the playground package's js cycle test to a wasm binary.
	tmp := t.TempDir()
	wasmBin := filepath.Join(tmp, "playground.test.wasm")
	build := exec.Command("go", "test", "-c", "-o", wasmBin, ".")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wasm test: %v\n%s", err, out)
	}

	// 3. Write the spoof shim + run the wasm cycle under Node.
	shim := filepath.Join(tmp, "run_fetch.js")
	if err := os.WriteFile(shim, []byte(gitPanelRunFetchJS), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(node, shim, wasmBin, "-test.run", "TestGitPanelWasmCycle", "-test.v")
	run.Env = append(os.Environ(),
		"WASM_EXEC="+wasmExec,
		"GITPANEL_REPO_URL="+repoURL,
	)
	out, err := run.CombinedOutput()
	t.Logf("=== wasm/node output ===\n%s", out)
	if err != nil {
		t.Fatalf("node wasm run failed: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"CLONE_OK (Fetch RoundTripper)",
		"COMMIT_OK",
		"GIT_PANEL_WASM_CYCLE_OK",
		"PASS",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wasm cycle did not report %q; output above", want)
		}
	}

	// 4. Witness: a fresh native clone must see the editor's edit that the panel
	// committed + pushed over Fetch.
	c := browsergit.New(browsergit.Options{BaseURL: repoURL})
	witness, err := c.Clone(context.Background(), "", "main", 1)
	if err != nil {
		t.Fatalf("witness clone: %v", err)
	}
	data, err := witness.ReadFile("main.tex")
	if err != nil {
		t.Fatalf("witness read main.tex: %v", err)
	}
	const marker = "% edited by the Git panel over the browser Fetch RoundTripper"
	if !strings.Contains(string(data), marker) {
		t.Fatalf("witness main.tex missing the panel's edit — push did NOT land:\n%s", data)
	}
	t.Log("WITNESS_OK: the Git panel's committed edit is present in the bare origin")
}

// --- a test-only CORS smart-HTTP git origin (mirrors browsergit's) -----------

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=seed", "GIT_AUTHOR_EMAIL=seed@test",
		"GIT_COMMITTER_NAME=seed", "GIT_COMMITTER_EMAIL=seed@test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// seedGitPanelRepo creates <root>/<name>.git as a push-enabled bare repo seeded
// with one commit on main containing the given files.
func seedGitPanelRepo(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	bare := filepath.Join(root, name+".git")
	runGit(t, root, "init", "--bare", "-b", "main", bare)
	runGit(t, bare, "config", "http.receivepack", "true")

	work := filepath.Join(root, name+"-seed")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "init", "-b", "main")
	for p, c := range files {
		full := filepath.Join(work, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")
	runGit(t, work, "remote", "add", "origin", bare)
	runGit(t, work, "push", "origin", "main")
}

// startCORSGitOrigin serves everything under root via git-http-backend with the
// permissive CORS headers a sovereign proxy / CORS-enabled Forgejo would add.
func startCORSGitOrigin(t *testing.T, root string) *httptest.Server {
	t.Helper()
	backend := gitBackendPath(t)
	h := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Git-Protocol,Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type,Content-Length")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// gitBackendPath locates git-http-backend, skipping the test if git is absent.
func gitBackendPath(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("git-http-backend"); err == nil {
		return p
	}
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("git-http-backend not found at %s: %v", p, err)
	}
	return p
}
