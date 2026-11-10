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

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
    // A dedicated worker is its own target; forward its console too.
    browser.on("targetcreated", async (t) => {
      if (t.type() === "worker" || (t.url() && t.url().includes("git-worker.js"))) {
        console.log("[worker target] " + t.url());
        try {
          const w = await t.worker();
          if (w) w.on("console", (m) => console.log("[worker] " + m.text()));
        } catch (e) {
          /* worker console is best-effort */
        }
      }
    });

    // Record the Worker's request for git-worker.wasm — the load-on-demand proof.
    let workerWasmURL = "";
    page.on("request", (r) => {
      if (r.url().includes("git-worker.wasm")) workerWasmURL = r.url();
    });

    await page.goto(url, { waitUntil: "load", timeout: 30000 });
    // Same rule as the waits below: a boot that never happens must say so,
    // and distinguish "the wasm failed" from "it is still loading".
    try {
      await page.waitForFunction(() => globalThis.gotexPlaygroundReady === true, {
        timeout: READY_TIMEOUT,
        polling: POLL,
      });
    } catch (e) {
      const boot = await page.evaluate(() => globalThis.__bootError || null);
      throw new Error("the main-thread wasm never became ready after " + READY_TIMEOUT + "ms" +
        (boot ? "; boot error: " + boot : "; no boot error was reported"));
    }
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

    // ⛔ A TIMEOUT MUST SAY WHAT IT WAS WAITING ON. Twice on main — 2026-10-25
    // and 2026-11-01 — this proof failed with nothing but
    //
    //   DRIVER_FAIL Waiting failed: 90000ms exceeded
    //
    // which names neither the step nor the state, so both occurrences are
    // undiagnosable after the fact: the run took 112s and 127s against ~35s
    // healthy, and that is the whole of what is known. The two-tab driver
    // prints `last state={…}` on its timeouts and that is precisely why its
    // stall could be located to one missing event. This does the same.
    const until = async (what, pred) => {
      try {
        await page.waitForFunction(pred, { timeout: OP_TIMEOUT, polling: POLL });
      } catch (e) {
        let last = "unreadable";
        try {
          last = JSON.stringify(await debug());
        } catch (e2) {
          last = "gotexGitDebug() threw: " + (e2 && e2.message ? e2.message : e2);
        }
        throw new Error(
          "timed out waiting for " + what + " after " + OP_TIMEOUT + "ms; last state=" + last +
          "; worker wasm " + (workerWasmURL ? "was requested" : "was NEVER requested"));
      }
    };

    const waitIdle = async (what) => {
      await until(what, () => {
        const d = globalThis.gotexGitDebug();
        return d.busy === false && (d.loadedPath !== "" || d.error !== "" || d.notice !== "");
      });
      const d = await debug();
      if (d.error) throw new Error(what + " reported an error: " + d.error);
      return d;
    };

    await page.evaluate(() => globalThis.gotexGitClone());
    // Wait until the clone is no longer busy AND a file loaded.
    await until("the clone to finish", () => {
      const d = globalThis.gotexGitDebug();
      return d.busy === false && (d.loadedPath !== "" || d.error !== "");
    });
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

    // ⛔ THE SECONDARY CLICK, through the REAL browser gesture. The Go side is
    // covered by native tests that call HandleContextClick directly; those prove
    // the routing and say nothing about whether the page listens for a
    // right-click at all — and it did not, which is why New File / Rename /
    // Delete were keyboard-only. Only a real contextmenu event over the canvas
    // shows the host wiring exists.
    const sbBefore = await page.evaluate(() => globalThis.gotexSidebar());
    const tr = sbBefore.treeRect;
    if (!(tr[2] > 0 && tr[3] > 0)) {
      throw new Error("no file tree to right-click (treeRect " + JSON.stringify(tr) + ")");
    }
    if (sbBefore.menuOpen) throw new Error("the context menu was already open before the click");
    {
      // Device px -> CSS px, the same mapping the app's own handler inverts.
      const pt = await page.evaluate((q) => {
        const c = document.getElementById("gotex-canvas");
        const b = c.getBoundingClientRect();
        const dpr = c.width / b.width;
        return { x: b.left + (q[0] + q[2] / 2) / dpr, y: b.top + (q[1] + 8) / dpr };
      }, tr);
      await page.mouse.click(pt.x, pt.y, { button: "right" });
    }
    const sbAfter = await page.evaluate(() => globalThis.gotexSidebar());
    if (!sbAfter.menuOpen) {
      throw new Error("a real right-click on the file tree did not open the Explorer's menu");
    }
    console.log("CONTEXT_OK: a real right-click opened the workspace Explorer's menu");
    // Leave it closed, so what follows is not typing into an open menu.
    await page.keyboard.press("Escape");

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
