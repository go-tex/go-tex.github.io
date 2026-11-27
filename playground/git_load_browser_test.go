// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package playground_test

// How long the page takes to show the Git panel's content, and what it waits on.
//
// The boot clone is the first request on the git Worker, so the panel's content
// cannot arrive before three things have happened in series: playground.wasm
// downloads and boots, git-worker.wasm downloads and boots, and the clone runs.
// Two things cost time there, and both are fixed:
//
//   - The second download began only after the first had finished BOOTING. The page
//     now starts the Worker as soon as the engine's bytes are in
//     (static/js/git-prespawn.js) and Go adopts it (jsWorkerTransport.Spawn).
//   - The Worker's bootstrap posted one progress message per stream chunk
//     (hundreds), and each made the page repaint its whole canvas on its only
//     thread; the Worker's "ready" waited in the queue behind them, and the first
//     git request with it. static/git-worker.js now reports every 100 ms.
//
// The test serves the real wasm binaries the way Pages does, gzip-compressed and at
// a fixed rate, and the origin with a fixed latency, then runs the page twice: as
// it was (?prespawn=0&legacy=1, the old bootstrap kept as
// browsertest/git-worker-legacy.js) and as it is. The times are LOGGED; what is
// ASSERTED is structural, because wall time on a loaded runner is not:
//
//   - the Worker is created once (adopted, not duplicated);
//   - the new order requests git-worker.wasm earlier than the old;
//   - the page receives under a third of the old number of progress messages;
//   - both end with the repository's main.tex in the panel.
//
// GOTEX_LOAD_RUNS repeats the pair (default 1) for a table; the model is
// loadWireRate and loadLatency below.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// loadWireRate is the modelled download speed, in COMPRESSED bytes per second:
	// 5 MB/s is 40 Mbit/s, an ordinary broadband line.
	loadWireRate = 5 << 20
	// loadLatency is added to every request, the static host's and the git origin's.
	loadLatency = 100 * time.Millisecond
)

type loadResult struct {
	Workers             int    `json:"workers"`
	WorkerWasmRequested int    `json:"workerWasmRequested"`
	WorkerWasmDone      int    `json:"workerWasmDone"`
	MainWasmDone        int    `json:"mainWasmDone"`
	MainDownloaded      int    `json:"mainDownloaded"`
	MainInstantiated    int    `json:"mainInstantiated"`
	MainReady           int    `json:"mainReady"`
	PanelContent        int    `json:"panelContent"`
	LoadedPath          string `json:"loadedPath"`
	Error               string `json:"error"`
	TimeOrigin          int64  `json:"timeOrigin"`
	ProgressMsgs        int    `json:"progressMsgs"`
	MaxGap              int    `json:"maxGap"`
}

