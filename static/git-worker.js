"use strict";
// Minimal Web Worker bootstrap for the playground's off-thread git client.
//
// The main playground.wasm creates this worker on the first Git-panel open with
// `new Worker("git-worker.js?v=<sha>")`. This script loads Go's wasm_exec.js and
// instantiates git-worker.wasm INSIDE the worker, so the go-git-backed handler
// runs off the page's main thread. The go-git dependency lives only in
// git-worker.wasm, never in the base playground download.
//
// The `?v=<sha>` cache-bust on this worker's own URL is inherited by the wasm
// fetch below (via self.location.search), so a deploy that changes the wasm serves
// a new URL for it too — the same anti-stale trick the main page uses. Everything
// is same-origin and self-contained (no external hosts), so it works offline and
// under a strict CSP.
(function () {
  var bust = self.location.search || ""; // "?v=<sha>", or "" on a plain host

  // wasm_exec.js is served from /js/ (static/js/), resolved relative to this
  // worker's URL. It defines the global `Go`.
  importScripts("js/wasm_exec.js");

  var go = new Go();
  fetch("git-worker.wasm" + bust)
    .then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status + " fetching git-worker.wasm");
      return r.arrayBuffer();
    })
    .then(function (buf) {
      return WebAssembly.instantiate(buf, go.importObject);
    })
    .then(function (result) {
      // go.run resolves only when the Go program exits; git-worker runs forever
      // (select {}) serving messages, so we do not await it.
      go.run(result.instance);
    })
    .catch(function (e) {
      // Surface the failure to the page: the main app's onerror gate releases the
      // first Call so it reports a transport error instead of hanging.
      throw e;
    });
})();
