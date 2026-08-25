// Headless-browser driver for the playground's ZERO-CONFIG "In this browser
// (instant)" collaboration — the BroadcastChannel mode — driven the way a real
// user does it: TWO SEPARATE tabs of the same browser.
//
// Unlike the WebRTC two-tab proof (twotab-driver.cjs), there is NOTHING to carry
// between the tabs: no offer/answer blob, no clipboard, no QR, and no ICE/STUN/
// TURN. Two pages of the same origin share a BroadcastChannel, so the moment both
// click the primary "In this browser (instant)" button they are on one bus, one
// tab is elected host and the other joins it, and the shared document converges.
// This is the definitive proof of the feature AND the reason it is a reliable CI
// test: with no STUN/ICE there is no network path to flake on.
//
// It opens TWO INDEPENDENT puppeteer pages — two page contexts, two Go/wasm
// instances, two real BroadcastChannels on one origin — loads the REAL playground
// app (cmd/playground-wasm) in each, clicks the launcher then "localConnect" with
// genuine pointer events at their device rects, waits for both to report a live
// session, then types into tab A's editor and asserts tab B converges and paints
// A's remote caret, and vice-versa.
//
// Env:
//   PAGE_URL       the page both tabs load
//   CHROME         the Chrome (for Testing) binary
//   SCREENSHOT     path prefix for the two proof PNGs (…-A.png / …-B.png)
//   CONNECT_TIMEOUT_MS  how long to wait for both tabs to connect (default 15000)
// CommonJS so require() finds puppeteer-core through NODE_PATH.
const puppeteer = require("puppeteer-core");

const MARKER_A = "ALPHA";
const MARKER_B = "BRAVO";

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
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

  // Keep BOTH tabs' Go/wasm event loops running while the other is foregrounded.
  // This mode's handshake and message routing run inside each tab's Go runtime
  // (unlike WebRTC, whose ICE runs in the browser's native stack), so a
  // background tab that Chrome throttles to ~1 timer/minute could not answer the
  // other tab's election hello within the 250 ms window, and both would elect
  // themselves host. Disabling the three backgrounding behaviours keeps a
  // non-foreground tab scheduling normally, which is what a real user's two
  // visible windows do anyway.
  const args = [
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--disable-background-timer-throttling",
    "--disable-backgrounding-occluded-windows",
    "--disable-renderer-backgrounding",
  ];
  const browser = await puppeteer.launch({ executablePath, headless: true, args });
  try {
    // Bring up one real tab: load the app and wait for boot. No WebRTC probe and
    // no clipboard grant — this mode needs neither.
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

    // Click the centre of a device-pixel [x,y,w,h] rect with a real pointer, the
    // same device->CSS mapping the app's mousedown handler inverts.
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

    // --- the zero-config handshake across two tabs, through the real UI -------
    // Tab A opens the panel and clicks the primary "In this browser (instant)"
    // button; it is the first tab, so it is elected host and holds the document.
    await A.bringToFront();
    await clickButton(A, "launcher");
    await waitState(A, (s) => s.open, 4000, "A panel open");
    await clickButton(A, "localConnect");
    const aUp = await waitState(A, (s) => s.connected, connectTimeout, "A connected (in-browser)");
    check(aUp.connected, "tab A reached a live in-browser session after clicking 'In this browser (instant)'");

    // Tab B does the same; it finds A already holding the room and joins it.
    await B.bringToFront();
    await clickButton(B, "launcher");
    await waitState(B, (s) => s.open, 4000, "B panel open");
    await clickButton(B, "localConnect");
    const bUp = await waitState(B, (s) => s.connected, connectTimeout, "B connected (in-browser)");
    check(bUp.connected, "tab B joined the same in-browser session with no blob to paste");

    // That both landed on the SAME BroadcastChannel bus (one host, one client) is
    // proven below by convergence and the remote carets crossing between the two —
    // presence (the peer count) only populates once a participant publishes a
    // cursor, i.e. after the first edit, so it is not a pre-edit gate here.
    //
    // Close both panels so the editor + remote carets are unobstructed, and type.
    const typeInto = async (p, marker) => {
      await p.bringToFront();
      await p.keyboard.press("Escape"); // close the modal panel
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

    // A -> B: type into A, B converges and shows A's caret.
    await typeInto(A, MARKER_A);
    let ok = false;
    for (let i = 0; i < 120 && !ok; i++) {
      ok = (await source(B)).includes(MARKER_A);
      if (!ok) await sleep(120);
    }
    check(ok, "tab B converged on tab A's edit (" + JSON.stringify(MARKER_A) + ")");
    let dB = await decos(B);
    check(dB.some((d) => d.label === nameA), "tab B paints tab A's remote caret (" + JSON.stringify(dB) + ")");

    // B -> A: type into B, A converges and shows B's caret.
    await typeInto(B, MARKER_B);
    ok = false;
    for (let i = 0; i < 120 && !ok; i++) {
      ok = (await source(A)).includes(MARKER_B);
      if (!ok) await sleep(120);
    }
    check(ok, "tab A converged on tab B's edit (" + JSON.stringify(MARKER_B) + ")");
    let dA = await decos(A);
    check(dA.some((d) => d.label === nameB), "tab A paints tab B's remote caret (" + JSON.stringify(dA) + ")");

    // Both buffers now hold both markers and agree.
    const sA = await source(A);
    const sB = await source(B);
    check(sA === sB && sA.includes(MARKER_A) && sA.includes(MARKER_B), "both tabs hold identical, fully-merged buffers");

    if (screenshot) {
      for (const [p, suffix] of [[A, "-A.png"], [B, "-B.png"]]) {
        try {
          await p.bringToFront();
          await clickButton(p, "launcher"); // reopen the panel for the peer list in the shot
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
    console.log("RESULT " + JSON.stringify({ ok: allOk, connected: true, fails }));
    process.exitCode = allOk ? 0 : 1;
  } catch (err) {
    console.error("DRIVER_FAIL " + (err && err.stack ? err.stack : err));
    process.exitCode = 2;
  } finally {
    await browser.close();
  }
})();
