// Headless proof that the playground's rendered page is TEXT to the browser —
// searchable, readable, positioned on its glyphs — and that gaining it took
// nothing away: a click on the render pane still moves the caret to the source
// line it came from.
//
// The verdict is what the browser found, copied and where it put things; never
// a return code.
//
// Env: PAGE_URL, CHROME, NODE_PATH (with puppeteer-core), SCREENSHOT.
const puppeteer = require("puppeteer-core");

const DOC = String.raw`\documentclass{article}
\begin{document}
\section{A searchable page}
The office of the typesetter is to place marks; the office of the document
is to remain text. This paragraph is blitted pixels on a canvas, and every
word of it can be found.

The rest mass is $E = mc^2$ exactly, and a table:
\begin{tabular}{ll}alpha & beta\\ gamma & delta\end{tabular}

A second paragraph, so the click below has a line of its own to land on.
\end{document}`;

const PHRASE = "office of the typesetter"; // crosses spaces AND an ffi ligature

// Phrases that only match once the word boundaries survive an interruption: a
// formula between two words, a line break between two words, and two table
// cells. Each of these read as one glued word before engine v0.169.0.
const BOUNDARY_PHRASES = [
  "The rest mass is E = mc^2 exactly", // across a formula, which speaks its source
  "E = mc^2",                          // the formula itself
  "alpha beta",                        // across two table cells
  "every word of it can be found",     // across a source newline
];

(async () => {
  const browser = await puppeteer.launch({
    executablePath: process.env.CHROME,
    headless: true,
    args: ["--no-sandbox"],
  });
  const page = await browser.newPage();
  await page.setViewport({ width: 1160, height: 900, deviceScaleFactor: 2 });
  page.on("console", (m) => console.log("  page:", m.text()));
  await page.goto(process.env.PAGE_URL, { waitUntil: "networkidle0" });
  await page.waitForFunction("globalThis.gotexPlaygroundReady === true", { timeout: 120000 });

  await page.evaluate((src) => globalThis.gotexSetSource(src), DOC);
  await page.waitForFunction("gotexDebug().drawnPages > 0", { timeout: 60000 });
  // The overlay is written during the frame that follows the compile.
  await page.waitForFunction(
    "document.querySelectorAll('#gotex-pages .pg-page').length > 0",
    { timeout: 30000 },
  );

  const placed = await page.evaluate(() => {
    const pages = [...document.querySelectorAll("#gotex-pages .pg-page")];
    const layer = document.getElementById("gotex-pages");
    const cs = getComputedStyle(layer);
    const box = pages[0].getBoundingClientRect();
    const canvas = document.getElementById("gotex-canvas").getBoundingClientRect();
    return {
      count: pages.length,
      pointerEvents: cs.pointerEvents,
      chars: pages.map((p) => p.textContent.length).reduce((a, b) => a + b, 0),
      insideCanvas:
        box.left >= canvas.left - 1 && box.top >= canvas.top - 1 &&
        box.right <= canvas.right + 1 && box.bottom <= canvas.bottom + 1,
      // the overlay must sit in the RIGHT half: that is where the render pane is
      inRenderPane: box.left > canvas.left + canvas.width / 2 - 1,
    };
  });
  console.log("overlay pages          :", placed.count);
  console.log("readable chars         :", placed.chars);
  console.log("pointer-events         :", placed.pointerEvents);
  console.log("inside the canvas box  :", placed.insideCanvas);
  console.log("in the render pane half:", placed.inRenderPane);

  const found = await page.evaluate(
    (p) => {
      const ok = window.find(p, false, false, true, false, true, false);
      const sel = window.getSelection();
      return { ok, text: sel ? sel.toString() : "" };
    },
    PHRASE,
  );
  console.log("find(phrase)           :", found.ok);
  console.log("selection copied       :", JSON.stringify(found.text));

  const boundaries = await page.evaluate((phrases) => {
    const out = {};
    for (const p of phrases) {
      window.getSelection().removeAllRanges();
      out[p] = window.find(p, false, false, true, false, true, false);
    }
    window.getSelection().removeAllRanges();
    return out;
  }, BOUNDARY_PHRASES);
  for (const [p, ok] of Object.entries(boundaries)) {
    console.log("boundary phrase        :", ok, "|", p);
  }
  const dump = await page.evaluate(() =>
    document.querySelector("#gotex-pages text").textContent.replace(/\s+/g, " ").trim());
  console.log("layer text content     :", JSON.stringify(dump));

  // The overlay must land ON the glyphs, not merely near them: every run has to
  // sit inside the CARD — the <svg> stretched over the rasterised page. The
  // container around it is only the window the pane still shows, so a run below
  // the fold is correctly outside the container and correctly inside the card.
  const aligned = await page.evaluate(() => {
    const runs = [...document.querySelectorAll("#gotex-pages text")];
    if (!runs.length) return null;
    const card = document.querySelector("#gotex-pages svg").getBoundingClientRect();
    let bad = 0;
    for (const r of runs) {
      const b = r.getBoundingClientRect();
      if (b.width <= 0) continue;
      if (b.left < card.left - 2 || b.right > card.right + 2 ||
          b.top < card.top - 2 || b.bottom > card.bottom + 2) bad++;
    }
    return { runs: runs.length, outside: bad, cardW: Math.round(card.width) };
  });
  console.log("text runs              :", aligned.runs, "outside the card:", aligned.outside);
  console.log("card width (CSS px)    :", aligned.cardW);

  // NO REGRESSION: a click on the rendered page must still drive the caret. The
  // overlay is inert to the pointer, so the canvas still receives it.
  const linking = await page.evaluate(async () => {
    const before = gotexDebug().cursorLine;
    const el = document.querySelector("#gotex-pages tspan");
    const b = el.getBoundingClientRect();
    const x = b.left + b.width / 2, y = b.top + b.height / 2;
    const target = document.elementFromPoint(x, y);
    const canvas = document.getElementById("gotex-canvas");
    const rect = canvas.getBoundingClientRect();
    const ev = (type) => canvas.dispatchEvent(new MouseEvent(type, {
      clientX: x, clientY: y, bubbles: true, cancelable: true,
    }));
    ev("mousedown"); ev("mouseup");
    await new Promise((r) => setTimeout(r, 250));
    return {
      hitTest: target ? target.tagName.toLowerCase() : "none",
      before, after: gotexDebug().cursorLine,
      inCanvas: x >= rect.left && x <= rect.right,
    };
  });
  console.log("element under a glyph  :", linking.hitTest, "(must be the canvas)");
  console.log("caret line before/after:", linking.before, "->", linking.after);

  if (process.env.SCREENSHOT) await page.screenshot({ path: process.env.SCREENSHOT });
  await browser.close();

  const ok =
    placed.count === 1 && placed.chars > 200 && placed.pointerEvents === "none" &&
    placed.insideCanvas && placed.inRenderPane &&
    found.ok === true && found.text.includes("office") &&
    Object.values(boundaries).every(Boolean) &&
    aligned.runs > 0 && aligned.outside === 0 &&
    linking.hitTest === "canvas" && linking.after !== linking.before;
  console.log("RESULT " + JSON.stringify({ ok }));
  process.exit(ok ? 0 : 1);
})().catch((e) => { console.error("DRIVER_FAIL", e); process.exit(2); });
