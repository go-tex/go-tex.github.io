// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

// The real two-wasm proof of the off-thread git split. A headless Chrome loads the
// ACTUAL playground.wasm (which contains NO go-git); opening the Git panel spawns a
// Web Worker that loads a SEPARATE git-worker.wasm (which does). The driver then
// drives the panel's real actions against a local CORS git origin — Clone (the
// seeded main.tex must land in the source editor), edit, Commit, Push — and this
// orchestrator witnesses the pushed edit on the bare origin. It proves the whole
// path end-to-end with the production two-binary layout, no infra touched.
//
// It needs a browser + the js/wasm toolchain + git, which CI lacks, so it skips
// unless a browser is found. GOTEX_REQUIRE_BROWSER turns a missing browser into a
// failure. Chrome / puppeteer-core / the screenshot dir are discovered the same way
// as the collaboration proof (see the shared helpers in collab_browser_test.go).
//
//	CHROME                   the Chrome (for Testing) binary
//	GOTEX_BROWSER_NODE_PATH  a node_modules directory holding puppeteer-core
//	GOTEX_SCREENSHOT_DIR     where to write the proof PNG (default: the repo dir)

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gitWorkerMarker is the edit the panel commits + pushes; the native witness greps
// for it on the bare origin to prove the push landed.
const gitWorkerMarker = "% edited by the go-tex Git panel over the off-thread git-worker.wasm"

func TestGitWorkerTwoWasmE2E(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the two-wasm git proof", what)
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

	// 1. Seed + serve a CORS-enabled git origin with a main.tex the panel opens.
	originRoot := t.TempDir()
	seedGitPanelRepo(t, originRoot, "panel", map[string]string{
		"main.tex": "\\documentclass{article}\n% SEED-TEX\n\\begin{document}Hello from the origin\\end{document}\n",
	})
	origin := startCORSGitOrigin(t, originRoot)
	repoURL := origin.URL + "/panel.git"

	// 2. Assemble the served directory: the page, the loader (under js/, where both
	//    the page and the worker load it from), the worker bootstrap, and BOTH
	//    freshly-built wasm binaries.
	served := t.TempDir()
	if err := os.MkdirAll(filepath.Join(served, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, wasmExec, filepath.Join(served, "js", "wasm_exec.js"))
	copyFile(t, "browsertest/git-index.html", filepath.Join(served, "git-index.html"))
	copyFile(t, "../static/git-worker.js", filepath.Join(served, "git-worker.js"))

	buildWasm(t, served, "playground.wasm", "./cmd/playground-wasm")
	buildWasm(t, served, "git-worker.wasm", "./cmd/git-worker")

	srv := httptest.NewServer(wasmMIME(http.FileServer(http.Dir(served))))
	defer srv.Close()

	shotDir := os.Getenv("GOTEX_SCREENSHOT_DIR")
	if shotDir == "" {
		shotDir = "."
	}
	shot, err := filepath.Abs(filepath.Join(shotDir, "git-worker-two-wasm-proof.png"))
	if err != nil {
		t.Fatalf("resolving the screenshot path: %v", err)
	}

	// 3. Run the headless driver.
	cmd := exec.Command(node, "browsertest/git-driver.cjs")
	cmd.Env = append(os.Environ(),
		"PAGE_URL="+srv.URL+"/git-index.html",
		"CHROME="+chrome,
		"NODE_PATH="+nodePath,
		"SCREENSHOT="+shot,
		"REPO_URL="+repoURL,
		"MARKER="+gitWorkerMarker,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("git-worker driver output:\n%s", out)
	if err != nil {
		t.Fatalf("the two-wasm git proof failed: %v", err)
	}
	log := string(out)
	for _, want := range []string{
		"Web Worker requested git-worker.wasm",
		"CLONE_OK",
		"COMMIT_OK",
		"PUSH_OK",
		"RESULT ",
		`"ok":true`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("the driver did not report %q in:\n%s", want, out)
		}
	}
	t.Logf("screenshot written to %s", shot)

	// 4. Witness: the bare origin's main.tex must now carry the panel's edit,
	//    proving Commit + Push (over the off-thread worker's Fetch) actually landed.
	bare := filepath.Join(originRoot, "panel.git")
	show := exec.Command("git", "--git-dir="+bare, "show", "refs/heads/main:main.tex")
	data, err := show.CombinedOutput()
	if err != nil {
		t.Fatalf("witness read of the bare origin failed: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), gitWorkerMarker) {
		t.Fatalf("witness main.tex missing the panel's edit — push did NOT land:\n%s", data)
	}
	t.Log("WITNESS_OK: the Git panel's committed edit is present on the bare origin")
}

// buildWasm compiles pkg to <dir>/<name> for js/wasm.
func buildWasm(t *testing.T, dir, name, pkg string) {
	t.Helper()
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, name), pkg)
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s failed: %v\n%s", name, err, out)
	}
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
