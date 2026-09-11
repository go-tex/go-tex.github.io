// Headless-browser driver for typing an accent — the thing a French keyboard
// cannot do without it.
//
// A dead key does not arrive as a keydown carrying its character: the browser
// reports a COMPOSITION (compositionstart / update / end), and it only starts one
// when something EDITABLE has the focus. A canvas app has nothing editable, so
// the playground kept a transparent one-pixel textarea focused for exactly this.
//
// The driver does not fake those events. It drives Chrome's real input-method
// path over CDP — Input.imeSetComposition sets the pending text, Input.insertText
// commits it — which is the same road a macOS dead key or a CJK candidate takes,
// and which reaches the app ONLY if a focused editable element is there to
// receive it. On a build without one, nothing arrives and the checks fail.
//
// CommonJS so require() finds puppeteer-core via NODE_PATH. Env: PAGE_URL,
// CHROME.
const puppeteer = require("puppeteer-core");

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
    const bootError = await page.evaluate(() => globalThis.__bootError || "");
    if (bootError) throw new Error("boot: " + bootError);

    // The element a composition needs. Without it the browser starts none.
    const ime = await page.evaluate(() => {
      const el = document.getElementById("gotex-ime");
      return el
        ? { present: true, focused: document.activeElement === el, tag: el.tagName }
        : { present: false, focused: false, tag: "" };
    });
    check(ime.present, "the page holds an element a composition can happen in");
    check(ime.focused, "and it has the keyboard focus");

    const source = () => page.evaluate(() => globalThis.gotexSource());
    const client = await page.target().createCDPSession();
    const compose = async (pending, commit) => {
      await client.send("Input.imeSetComposition", {
        text: pending,
        selectionStart: pending.length,
        selectionEnd: pending.length,
      });
      await client.send("Input.insertText", { text: commit });
      await new Promise((r) => setTimeout(r, 150));
    };

    // 1. A bare circumflex — how a superscript is written, and what a French
    //    keyboard produces with the dead key followed by a space.
    await page.evaluate(() => globalThis.gotexSetSource(""));
    await compose("^", "^");
    let s = await source();
    check(s.includes("^"), 'a dead-key circumflex reached the document (source is "' + s + '")');

    // 2. The pending text is NOT in the document until it commits.
    await page.evaluate(() => globalThis.gotexSetSource(""));
    await client.send("Input.imeSetComposition", {
      text: "^",
      selectionStart: 1,
      selectionEnd: 1,
    });
    await new Promise((r) => setTimeout(r, 150));
    s = await source();
    check(s === "", 'a composition in flight leaves the document alone (source is "' + s + '")');
    await client.send("Input.insertText", { text: "î" });
    await new Promise((r) => setTimeout(r, 150));
    s = await source();
    check(s === "î", 'committing it replaces the preview with the letter (source is "' + s + '")');

    // 3. A commit can be several characters, as a CJK candidate is.
    await page.evaluate(() => globalThis.gotexSetSource(""));
    await compose("ni", "你好");
    s = await source();
    check(s === "你好", 'a multi-character commit lands whole (source is "' + s + '")');

    // 4. And ordinary typing still works beside it.
    await page.evaluate(() => globalThis.gotexSetSource(""));
    await page.keyboard.type("i", { delay: 20 });
    await compose("^", "^");
    await page.keyboard.type("2", { delay: 20 });
    s = await source();
    check(s === "i^2", 'i, then a dead key, then 2 gives i^2 (source is "' + s + '")');

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
