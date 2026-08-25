// Headless-browser driver for the playground's ZERO-CONFIG "In this browser
// (instant)" collaboration, driven the way the REPORTING USER did it: two
// side-by-side visible windows, clicking "In this browser (instant)" in one and
// then the other SEQUENTIALLY with a human-like gap — NOT the ~simultaneous,
// wait-until-A-is-connected order the first proof used.
//
// The difference matters. The old proof clicked tab B only after tab A reported
// a live session, so B always met A already answering. A real user clicks the
// second window while the first is still electing/serving — landing in the
// serve-gap, the window in which an elected-but-not-yet-serving host is deaf. A
// second tab clicking then used to elect ITSELF host: two hosts, two in-memory
// documents, both "Connected", and no sync. This driver clicks B a configurable
// CLICK_GAP_MS after A WITHOUT waiting for A to connect, so it exercises exactly
// that gap, then proves the two tabs converge both ways (which a split-brain
// cannot).
//
// Env:
//   PAGE_URL       the page both tabs load
//   CHROME         the Chrome (for Testing) binary
//   CLICK_GAP_MS   how long after A's click to click B (default 300)
//   SCREENSHOT     optional path prefix for two proof PNGs (…-A.png / …-B.png)
//   CONNECT_TIMEOUT_MS  how long to wait for both tabs to connect (default 15000)
// CommonJS so require() finds puppeteer-core through NODE_PATH.
const puppeteer = require("puppeteer-core");