func TestGitPanelLoadOrder(t *testing.T) {
	required := os.Getenv("GOTEX_REQUIRE_BROWSER") != ""
	need := func(what, path string, err error) string {
		if err != nil || path == "" {
			if required {
				t.Fatalf("GOTEX_REQUIRE_BROWSER is set but %s is missing: %v", what, err)
			}
			t.Skipf("%s not found; skipping the git panel load measurement", what)
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

	originRoot := t.TempDir()
	seedGitPanelRepo(t, originRoot, "panel", map[string]string{
		"main.tex": "\\documentclass{article}\n% SEED-TEX\n\\begin{document}Hello\\end{document}\n",
	})
	origin := startCORSGitOrigin(t, originRoot)
	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	slowOrigin := httptest.NewServer(withLatency(httputil.NewSingleHostReverseProxy(target)))
	defer slowOrigin.Close()

	served := t.TempDir()
	if err := os.MkdirAll(filepath.Join(served, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, wasmExec, filepath.Join(served, "js", "wasm_exec.js"))
	copyFile(t, "../static/js/git-prespawn.js", filepath.Join(served, "js", "git-prespawn.js"))
	copyFile(t, "browsertest/load-index.html", filepath.Join(served, "load-index.html"))
	copyFile(t, "../static/git-worker.js", filepath.Join(served, "git-worker.js"))
	copyFile(t, "browsertest/git-worker-legacy.js", filepath.Join(served, "git-worker-legacy.js"))
	buildWasm(t, served, "playground.wasm", "./cmd/playground-wasm")
	buildWasm(t, served, "git-worker.wasm", "./cmd/git-worker")

	pages := httptest.NewServer(withLatency(gzipAtRate(t, served, loadWireRate)))
	defer pages.Close()

	runs := 1
	if v, err := strconv.Atoi(os.Getenv("GOTEX_LOAD_RUNS")); err == nil && v > 0 {
		runs = v
	}
	// run loads the page once. old = the order and the bootstrap as they were
	// before this change (Worker created by Go after boot, one progress message per
	// stream chunk); otherwise the page as it is now.
	run := func(old bool) loadResult {
		t.Helper()
		flag := map[bool]int{false: 0, true: 1}
		pageURL := fmt.Sprintf("%s/load-index.html?prespawn=%d&legacy=%d&remote=%s",
			pages.URL, flag[!old], flag[old], url.QueryEscape(slowOrigin.URL+"/panel.git"))
		cmd := exec.Command(node, "browsertest/load-driver.cjs")
		cmd.Env = append(os.Environ(), "PAGE_URL="+pageURL, "CHROME="+chrome, "NODE_PATH="+nodePath)
		out, err := cmd.CombinedOutput()
		var res loadResult
		for _, line := range strings.Split(string(out), "\n") {
			if rest, ok := strings.CutPrefix(line, "LOAD_RESULT "); ok {
				if jerr := json.Unmarshal([]byte(rest), &res); jerr != nil {
					t.Fatalf("unreadable LOAD_RESULT %q: %v", rest, jerr)
				}
			}
		}
		if err != nil {
			t.Fatalf("load driver (old=%v) failed: %v\n%s", old, err, out)
		}
		return res
	}

	t.Logf("model: wire %d MB/s compressed, +%v per request; times in ms since navigation start", loadWireRate>>20, loadLatency)
	t.Logf("%-6s %-9s %-10s %-10s %-10s %-10s %-9s %-9s %-8s", "order", "main-dl", "main-ready", "worker-req", "worker-dl", "panel", "progress", "max-gap", "workers")
	for i := 0; i < runs; i++ {
		// Interleaved: any drift in the machine's load lands on both orders.
		old, now := run(true), run(false)
		for _, c := range []struct {
			name string
			r    loadResult
		}{{"old", old}, {"new", now}} {
			t.Logf("%-6s %-9d %-10d %-10d %-10d %-10d %-9d %-9d %-8d", c.name, c.r.MainDownloaded, c.r.MainReady, c.r.WorkerWasmRequested, c.r.WorkerWasmDone, c.r.PanelContent, c.r.ProgressMsgs, c.r.MaxGap, c.r.Workers)
			if c.r.Workers != 1 {
				t.Errorf("%s: %d Workers were created, want exactly 1 (an adopted Worker must not be duplicated)", c.name, c.r.Workers)
			}
			if c.r.LoadedPath != "main.tex" {
				t.Errorf("%s: the panel loaded %q, want main.tex", c.name, c.r.LoadedPath)
			}
		}
		// The baseline must be the old order, or the comparison below is empty.
		if old.WorkerWasmRequested < old.MainDownloaded {
			t.Errorf("old: git-worker.wasm was requested at %d ms, before the engine finished downloading at %d ms; the baseline is not the old order", old.WorkerWasmRequested, old.MainDownloaded)
		}
		// 1. The Worker's download starts earlier than the old order's.
		if now.WorkerWasmRequested >= old.WorkerWasmRequested {
			t.Errorf("new requested git-worker.wasm at %d ms, no earlier than the old order's %d ms", now.WorkerWasmRequested, old.WorkerWasmRequested)
		}
		// 2. The page is not flooded with progress messages, each of which repaints
		//    the whole canvas on its only thread. 5 MB/s over ~1.2 s is a dozen at
		//    one per 100 ms; allow generous slack, and require a large cut.
		if now.ProgressMsgs > 60 || now.ProgressMsgs*3 > old.ProgressMsgs {
			t.Errorf("progress messages: new %d, old %d; want new <= 60 and under a third of old", now.ProgressMsgs, old.ProgressMsgs)
		}
	}
}

// withLatency delays every request by loadLatency.
func withLatency(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(loadLatency)
		h.ServeHTTP(w, r)
	})
}

// gzipAtRate serves dir the way Pages serves the playground: .wasm gzip-compressed
// (Content-Encoding: gzip, Content-Length the COMPRESSED size) and written no
// faster than rate compressed bytes per second; everything else as-is.
func gzipAtRate(t *testing.T, dir string, rate int) http.Handler {
	t.Helper()
	zipped := map[string][]byte{}
	for _, name := range []string{"playground.wasm", "git-worker.wasm"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		zipped["/"+name] = buf.Bytes()
		t.Logf("%s: %d bytes raw, %d gzip", name, len(raw), buf.Len())
	}
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := zipped[r.URL.Path]
		if !ok {
			wasmMIME(files).ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/wasm")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		const chunk = 64 << 10
		tick := time.Duration(float64(chunk) / float64(rate) * float64(time.Second))
		for len(body) > 0 {
			n := min(chunk, len(body))
			if _, err := w.Write(body[:n]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			body = body[n:]
			time.Sleep(tick)
		}
	})
}
