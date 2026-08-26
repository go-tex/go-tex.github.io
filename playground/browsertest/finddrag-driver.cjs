// Real-browser proof that the find-and-replace modal follows the pointer when
// its title bar is dragged. Real mouse events on the canvas, and the verdict is
// where the panel ENDED UP — read back from the app, not inferred from a click
// count.
//
// Env: PAGE_URL, CHROME, NODE_PATH (with puppeteer-core), SCREENSHOT.
const puppeteer = require("puppeteer-core");

(async () => {
  const browser = await puppeteer.launch({
    executablePath: process.env.CHROME, headless: true, args: ["--no-sandbox"],
  });
  const page = await browser.newPage();
  await page.setViewport({ width: 1160, height: 900, deviceScaleFactor: 2 });
  page.on("console", (m) => console.log("  page:", m.text()));
  await page.goto(process.env.PAGE_URL, { waitUntil: "load" });
  await page.waitForFunction("globalThis.gotexPlaygroundReady === true", { timeout: 120000 });

  await page.evaluate(() => gotexToggleFindReplace());
  await page.waitForFunction("gotexRects()['findPanel']", { timeout: 15000 });

  const before = await page.evaluate(() => gotexRects()["findPanel"]);
  console.log("panel before           :", JSON.stringify(before));

  // Grab the title strip: above the input bar, left of the × button. Coordinates
  // come back in DEVICE pixels; the mouse takes CSS pixels.
  const dpr = await page.evaluate(() => window.devicePixelRatio || 1);
  const rect = await page.evaluate(() => {
    const c = document.getElementById("gotex-canvas").getBoundingClientRect();
    return { x: c.x, y: c.y };
  });
  const title = await page.evaluate(() => gotexRects()["findTitle"]);
  console.log("title strip            :", JSON.stringify(title));
  const gx = rect.x + (title[0] + title[2] / 3) / dpr;
  const gy = rect.y + (title[1] + title[3] / 2) / dpr;
  const DX = 60, DY = 40;

  await page.mouse.move(gx, gy);
  await page.mouse.down();
  await page.mouse.move(gx + DX / 2, gy + DY / 2);
  await page.mouse.move(gx + DX, gy + DY);
  await page.mouse.up();
  await new Promise((r) => setTimeout(r, 200));

  const after = await page.evaluate(() => gotexRects()["findPanel"]);
  console.log("panel after            :", JSON.stringify(after));
  const movedX = after[0] - before[0], movedY = after[1] - before[1];
  console.log(`moved                  : ${movedX},${movedY} device px (dragged ${DX * dpr},${DY * dpr})`);

  // And it stops when the button comes up.
  await page.mouse.move(gx + DX + 80, gy + DY + 80);
  await new Promise((r) => setTimeout(r, 150));
  const parked = await page.evaluate(() => gotexRects()["findPanel"]);
  const stuck = parked[0] === after[0] && parked[1] === after[1];
  console.log("stays put after release:", stuck);

  if (process.env.SCREENSHOT) await page.screenshot({ path: process.env.SCREENSHOT });
  await browser.close();

  const ok = movedX > 0 && movedY > 0 && stuck;
  console.log("RESULT " + JSON.stringify({ ok, movedX, movedY }));
  process.exit(ok ? 0 : 1);
})().catch((e) => { console.error("DRIVER_FAIL", e); process.exit(2); });
