// Headless-browser driver for the playground's WebRTC collaboration proof.
//
// It loads the page in a real Chrome, forwards everything the page logs, waits
// for the wasm to leave its verdict on globalThis.__result, takes a screenshot
// (the two editors, the guest showing the host's remote caret), prints the
// verdict, and exits 0 only if the peers converged. It is CommonJS so that
// require() finds puppeteer-core through NODE_PATH, which ES-module bare imports
// do not consult.
//
// Env: PAGE_URL (the page), CHROME (the browser binary), SCREENSHOT (a path to
// write the PNG to). NODE_PATH must contain a node_modules with puppeteer-core.
const puppeteer = require("puppeteer-core");

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
  if (!url || !executablePath) {
    console.error("DRIVER_FAIL missing PAGE_URL or CHROME");
    process.exit(2);
  }

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: [
      "--no-sandbox",
      "--disable-dev-shm-usage",
      // Two peers in one headless page reach each other over loopback host
      // candidates. Chrome otherwise hides local IPs behind mDNS ".local"
      // candidates, which have nothing to resolve them in a headless browser,
      // and the connection never completes.
      "--disable-features=WebRtcHideLocalIpsWithMdns",
    ],
  });
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1120, height: 900, deviceScaleFactor: 2 });
    page.on("console", (m) => console.log("[page] " + m.text()));
    page.on("pageerror", (e) => console.log("[pageerror] " + e.message));

    await page.goto(url, { waitUntil: "load", timeout: 30000 });
    const handle = await page.waitForFunction(
      () => globalThis.__result || null,
      { timeout: 60000, polling: 200 },
    );
    const result = await handle.jsonValue();
    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot, fullPage: true });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }
    console.log("RESULT " + JSON.stringify(result));
    process.exitCode = result && result.ok ? 0 : 1;
  } catch (err) {
    console.error("DRIVER_FAIL " + (err && err.message ? err.message : err));
    process.exitCode = 2;
  } finally {
    await browser.close();
  }
})();
