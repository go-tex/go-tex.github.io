// Headless driver for the playground's off-thread git proof.
//
// It loads the REAL playground in a headless Chrome, opens the Git panel (which
// spawns the Web Worker), asserts the Worker fetched git-worker.wasm — proving the
// remote-git client runs off the main thread from a SEPARATE wasm — then drives
// the panel's real actions against a local CORS git origin: Clone (the seeded
// main.tex must land in the source editor), edit, Commit, Push. It screenshots the
// Git panel, forwards page + worker console, and leaves the verdict on
// globalThis.__result. Exit 0 only if the whole cycle succeeded.
//
// CommonJS so require() finds puppeteer-core through NODE_PATH. Env: PAGE_URL,
// CHROME, SCREENSHOT, REPO_URL (the CORS git origin), MARKER (the edit the native
// witness greps for on the bare origin).
const puppeteer = require("puppeteer-core");

const POLL = 250;
const READY_TIMEOUT = 60000;
const OP_TIMEOUT = 90000;

// ⛔ A FIXED WALL ASKS THE WRONG QUESTION. "Has it been longer than 90 s?" is
// answered YES by a run that is progressing perfectly well on a loaded machine,
// and that is how this proof became flaky: a passing CI run spends 45 of its 90
// seconds, so the margin is 2x for a step that serves a 29.4 MB and a 19.8 MB
// wasm and instantiates TWO Go wasm runtimes in one headless Chrome. Three local
// runs at load average 21-40 all exceeded it; the same code passed in CI.
//
// ⭐ So wait on WORK instead. STALL_TIMEOUT is how long nothing may advance
// before we give up, and the app publishes what "advance" means: the worker's
// wasm download reports bytes, a clone reports objects. HARD_CEILING is the
// absolute bound so a genuinely wedged run cannot hold CI forever.
//
// Raising the wall instead would have measured nothing: a threshold fitted to
// the failure is not a measurement.
const STALL_TIMEOUT = 30000;
const HARD_CEILING = 300000;

// ⛔ AND ONE PHASE PUBLISHES NO MEASUREMENT AT ALL, so no stall may be claimed
// in it. While the worker's wasm is on its way, `assetLoading` names it and
// its measure stays empty -- measured three times here, assetProgress -1 for
// the whole download. No `gotex-asset-progress` message reaches the page at all
// in this harness (a byte count carried alongside the fraction stayed 0 too, so
// it is not merely the missing `?n=` total that the fraction needs), which means
// the longest step of the run is invisible from the page. Worth fixing upstream
// in static/git-worker.js; until then it must not be guessed at.
//
// Treating that silence as a stall is what my first attempt at this did: it
// failed at 30 s, which was WORSE than the 90 s wall it replaced. An unread
// measurement is not a measurement of zero. So this phase gets a bound of its
// own and no stall check -- honest about measuring nothing rather than
// confident about nothing happening.
const OPAQUE_PHASE_BUDGET = 180000;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// liveness is the tuple that says whether anything is still happening. It is
// deliberately coarse: any change in any field counts as progress, because the
// question is "is this run alive", not "how far along is it".
const liveness = (d) =>
  JSON.stringify([d.busy, d.loadedPath, d.error, d.notice, d.statusLine,
                  d.assetLoading, d.assetProgress,
                  d.gitProgress, d.gitProgressLine]);

