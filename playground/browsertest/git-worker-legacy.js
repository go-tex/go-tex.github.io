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
//
// The page also passes `&n=<decompressed bytes>` on that same search string. The
// fetch below is read through a stream so the multi-megabyte download can report
// progress to the page — the workspace sidebar is empty until it lands, and a
// reader deserves to see why. The reader yields DECOMPRESSED bytes while
// Content-Length is the COMPRESSED length, so the bar is driven against `n`;
// without it the fraction is reported as unknown and the bar slides instead of
// filling, which is the honest rendering of "no measurement".
(function () {
  var bust = self.location.search || ""; // "?v=<sha>", or "" on a plain host

  // wasm_exec.js is served from /js/ (static/js/), resolved relative to this
  // worker's URL. It defines the global `Go`.
  importScripts("js/wasm_exec.js");

  // The decompressed size the page measured at build time, for the progress bar.
  var total = 0;
  try {
    total = parseInt(new URLSearchParams(bust).get("n") || "0", 10) || 0;
  } catch (e) { /* no URLSearchParams: the bar simply reports "unknown" */ }

  function report(got) {
    self.postMessage({ t: "gotex-asset-progress", got: got, total: total });
  }

  var go = new Go();
  // NOT {cache:"reload"}: the URL carries the deploy sha, so a cached copy can
  // only ever be the right bytes, and re-downloading megabytes on every visit
  // buys nothing.
  fetch("git-worker.wasm" + bust)
    .then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status + " fetching git-worker.wasm");
      if (!r.body || !r.body.getReader) return r.arrayBuffer(); // no streaming: one shot
      var reader = r.body.getReader(), received = 0, chunks = [];
      report(0);
      return (function pump() {
        return reader.read().then(function (res) {
          if (res.done) {
            var buf = new Uint8Array(received), pos = 0;
            for (var i = 0; i < chunks.length; i++) { buf.set(chunks[i], pos); pos += chunks[i].length; }
            return buf.buffer;
          }
          chunks.push(res.value);
          received += res.value.length;
          report(received);
          return pump();
        });
      })();
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
