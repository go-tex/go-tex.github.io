// Headless-browser driver for the playground's regex find-and-replace wiring.
//
// It loads the REAL playground app (cmd/playground-wasm) in a real Chrome at
// devicePixelRatio 2 and drives it exactly as a person would — no back door into
// Go state, only real keyboard events + the page's gotex* debug read-outs and the
// canvas pixels:
//
//   1. Seed a document with three "foo" occurrences on three lines.
//   2. Press ⌘F/Ctrl+F -> the find bar opens (gotexFindDebug().visible).
//   3. Type the regex "foo" -> the count reads "1 of 3" and three highlight
//      points are reported.
//   4. Prove HIGHLIGHT PIXELS appear on the matches: the canvas region around
//      each match differs with the query typed vs cleared (band present vs gone).
//   5. Press Enter (next) -> current advances to match 1 AND the strong
//      current-match emphasis moves off match 0 onto match 1 (both regions
//      change colour).
//   6. Prove the layout is unbroken: the canvas backing store is the CSS box ×
//      dpr and the editor pane spans a real width.
//
// A passing run proves the whole app path: DOM key event -> wasm keydown ->
// State.ToggleFindReplace / HandleChar / HandleKeyDown -> FindReplace bar ->
// FindMatches -> CodeEditor match highlights -> canvas. CommonJS so require()
// finds puppeteer-core via NODE_PATH. Env: PAGE_URL, CHROME, SCREENSHOT.
const puppeteer = require("puppeteer-core");

const SEED =
  "\\documentclass{article}\n\\begin{document}\nSection foo alpha.\nAnother foo beta.\nFinal foo gamma.\n\\end{document}\n";

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
    const appDebug = () => page.evaluate(() => globalThis.gotexDebug());
    // Sum every RGBA channel over a small canvas region (device coords) — robust
    // to a glyph sitting inside the band: the translucent match band tints the
    // region's background pixels whether or not a glyph overlaps the centre.
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

    // 1. Seed the document (three "foo" on three lines).
    await page.evaluate((s) => globalThis.gotexSetSource(s), SEED);

    // 2. Open the find bar with a REAL Ctrl+F (⌘F peer).
    let d = await findDebug();
    check(d.visible === false, "find bar starts closed");
    await page.keyboard.down("Control");
    await page.keyboard.press("f");
    await page.keyboard.up("Control");
    await page.waitForFunction(() => globalThis.gotexFindDebug().visible, {
      timeout: 5000,
      polling: 50,
    });
    check(true, "Ctrl+F opened the find bar");

    // 3. Type the regex through real keystrokes; assert the count read-out.
    await page.keyboard.type("foo", { delay: 20 });
    await page.waitForFunction(
      () => globalThis.gotexFindDebug().total === 3,
      { timeout: 5000, polling: 50 },
    );
    d = await findDebug();
    check(d.total === 3, "typing the regex found 3 matches (got " + d.total + ")");
    check(d.current === 0, "current match is the first (got " + d.current + ")");
    check(d.countText === "1 of 3", 'count read-out is "1 of 3" (got "' + d.countText + '")');
    check(
      Array.isArray(d.matchPoints) && d.matchPoints.length === 3,
      "3 highlight points reported (got " + (d.matchPoints && d.matchPoints.length) + ")",
    );
    const pts = d.matchPoints;

    // 4. HIGHLIGHT PIXELS: each match region differs with the query typed vs
    //    cleared (band painted vs gone), proving the highlights land on the code.
    const litSums = [];
    for (const p of pts) litSums.push(await regionSum(p));
    // Clear the query with three real Backspaces -> highlights vanish.
    await page.keyboard.press("Backspace");
    await page.keyboard.press("Backspace");
    await page.keyboard.press("Backspace");
    await page.waitForFunction(
      () => globalThis.gotexFindDebug().total === 0,
      { timeout: 5000, polling: 50 },
    );
    let plainDiffered = true;
    for (let i = 0; i < pts.length; i++) {
      const plain = await regionSum(pts[i]);
      if (plain === litSums[i]) plainDiffered = false;
    }
    check(plainDiffered, "each match region shows highlight pixels (band present only while matched)");

    // Re-type the regex; back to current = 0 with all highlights.
    await page.keyboard.type("foo", { delay: 20 });
    await settle(0);
    const c0Before = await regionSum(pts[0]);
    const c1Before = await regionSum(pts[1]);

    // Screenshot the bar open over the highlighted matches.
    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // 5. Enter = next: current advances AND the strong emphasis moves match0->match1.
    await page.keyboard.press("Enter");
    await settle(1);
    d = await findDebug();
    check(d.current === 1, "Enter advanced to the next match (current " + d.current + ")");
    check(d.countText === "2 of 3", 'count read-out advanced to "2 of 3" (got "' + d.countText + '")');
    const c0After = await regionSum(pts[0]);
    const c1After = await regionSum(pts[1]);
    check(
      c1After !== c1Before,
      "the current-match emphasis moved ONTO match 1 (region changed)",
    );
    check(
      c0After !== c0Before,
      "the current-match emphasis moved OFF match 0 (region changed)",
    );

    // 6. Layout unbroken: backing store is the CSS box × dpr, editor spans width.
    const layout = await page.evaluate(() => {
      const c = document.getElementById("gotex-canvas");
      const b = c.getBoundingClientRect();
      return {
        cw: c.width,
        ch: c.height,
        dpr: c.width / b.width,
        expW: Math.round(b.width * (window.devicePixelRatio || 1)),
        expH: Math.round(b.height * (window.devicePixelRatio || 1)),
      };
    });
    check(
      layout.cw === layout.expW && layout.ch === layout.expH,
      "canvas backing store is the CSS box × dpr (" + layout.cw + "x" + layout.ch + ")",
    );
    const ad = await appDebug();
    check(ad.editorW > 200, "editor pane spans a real width with the bar open (editorW " + ad.editorW + ")");
    const inBounds = pts.every((p) => p[0] >= 0 && p[1] >= 0 && p[0] < layout.cw && p[1] < layout.ch);
    check(inBounds, "all match points fall inside the canvas");

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
