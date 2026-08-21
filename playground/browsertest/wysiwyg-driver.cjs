// Headless-browser driver for the playground's WYSIWYG go-richdoc v0.2.0 nodes.
//
// It loads the REAL playground app (cmd/playground-wasm) in a real Chrome at
// devicePixelRatio 2, then drives it exactly as a person would: seed LaTeX with a
// \section{...}\label{...}, a \footnote and a \ref + \cite, switch to the WYSIWYG
// tab, and prove the RichEditor adopted the v0.2 reference inlines — a Footnote
// (painted as a superscript marker), two CrossRefs (\ref + \cite, painted as
// accent runs) and a Heading whose \label folded into its anchor id. It
// screenshots the WYSIWYG tab WITH the footnote marker, then switches back to
// Source and checks every construct round-tripped unchanged.
//
// It talks to the app only through the page's gotex* debug hooks and real
// tab-switch calls — no back door into Go state. CommonJS so require() finds
// puppeteer-core via NODE_PATH. Env: PAGE_URL, CHROME, SCREENSHOT (optional).
const puppeteer = require("puppeteer-core");

const SEED =
  "\\section{Intro}\\label{sec:i}\n\n" +
  "A paragraph with a note\\footnote{a note} and a reference to \\ref{sec:i} plus a citation \\cite{knuth}.\n";

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

    // Seed the source and switch to the WYSIWYG tab.
    await page.evaluate((s) => globalThis.gotexSetSource(s), SEED);
    await page.evaluate(() => globalThis.gotexSetEditorTab(1));

    let d = await debug();
    check(d.active === true, "WYSIWYG tab active");
    check(d.parseError === "", "no parse error (got " + JSON.stringify(d.parseError) + ")");
    check(d.footnotes === 1, "RichEditor holds 1 footnote marker (got " + d.footnotes + ")");
    check(d.crossRefs === 2, "RichEditor holds 2 crossref runs — \\ref + \\cite (got " + d.crossRefs + ")");
    check(d.firstHeadingID === "sec:i", 'heading anchor id folded from \\label = "sec:i" (got ' + JSON.stringify(d.firstHeadingID) + ")");
    check(d.firstHeading === "Intro", 'first heading text = "Intro" (got ' + JSON.stringify(d.firstHeading) + ")");

    // Screenshot the WYSIWYG tab WITH the footnote marker + crossref runs visible.
    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // Switch back to Source: every v0.2 construct round-tripped into the LaTeX.
    await page.evaluate(() => globalThis.gotexSetEditorTab(0));
    d = await debug();
    check(d.active === false, "back on the Source tab");
    const src = await page.evaluate(() => globalThis.gotexSource());
    check(src.indexOf("\\footnote{") >= 0, "written-back LaTeX keeps \\footnote{");
    check(src.indexOf("\\label{sec:i}") >= 0, "written-back LaTeX keeps \\label{sec:i}");
    check(src.indexOf("\\ref{sec:i}") >= 0, "written-back LaTeX keeps \\ref{sec:i}");
    check(src.indexOf("\\cite{knuth}") >= 0, "written-back LaTeX keeps \\cite{knuth}");

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
