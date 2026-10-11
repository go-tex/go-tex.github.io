// Headless-browser driver for the playground's HiDPI geometry: it loads the REAL
// app twice in the SAME CSS viewport, once at deviceScaleFactor 1 and once at 2,
// and asks whether the two agree about the layout once device pixels are divided
// back by the ratio.
//
// That is the whole claim the HiDPI path makes (cmd/playground-wasm/main.go): the
// canvas backing store is the CSS box × dpr, and the toolkit renders at that
// device scale (playground.SetupText), so a reader on a Retina panel sees the
// same layout as a reader on a 1× panel — crisper, not bigger.
//
// Anything that does NOT scale shows up as a rect whose CSS size differs between
// the two runs. A text metric that does not scale is what a reader reports as a
// font-size problem: the glyphs keep their 1× size inside boxes that doubled, or
// the reverse.
//
// It prints one "M <name> <cssW>x<cssH>" line per measured rect per run, plus the
// canvas and viewport geometry, and compares the two runs itself. CommonJS so
// require() finds puppeteer-core via NODE_PATH. Env: PAGE_URL, CHROME.
const puppeteer = require("puppeteer-core");

const SEED =
  "\\documentclass{article}\n\\begin{document}\nUne ligne de texte ordinaire pour mesurer.\n\\end{document}";

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
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

  // measure boots the app at one device-pixel ratio and returns everything in CSS
  // pixels: the device numbers the app reports, divided by the ratio it was given.
  const measure = async (browser, dpr) => {
    const page = await browser.newPage();
    await page.setViewport({ width: 1200, height: 900, deviceScaleFactor: dpr });
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
      return null;
    }
    await page.evaluate((s) => globalThis.gotexSetSource && globalThis.gotexSetSource(s), SEED);
    await new Promise((r) => setTimeout(r, 1500));

    const out = await page.evaluate(() => {
      const c = document.getElementById("gotex-canvas");
      const rects = globalThis.gotexRects ? globalThis.gotexRects() : {};
      const dbg = globalThis.gotexDebug ? globalThis.gotexDebug() : {};
      // The rendered page is DOM SVG over the canvas; its CSS box is what a
      // reader sees, and a text run inside it is the document's own font.
      const svg = document.querySelector("#gotex-pages svg");
      let svgBox = null;
      let textBox = null;
      if (svg) {
        const r = svg.getBoundingClientRect();
        svgBox = [r.width, r.height];
        const t = svg.querySelector("text, tspan, path");
        if (t) {
          const tr = t.getBoundingClientRect();
          textBox = [tr.width, tr.height];
        }
      }
      return {
        ratio: globalThis.devicePixelRatio,
        attr: [c.width, c.height],
        css: [c.clientWidth, c.clientHeight],
        rects,
        editorW: dbg.editorW,
        zoom: dbg.zoomPercent,
        dividerX: dbg.dividerX,
        svgBox,
        textBox,
      };
    });
    await page.close();
    return out;
  };

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });
  try {
    const a = await measure(browser, 1);
    const b = await measure(browser, 2);
    if (!a || !b) return;

    console.log("R1 ratio=" + a.ratio + " attr=" + a.attr + " css=" + a.css);
    console.log("R2 ratio=" + b.ratio + " attr=" + b.attr + " css=" + b.css);
    check(a.ratio === 1 && b.ratio === 2, "the two runs really had ratio 1 and 2");
    // The backing store is the CSS box times the ratio, in both runs. Without
    // this the rest means nothing: it is the premise the app states.
    check(Math.abs(a.attr[0] - a.css[0] * 1) <= 1, "run 1: backing store is the CSS box × 1");
    check(Math.abs(b.attr[0] - b.css[0] * 2) <= 1, "run 2: backing store is the CSS box × 2");
    check(a.css[0] === b.css[0] && a.css[1] === b.css[1], "the CSS box is the same in both runs");

    // Every device-pixel rect the app reports, in CSS pixels. Equal means the
    // layout is ratio-independent, which is the claim.
    const names = Object.keys(a.rects).filter((n) => b.rects[n]).sort();
    check(names.length > 0, "the app reported some rects to compare");
    for (const n of names) {
      const ra = a.rects[n], rb = b.rects[n];
      const aw = ra[2] / 1, ah = ra[3] / 1;
      const bw = rb[2] / 2, bh = rb[3] / 2;
      console.log("M " + n + " r1=" + aw.toFixed(1) + "x" + ah.toFixed(1) +
        " r2=" + bw.toFixed(1) + "x" + bh.toFixed(1));
      // One CSS pixel of rounding is fair; a scale that was forgotten shows as a
      // factor of two, not a pixel.
      check(Math.abs(aw - bw) <= 2 && Math.abs(ah - bh) <= 2, "rect " + n + " is the same size in CSS pixels");
    }
    for (const [n, va, vb] of [["editorW", a.editorW, b.editorW], ["dividerX", a.dividerX, b.dividerX]]) {
      if (va == null || vb == null) continue;
      console.log("M " + n + " r1=" + va + " r2=" + vb / 2);
      check(Math.abs(va - vb / 2) <= 2, n + " is the same in CSS pixels");
    }
    // And the rendered document: its SVG box and a run inside it are CSS boxes,
    // so they must not depend on the ratio either.
    console.log("M zoom r1=" + a.zoom + " r2=" + b.zoom);
    console.log("M svgBox r1=" + a.svgBox + " r2=" + b.svgBox);
    console.log("M textBox r1=" + a.textBox + " r2=" + b.textBox);
    // ⚠ The rendered page carries ONE known residual, so it is bounded rather
    // than pinned: the fit-to-width zoom is computed in DEVICE pixels and
    // quantised to a whole percent, so the quantum is twice as coarse against the
    // logical size at ratio 2 and can land one step away — 30% at ratio 1 against
    // 61% at ratio 2 above, not 60. That is 1.7%, and it is the whole of the
    // difference these two boxes show. Filed separately; the bound here is 3%, so
    // a forgotten SCALE (a factor of two) still fails.
    const near = (x, y, pct) => Math.abs(x - y) <= Math.max(2, (pct / 100) * Math.max(x, y));
    if (a.svgBox && b.svgBox) {
      check(near(a.svgBox[0], b.svgBox[0], 3) && near(a.svgBox[1], b.svgBox[1], 3),
        "the rendered page's CSS box is within the zoom quantum in both runs");
    }
    if (a.textBox && b.textBox) {
      check(near(a.textBox[0], b.textBox[0], 3) && near(a.textBox[1], b.textBox[1], 3),
        "a run of the document's own text is within the zoom quantum in both runs");
    }
  } finally {
    await browser.close();
  }

  if (fails.length) {
    console.log("DRIVER_FAIL " + fails.length + " check(s) failed");
    process.exitCode = 1;
  } else {
    console.log("DRIVER_OK");
  }
})();
