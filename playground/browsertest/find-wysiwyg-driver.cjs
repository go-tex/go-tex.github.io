// Headless-browser driver for the playground's regex find-and-replace MODAL,
// targeting the WYSIWYG editor (the RichEditor).
//
// It loads the REAL playground app (cmd/playground-wasm) in a real Chrome at
// devicePixelRatio 2 and drives it exactly as a person would — no back door into
// Go state, only real keyboard events + the page's gotex* debug read-outs and the
// canvas pixels:
//
//   1. Seed LaTeX whose body parses into two paragraphs holding three "foo"
//      occurrences, then switch to the WYSIWYG tab (gotexSetEditorTab(1)); assert
//      gotexWysiwygDebug().active.
//   2. Press ⌘F/Ctrl+F -> the find MODAL opens over the RichEditor.
//   3. Type the regex "foo" into the modal's input bar -> the count reads
//      "1 of 3" and three highlight points (in the RichEditor) are reported.
//   4. Prove HIGHLIGHT PIXELS appear on the matches (through the modal's dimming
//      scrim): the canvas region around each match differs with the query typed
//      vs cleared (band present vs gone) — the RichEditor's own match bands.
//   5. Press Enter (next) -> current advances to match 1.
//   6. Press Escape -> the modal closes and the RichEditor highlights clear.
//
// A passing run proves the active-editor abstraction reaches the RichEditor: DOM
// key event -> wasm -> the find modal -> FindMatches over BlockTexts ->
// DocSelectionsFromMatches -> RichEditor match highlights -> canvas. CommonJS so
// require() finds puppeteer-core via NODE_PATH. Env: PAGE_URL, CHROME, SCREENSHOT.
const puppeteer = require("puppeteer-core");

// Two body paragraphs — "Alpha foo beta." and "Gamma foo foo delta." — so the
// RichEditor holds three "foo" occurrences across two blocks.
const SEED =
  "\\documentclass{article}\n\\begin{document}\nAlpha foo beta.\n\nGamma foo foo delta.\n\\end{document}\n";

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
  if (!url || !executablePath) {
    console.error("DRIVER_FAIL missing PAGE_URL or CHROME");
    process.exit(2);
  }

  const fails = [];
  const check = (cond, msg) => {
    if (cond) console.log("PASS " + msg);
    else {
      console.log("FAIL " + msg);
      fails.push(msg);
    }
  };

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1200, height: 900, deviceScaleFactor: 2 });
    page.on("console", (m) => console.log("[page] " + m.text()));
    page.on("pageerror", (e) => console.log("[pageerror] " + e.message));

    await page.goto(url, { waitUntil: "load", timeout: 30000 });
    await page.waitForFunction(
      () => globalThis.gotexPlaygroundReady || globalThis.__bootError,
      { timeout: 60000, polling: 100 },
    );
    const boot = await page.evaluate(() => globalThis.__bootError || null);
    if (boot) {
      console.log("DRIVER_FAIL wasm boot error: " + boot);
      process.exitCode = 2;
      return;
    }

    const findDebug = () => page.evaluate(() => globalThis.gotexFindDebug());
    const wysDebug = () => page.evaluate(() => globalThis.gotexWysiwygDebug());
    const regionSum = (pt) =>
      page.evaluate((p) => {
        const c = document.getElementById("gotex-canvas");
        const ctx = c.getContext("2d");
        const x = Math.max(0, p[0] - 3);
        const y = Math.max(0, p[1] - 8);
        const w = Math.min(26, c.width - x);
        const h = Math.min(16, c.height - y);
        const d = ctx.getImageData(x, y, w, h).data;
        let s = 0;
        for (let i = 0; i < d.length; i++) s += d[i];
        return s;
      }, pt);
    const settle = async (want) => {
      await page.waitForFunction(
        (w) => globalThis.gotexFindDebug().current === w,
        { timeout: 5000, polling: 50 },
        want,
      );
    };

    // 1. Seed the LaTeX and switch to the WYSIWYG tab.
    await page.evaluate((s) => globalThis.gotexSetSource(s), SEED);
    await page.evaluate(() => globalThis.gotexSetEditorTab(1));
    await page.waitForFunction(() => globalThis.gotexWysiwygDebug().active, {
      timeout: 5000,
      polling: 50,
    });
    let w = await wysDebug();
    check(w.active === true, "WYSIWYG tab active (parseError " + JSON.stringify(w.parseError) + ")");

    // 2. Open the find modal over the RichEditor.
    let d = await findDebug();
    check(d.visible === false, "find modal starts closed");
    await page.keyboard.down("Control");
    await page.keyboard.press("f");
    await page.keyboard.up("Control");
    await page.waitForFunction(() => globalThis.gotexFindDebug().visible, {
      timeout: 5000,
      polling: 50,
    });
    check(true, "Ctrl+F opened the find modal over the RichEditor");

    // 3. Type the regex; assert the count read-out + highlight points.
    await page.keyboard.type("foo", { delay: 20 });
    await page.waitForFunction(() => globalThis.gotexFindDebug().total === 3, {
      timeout: 5000,
      polling: 50,
    });
    d = await findDebug();
    check(d.total === 3, "typing the regex found 3 matches in the RichEditor (got " + d.total + ")");
    check(d.current === 0, "current match is the first (got " + d.current + ")");
    check(d.countText === "1 of 3", 'count read-out is "1 of 3" (got "' + d.countText + '")');
    check(
      Array.isArray(d.matchPoints) && d.matchPoints.length === 3,
      "3 RichEditor highlight points reported (got " + (d.matchPoints && d.matchPoints.length) + ")",
    );
    const pts = d.matchPoints;

    // 4. HIGHLIGHT PIXELS on the RichEditor: each match region differs with the
    //    query typed vs cleared (band painted vs gone).
    const litSums = [];
    for (const p of pts) litSums.push(await regionSum(p));
    await page.keyboard.press("Backspace");
    await page.keyboard.press("Backspace");
    await page.keyboard.press("Backspace");
    await page.waitForFunction(() => globalThis.gotexFindDebug().total === 0, {
      timeout: 5000,
      polling: 50,
    });
    let plainDiffered = true;
    for (let i = 0; i < pts.length; i++) {
      const plain = await regionSum(pts[i]);
      if (plain === litSums[i]) plainDiffered = false;
    }
    check(plainDiffered, "each RichEditor match region shows highlight pixels (band present only while matched)");

    // Re-type the regex; back to current = 0 with all highlights.
    await page.keyboard.type("foo", { delay: 20 });
    await settle(0);

    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // 5. Enter = next.
    await page.keyboard.press("Enter");
    await settle(1);
    d = await findDebug();
    check(d.current === 1, "Enter advanced to the next match in the RichEditor (current " + d.current + ")");
    check(d.countText === "2 of 3", 'count read-out advanced to "2 of 3" (got "' + d.countText + '")');

    // 6. Escape closes the modal AND clears the RichEditor highlights.
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !globalThis.gotexFindDebug().visible, {
      timeout: 5000,
      polling: 50,
    });
    d = await findDebug();
    check(d.visible === false, "Escape closed the modal");
    check(
      Array.isArray(d.matchPoints) && d.matchPoints.length === 0,
      "closing the modal cleared the RichEditor highlights (0 match points)",
    );

    const ok = fails.length === 0;
    console.log("RESULT " + JSON.stringify({ ok, fails }));
    process.exitCode = ok ? 0 : 1;
  } catch (err) {
    console.error("DRIVER_FAIL " + (err && err.message ? err.message : err));
    process.exitCode = 2;
  } finally {
    await browser.close();
  }
})();
