// Headless driver for the playground's independent per-file edit-buffer proof.
//
// It loads the REAL playground in a headless Chrome, clones a two-file repo over
// the off-thread git worker, then drives the Git WORKSPACE SIDEBAR: open file A,
// type into it, open file B, type into it, and switch back to A. It proves — only
// through the gotexSidebar* hooks + the real editor — that:
//
//   - opening B did NOT discard A's edits: switching back to A restores A's text;
//   - the edits are per-file: A shows A's marker (not B's) after the round-trip;
//   - SEVERAL files are dirty at once — both A and B badge "M" in the tree, and B
//     stays dirty after navigating back to A.
//
// CommonJS so require() finds puppeteer-core through NODE_PATH. Env: PAGE_URL,
// CHROME, SCREENSHOT, REPO_URL (the CORS git origin).
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
  if (!url || !executablePath || !repoURL) {
    console.error("DRIVER_FAIL missing PAGE_URL/CHROME/REPO_URL");
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
    browser.on("targetcreated", async (t) => {
      if (t.type() === "worker" || (t.url() && t.url().includes("git-worker.js"))) {
        try {
          const w = await t.worker();
          if (w) w.on("console", (m) => console.log("[worker] " + m.text()));
        } catch (e) {
          /* worker console is best-effort */
        }
      }
    });

    await page.goto(url, { waitUntil: "load", timeout: 30000 });
    await page.waitForFunction(() => globalThis.gotexPlaygroundReady === true, {
      timeout: READY_TIMEOUT,
      polling: POLL,
    });
    console.log("playground.wasm booted (main thread)");

    // Configure + open the panel (spawns the git worker), then clone.
    await page.evaluate((repo) => {
      globalThis.gotexGitConfigure(repo, "main", "headless proof", "proof@go-tex.local");
      globalThis.gotexGitOpen(true);
    }, repoURL);
    await page.waitForRequest((r) => r.url().includes("git-worker.wasm"), { timeout: OP_TIMEOUT });

    await page.evaluate(() => globalThis.gotexGitClone());
    await page.waitForFunction(
      () => {
        const d = globalThis.gotexGitDebug();
        return d.busy === false && (d.loadedPath !== "" || d.error !== "");
      },
      { timeout: OP_TIMEOUT, polling: POLL },
    );
    let g = await page.evaluate(() => globalThis.gotexGitDebug());
    if (g.error) throw new Error("clone error: " + g.error);
    if (g.loadedPath !== "main.tex") throw new Error("clone loaded " + g.loadedPath + ", want main.tex");
    console.log("CLONE_OK: main.tex is the active, primary file");

    // Close the panel and reveal the workspace sidebar.
    await page.evaluate(() => {
      globalThis.gotexGitOpen(false);
      globalThis.gotexSidebar(true);
    });

    const debug = () => page.evaluate(() => globalThis.gotexSidebarDebug());
    const openFile = (p) => page.evaluate((x) => globalThis.gotexSidebarOpenFile(x), p);

    // --- Open A (main.tex) via the sidebar and type into it. ------------------
    await openFile("main.tex");
    await page.keyboard.type("AAA");
    let d = await debug();
    if (!d.source.includes("AAA")) throw new Error("typing into main.tex did not reach the editor: " + d.source.slice(0, 60));
    if (d.dirty["main.tex"] !== true) throw new Error("main.tex should be dirty after editing");
    if (!d.rows.some((r) => r.includes("main.tex M"))) throw new Error("main.tex not badged M in the tree: " + JSON.stringify(d.rows));
    console.log("EDIT_A_OK: main.tex edited and dirty in the tree");

    // --- Open B (sections/appendix.tex) via the sidebar and type into it. -----
    await openFile("sections/appendix.tex");
    d = await debug();
    if (d.loadedPath !== "sections/appendix.tex") throw new Error("did not switch to appendix.tex, got " + d.loadedPath);
    if (d.source.includes("AAA")) throw new Error("appendix.tex buffer leaked main.tex's edits: " + d.source.slice(0, 60));
    await page.keyboard.type("BBB");
    d = await debug();
    if (!d.source.includes("BBB")) throw new Error("typing into appendix.tex did not reach the editor");
    // BOTH files are dirty at once — the whole point of independent buffers.
    if (d.dirty["main.tex"] !== true) throw new Error("main.tex should STILL be dirty while editing appendix.tex");
    if (d.dirty["sections/appendix.tex"] !== true) throw new Error("appendix.tex should be dirty");
    const bothBadged = d.rows.some((r) => r.includes("main.tex M")) && d.rows.some((r) => r.includes("appendix.tex M"));
    if (!bothBadged) throw new Error("both files should badge M at once: " + JSON.stringify(d.rows));
    console.log("EDIT_B_OK: main.tex AND appendix.tex are dirty at once");

    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // --- Switch back to A → its edits come back; B stays dirty in the tree. ---
    await openFile("main.tex");
    d = await debug();
    if (d.loadedPath !== "main.tex") throw new Error("did not switch back to main.tex, got " + d.loadedPath);
    if (!d.source.includes("AAA")) throw new Error("main.tex's edits were lost on the round-trip: " + d.source.slice(0, 60));
    if (d.source.includes("BBB")) throw new Error("main.tex's buffer leaked appendix.tex's edits: " + d.source.slice(0, 60));
    if (d.dirty["sections/appendix.tex"] !== true) throw new Error("appendix.tex should STAY dirty after switching away");
    if (!d.rows.some((r) => r.includes("appendix.tex M"))) throw new Error("appendix.tex should still badge M in the tree");
    console.log("ROUND_TRIP_OK: switching back to main.tex restored its edits; appendix.tex stayed dirty");

    ok = true;
    detail = "independent per-file edit buffers: A restored after editing B; both dirty at once";
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
