// Headless-browser driver for the playground's RichEditorToolbar wiring.
//
// It loads the REAL playground app (cmd/playground-wasm) in a real Chrome at
// devicePixelRatio 2, then drives it exactly as a person would: seed LaTeX with a
// \section + a paragraph, switch to the WYSIWYG tab, and prove the formatting
// toolbar (a) shows above the RichEditor with its 12 buttons, and (b) actually
// drives the editor when its buttons are CLICKED with a real pointer — Bold makes
// the selection Strong, H2 changes the caret block, a list button wraps it. It
// screenshots the WYSIWYG tab WITH the toolbar, then switches back to Source and
// checks the toolbar is gone and the bold survived the write-back as \textbf.
//
// It talks to the app only through the page's gotex* debug hooks and real mouse
// clicks at the buttons' device rects — no back door into Go state — so a passing
// run proves the whole app path (canvas pointer -> HandleClick -> wysiwygClick ->
// toolbar -> editor verb). CommonJS so require() finds puppeteer-core via
// NODE_PATH. Env: PAGE_URL, CHROME, SCREENSHOT (optional).
const puppeteer = require("puppeteer-core");

const SEED = "\\section{Introduction}\n\nA plain paragraph to embolden.\n";

// toolkit BlockKind ints (richeditor_edit.go): Paragraph=0, H1=1, H2=2, ...
const BLOCK_H2 = 2;
// Button indices in the toolbar's grouped order.
const BTN_BOLD = 0;
const BTN_PARAGRAPH = 4;
const BTN_H2 = 6;
const BTN_BULLET = 10;

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

    const debug = () => page.evaluate(() => globalThis.gotexWysiwygDebug());
    const rects = () => page.evaluate(() => globalThis.gotexRichToolbarRects());

    // Click the centre of a device-pixel rect through a real pointer event: convert
    // device coords to CSS via the canvas backing/CSS ratio (= dpr), which is
    // exactly the mapping the app's own mousedown handler inverts.
    const clickRect = async (r) => {
      const pt = await page.evaluate((rr) => {
        const c = document.getElementById("gotex-canvas");
        const b = c.getBoundingClientRect();
        const dpr = c.width / b.width;
        return { x: b.left + (rr[0] + rr[2] / 2) / dpr, y: b.top + (rr[1] + rr[3] / 2) / dpr };
      }, r);
      await page.mouse.click(pt.x, pt.y);
    };

    // Seed the source and switch to the WYSIWYG tab.
    await page.evaluate((s) => globalThis.gotexSetSource(s), SEED);
    await page.evaluate(() => globalThis.gotexSetEditorTab(1));

    let d = await debug();
    check(d.active === true, "WYSIWYG tab active");
    check(d.toolbarVisible === true, "toolbar visible above the RichEditor");
    check(d.toolbarButtonCount === 12, "toolbar has 12 buttons (got " + d.toolbarButtonCount + ")");
    check(
      Array.isArray(d.toolbarRect) && d.toolbarRect[3] > 0,
      "toolbar strip has positive height (rect " + JSON.stringify(d.toolbarRect) + ")",
    );
    check(d.hasBold === false, "seeded document starts with no bold");

    const br = await rects();
    check(Array.isArray(br) && br.length === 12, "12 button rects exposed (got " + (br && br.length) + ")");
    // Every button rect sits inside the toolbar strip.
    const tr = d.toolbarRect;
    const inStrip = br.every(
      (r) => r[0] >= tr[0] && r[1] >= tr[1] && r[0] + r[2] <= tr[0] + tr[2] && r[1] + r[3] <= tr[1] + tr[3],
    );
    check(inStrip, "all button rects fall inside the toolbar strip");

    // Select the paragraph (block 1; block 0 is the heading) and click Bold.
    await page.evaluate(() => globalThis.gotexRichSelectBlock(1));
    await clickRect(br[BTN_BOLD]);
    d = await debug();
    check(d.hasBold === true, "clicking Bold added a Strong to the richdoc tree");
    check(d.buttonsPressed[BTN_BOLD] === true, "Bold button shows pressed after bolding");

    // Click H2: the caret block becomes a level-2 heading; H2 lights, Paragraph not.
    await clickRect(br[BTN_H2]);
    d = await debug();
    check(d.currentBlockKind === BLOCK_H2, "H2 click changed the caret block to a heading (kind " + d.currentBlockKind + ")");
    check(d.buttonsPressed[BTN_H2] === true, "H2 button pressed");
    check(d.buttonsPressed[BTN_PARAGRAPH] === false, "Paragraph button not pressed while H2 active");

    // Click the bullet-list button: the block is wrapped into a list; Bullet lights.
    await clickRect(br[BTN_BULLET]);
    d = await debug();
    check(d.buttonsPressed[BTN_BULLET] === true, "Bullet button pressed after wrapping in a list");

    // Screenshot the WYSIWYG tab WITH the toolbar visible.
    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // Switch back to Source: toolbar gone, and the new bold round-tripped to LaTeX.
    await page.evaluate(() => globalThis.gotexSetEditorTab(0));
    d = await debug();
    check(d.toolbarVisible === false, "toolbar hidden after returning to Source");
    const src = await page.evaluate(() => globalThis.gotexSource());
    check(src.indexOf("\\textbf") >= 0, "written-back LaTeX source contains \\textbf");

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
