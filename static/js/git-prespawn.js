"use strict";
// Starts the playground's git Worker BEFORE the main wasm has finished booting.
//
// Why. The page opens the sample repository as it starts (State.BootClone), and
// every git operation, the restore of the saved copy included, runs in the Worker
// that loads the SEPARATE git-worker.wasm. Until now that Worker was created by Go
// code, so its download began only after playground.wasm had been downloaded,
// compiled and initialised: the two multi-megabyte downloads ran back to back with
// a compile in the gap, where the network sat idle. Calling this the moment the
// main wasm's bytes have arrived puts the worker's download in that gap.
//
// The Go side adopts this Worker instead of creating a second one
// (jsWorkerTransport.Spawn, playground/git_js.go). The Worker may have posted
// messages (download progress, "ready") before Go listens, so every message is kept
// in globalThis.gotexGitWorkerBacklog until Go says it has taken over
// (backlog.adopted), and Go replays them in order.
//
// Idempotent, and a no-op without Worker support or a published worker URL, in
// which case Go creates the Worker itself exactly as before.
globalThis.gotexPrespawnGitWorker = function () {
  if (globalThis.gotexGitWorker) return;
  var url = globalThis.gotexGitWorkerURL;
  if (typeof url !== "string" || url === "" || typeof Worker === "undefined") return;
  var w;
  try {
    w = new Worker(url);
  } catch (e) {
    return; // Go will try again, and report the failure through its own path
  }
  var backlog = [];
  backlog.adopted = false;
  backlog.failed = false;
  w.addEventListener("message", function (ev) {
    if (!backlog.adopted) backlog.push(ev);
  });
  w.addEventListener("error", function () {
    if (!backlog.adopted) backlog.failed = true;
  });
  globalThis.gotexGitWorkerBacklog = backlog;
  globalThis.gotexGitWorker = w;
};