const MARKER_A = "ALPHA";
const MARKER_B = "BRAVO";

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
  const clickGap = parseInt(process.env.CLICK_GAP_MS || "300", 10);
  const connectTimeout = parseInt(process.env.CONNECT_TIMEOUT_MS || "15000", 10);
  if (!url || !executablePath) {
    console.error("DRIVER_FAIL missing PAGE_URL or CHROME");
    process.exit(2);
  }

  const fails = [];
  const check = (cond, msg) => {
    console.log((cond ? "PASS " : "FAIL ") + msg);
    if (!cond) fails.push(msg);
  };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // Two side-by-side VISIBLE windows schedule normally: a non-focused but visible
  // (non-occluded) window is not renderer-backgrounded, so these flags reproduce a
  // real user's two visible windows rather than paper over throttling. They do NOT
  // hide the serve-gap — that is a logic race in the election, exercised here by
  // the click TIMING, not by scheduling.
  const args = [
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--disable-backgrounding-occluded-windows",
    "--disable-renderer-backgrounding",
  ];
  const browser = await puppeteer.launch({ executablePath, headless: true, args });
  try {
    const openTab = async (tag) => {
      const page = await browser.newPage();
      await page.setViewport({ width: 1200, height: 860, deviceScaleFactor: 2 });
      page.on("console", (m) => console.log("[" + tag + "] " + m.text()));
      page.on("pageerror", (e) => console.log("[" + tag + " pageerror] " + e.message));
      await page.goto(url, { waitUntil: "load", timeout: 30000 });
      await page.waitForFunction(
        () => globalThis.gotexPlaygroundReady || globalThis.__bootError,
        { timeout: 60000, polling: 100 },
      );
      const boot = await page.evaluate(() => globalThis.__bootError || null);
      if (boot) throw new Error(tag + " wasm boot error: " + boot);
      return page;
    };

    const A = await openTab("A");
    const B = await openTab("B");

    const state = (p) => p.evaluate(() => globalThis.gotexCollabState());
    const rects = (p) => p.evaluate(() => globalThis.gotexCollabRects());
    const clickRect = async (p, r) => {
      const pt = await p.evaluate((rr) => {
        const c = document.getElementById("gotex-canvas");
        const b = c.getBoundingClientRect();
        const dpr = c.width / b.width;
        return { x: b.left + (rr[0] + rr[2] / 2) / dpr, y: b.top + (rr[1] + rr[3] / 2) / dpr };
      }, r);
      await p.mouse.click(pt.x, pt.y);
    };
    const clickButton = async (p, name) => {
      const r = await rects(p);
      if (!r[name]) throw new Error("no collab button " + name + " (have " + Object.keys(r) + ")");
      await clickRect(p, r[name]);
    };
    const openPanelAndConnect = async (p) => {
      await clickButton(p, "launcher");
      const deadline = Date.now() + 4000;
      while (Date.now() < deadline && !(await state(p)).open) await sleep(60);
      await clickButton(p, "localConnect");
    };
    const waitState = async (p, pred, ms, label) => {
      const deadline = Date.now() + ms;
      let last;
      while (Date.now() < deadline) {
        last = await state(p);
        if (pred(last)) return last;
        await sleep(120);
      }
      throw new Error("timeout waiting for " + label + "; last state=" + JSON.stringify(last));
    };

    // --- the sequential, human-timed clicks ----------------------------------
    // Tab A clicks first. Then, WITHOUT waiting for A to report connected — a real
    // user does not — tab B is brought to front and clicks after a human gap,
    // landing in A's serve-gap.
    await A.bringToFront();
    await openPanelAndConnect(A);

    await sleep(clickGap);

    await B.bringToFront();
    await openPanelAndConnect(B);

    // Both must reach a live session. Under the old split-brain each tab believed
    // itself a lone host and would also report connected — so "connected" alone is
    // not the proof; convergence below is.
    const aUp = await waitState(A, (s) => s.connected, connectTimeout, "A connected");
    const bUp = await waitState(B, (s) => s.connected, connectTimeout, "B connected");
    check(aUp.connected && bUp.connected, "both tabs reached a live in-browser session (gap " + clickGap + "ms)");

    const typeInto = async (p, marker) => {
      await p.bringToFront();
      await p.keyboard.press("Escape");
      await waitState(p, (s) => !s.open, 3000, "panel closed");
      const caret = await p.evaluate(() => globalThis.gotexCaretPixel(0, 0));
      const b = await p.evaluate(() => {
        const c = document.getElementById("gotex-canvas");
        const r = c.getBoundingClientRect();
        return { left: r.left, top: r.top, dpr: c.width / r.width };
      });
      await p.mouse.click(b.left + caret[0] / b.dpr, b.top + caret[1] / b.dpr);
      for (const ch of marker) await p.keyboard.type(ch);
    };
    const source = (p) => p.evaluate(() => globalThis.gotexSource());
    const decos = (p) => p.evaluate(() => globalThis.gotexCollabState().decorations);

    const nameA = (await state(A)).name;
    const nameB = (await state(B)).name;
    console.log("tab A is " + JSON.stringify(nameA) + ", tab B is " + JSON.stringify(nameB));

    // A -> B convergence: the decisive split-brain detector. Two hosts on two
    // documents never see each other's edits.
    await typeInto(A, MARKER_A);
    let ok = false;
    for (let i = 0; i < 150 && !ok; i++) {
      ok = (await source(B)).includes(MARKER_A);
      if (!ok) await sleep(120);
    }
    check(ok, "tab B converged on tab A's edit (" + JSON.stringify(MARKER_A) + ") — one shared document, not two");
    const dB = await decos(B);
    check(dB.some((d) => d.label === nameA), "tab B paints tab A's remote caret");

    // B -> A convergence.
    await typeInto(B, MARKER_B);
    ok = false;
    for (let i = 0; i < 150 && !ok; i++) {
      ok = (await source(A)).includes(MARKER_B);
      if (!ok) await sleep(120);
    }
    check(ok, "tab A converged on tab B's edit (" + JSON.stringify(MARKER_B) + ")");
    const dA = await decos(A);
    check(dA.some((d) => d.label === nameB), "tab A paints tab B's remote caret");

    const sA = await source(A);
    const sB = await source(B);
    check(sA === sB && sA.includes(MARKER_A) && sA.includes(MARKER_B), "both tabs hold identical, fully-merged buffers");

    if (screenshot) {
      for (const [p, suffix] of [[A, "-A.png"], [B, "-B.png"]]) {
        try {
          await p.bringToFront();
          await clickButton(p, "launcher");
          await sleep(200);
          const path = screenshot.replace(/\.png$/, "") + suffix;
          await p.screenshot({ path });
          console.log("SCREENSHOT " + path);
        } catch (e) {
          console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
        }
      }
    }

    const allOk = fails.length === 0;
    console.log("RESULT " + JSON.stringify({ ok: allOk, gap: clickGap, fails }));
    process.exitCode = allOk ? 0 : 1;
  } catch (err) {
    console.error("DRIVER_FAIL " + (err && err.stack ? err.stack : err));
    process.exitCode = 2;
  } finally {
    await browser.close();
  }
})();