// waitAlive polls until done(d) holds, and fails only when the liveness tuple
// has not moved for STALL_TIMEOUT. The error names the stall and the state,
// which is the diagnosis a bare "90000ms exceeded" threw away.
async function waitAlive(page, done, what) {
  const started = Date.now();
  let lastMove = started;
  let sig = "";
  for (;;) {
    const d = await page.evaluate(() => globalThis.gotexGitDebug());
    if (done(d)) return d;
    const now = liveness(d);
    if (now !== sig) {
      sig = now;
      lastMove = Date.now();
    }
    const stalled = Date.now() - lastMove;
    const elapsed = Date.now() - started;
    // An asset in flight is the opaque phase: nothing about it is observable
    // from here, so it is bounded but never called stalled.
    const opaque = d.assetLoading !== "";
    if (opaque && elapsed > OPAQUE_PHASE_BUDGET) {
      throw new Error(
        what + ": " + d.assetLoading + " has been loading for " +
        Math.round(elapsed / 1000) + "s and the page reports no progress for it " +
        "(assetProgress " + d.assetProgress +
        "), so this is a bound and not a measurement; state " + now);
    }
    if (!opaque && stalled > STALL_TIMEOUT) {
      throw new Error(
        what + ": no progress for " + Math.round(stalled / 1000) + "s (after " +
        Math.round(elapsed / 1000) + "s); state " + now);
    }
    if (elapsed > HARD_CEILING) {
      throw new Error(
        what + ": still not done after " + Math.round(elapsed / 1000) +
        "s even though it kept moving; state " + now);
    }
    await sleep(POLL);
  }
}

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
  const repoURL = process.env.REPO_URL;
  const marker = process.env.MARKER;
  if (!url || !executablePath || !repoURL || !marker) {
    console.error("DRIVER_FAIL missing PAGE_URL/CHROME/REPO_URL/MARKER");
    process.exit(2);
  }

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });

  let ok = false;
  let detail = "did not finish";
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1200, height: 900, deviceScaleFactor: 2 });
    page.on("console", (m) => console.log("[page] " + m.text()));
    page.on("pageerror", (e) => console.log("[pageerror] " + e.message));
    // ⭐ DEDICATED WORKERS ARE A PAGE-LEVEL EVENT. The browser.on("targetcreated")
    // + t.worker() route below reports `t.worker() returned nothing`, so the
    // worker's console and errors have never once been forwarded -- which is
    // why this test has never said WHY it fails.
    page.on("workercreated", (w) => {
      console.log("[workercreated] " + w.url());
      w.on("console", (m) => console.log("[w] " + m.text()));
    });
    page.on("workerdestroyed", (w) => console.log("[workerdestroyed] " + w.url()));
    // A dedicated worker is its own target; forward its console too.
    browser.on("targetcreated", async (t) => {
      if (t.type() === "worker" || (t.url() && t.url().includes("git-worker.js"))) {
        console.log("[worker target] " + t.url());
        try {
          const w = await t.worker();
          if (w) {
            w.on("console", (m) => console.log("[worker] " + m.text()));
            // An uncaught exception in a Worker is an `error` event, NOT a
            // console message, so a console-only listener sees nothing at all.
            w.on("error", (e) => console.log("[worker-error] " + (e && e.message ? e.message : e)));
          } else {
            console.log("[worker] t.worker() returned nothing");
          }
        } catch (e) {
          // Not best-effort any more: a swallowed failure here is the
          // difference between a diagnosis and ninety seconds of silence.
          console.log("[worker] could not attach to the worker: " + e.message);
        }
      }
    });

    // Record the Worker's request for git-worker.wasm — the load-on-demand proof.
    let workerWasmURL = "";
    page.on("request", (r) => {
      if (r.url().includes("git-worker.wasm")) workerWasmURL = r.url();
    });
    // A REQUEST is not a response. The proof above records that the worker
    // ASKED for its wasm; whether it got 200 and how many bytes is a different
    // fact, and a 404 here would look exactly like a hang from the page's side.
    page.on("response", (r) => {
      const u = r.url();
      if (u.includes("git-worker.wasm") || u.includes("git-worker.js") || r.status() === 404) {
        console.log("[response] " + r.status() + " " + u);
      }
    });
    page.on("requestfailed", (r) => {
      console.log("[requestfailed] " + r.url() + " " + (r.failure() ? r.failure().errorText : ""));
    });

    await page.goto(url, { waitUntil: "load", timeout: 30000 });
    await page.waitForFunction(() => globalThis.gotexPlaygroundReady === true, {
      timeout: READY_TIMEOUT,
      polling: POLL,
    });
    console.log("playground.wasm booted (main thread)");

    // Configure the remote + open the panel; opening spawns the git worker.
    await page.evaluate((repo) => {
      globalThis.gotexGitConfigure(repo, "main", "headless proof", "proof@go-tex.local");
      globalThis.gotexGitOpen(true);
    }, repoURL);

    // The Worker must fetch git-worker.wasm — the separate-binary, on-demand proof.
    // Wait on the RECORDED request, not on a fresh one. The worker is spawned
    // when the app opens its sample repository at boot, which is before this
    // line runs — waitForRequest would sit here waiting for an event that has
    // already happened. The page.on("request") listener above is registered
    // before page.goto, so it catches it whenever it comes.
    for (let i = 0; i < OP_TIMEOUT / 100 && !workerWasmURL; i++) {
      await new Promise((r) => setTimeout(r, 100));
    }
    if (!workerWasmURL) throw new Error("the Web Worker never requested git-worker.wasm");
    console.log("Web Worker requested git-worker.wasm: " + workerWasmURL);

    // Clone → the seeded main.tex must load into the SOURCE editor.
    const debug = () => page.evaluate(() => globalThis.gotexGitDebug());
    const waitIdle = async (what) => {
      const d = await waitAlive(
        page,
        (s) => s.busy === false && (s.loadedPath !== "" || s.error !== "" || s.notice !== ""),
        what,
      );
      if (d.error) throw new Error(what + " reported an error: " + d.error);
      return d;
    };

    // ⭐ WHAT THE STATE WAS BEFORE WE ASKED. If a boot-time operation is still
    // in flight, busy is already true and gotexGitClone() is a no-op (git.go
    // guards on v.busy.Get()), so the wait below would time out on an
    // operation that never started -- indistinguishable from a hung clone.
    console.log("[debug before clone] " + JSON.stringify(await page.evaluate(() => globalThis.gotexGitDebug())));
    await page.evaluate(() => globalThis.gotexGitClone());
    // Wait until the clone is no longer busy AND a file loaded — for as long as
    // it keeps making progress.
    await waitAlive(
      page,
      (d) => d.busy === false && (d.loadedPath !== "" || d.error !== ""),
      "clone",
    );
    let d = await debug();
    if (d.error) throw new Error("clone error: " + d.error);
    if (d.loadedPath !== "main.tex") throw new Error("clone loaded " + d.loadedPath + ", want main.tex");
    const clonedSrc = await page.evaluate(() => globalThis.gotexSource());
    if (!clonedSrc.includes("SEED-TEX")) {
      throw new Error("editor did not receive the seeded .tex: " + clonedSrc.slice(0, 80));
    }
    console.log("CLONE_OK: git-worker cloned over Fetch; main.tex is in the source editor");

    // Screenshot the Git panel now that it shows a cloned, loaded state.
    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // Edit the loaded document, then Commit + Push.
    await page.evaluate((m) => globalThis.gotexSetSource(globalThis.gotexSource() + "\n" + m + "\n"), marker);
    await page.evaluate(() => globalThis.gotexGitCommit());
    d = await waitIdle("commit");
    console.log("COMMIT_OK: " + d.notice);

    await page.evaluate(() => globalThis.gotexGitPush());
    d = await waitIdle("push");
    if (!/pushed/i.test(d.notice)) throw new Error("push notice unexpected: " + d.notice);
    console.log("PUSH_OK: " + d.notice);

    ok = true;
    detail = "off-thread git-worker cloned main.tex into the editor, committed the edit and pushed it to the origin";
  } catch (err) {
    detail = "DRIVER_FAIL " + (err && err.message ? err.message : err);
    console.error(detail);
  } finally {
    try {
      const page = (await browser.pages())[0];
      if (page) await page.evaluate((r) => (globalThis.__result = r), { ok, detail });
    } catch (e) {
      /* ignore */
    }
    console.log("RESULT " + JSON.stringify({ ok, detail }));
    await sleep(50);
    await browser.close();
    process.exitCode = ok ? 0 : 1;
  }
})();
