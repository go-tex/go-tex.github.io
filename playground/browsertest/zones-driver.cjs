// Headless-browser render proof for the full-height canvas + the two moved-in
// HTML bands (topZone status line, bottomZone description/footer with links).
//
// It loads the REAL playground app (cmd/playground-wasm) into a full-height flex
// page — a blue header, then the canvas stage flexing to fill the rest of the
// viewport, exactly like the Hugo playground page — and proves, only through the
// gotex* hooks + real canvas pixels + a real pointer click:
//
//   1. the canvas fills the viewport height below the header (full-height layout);
//   2. the topZone status band paints, including its green "ready" dot;
//   3. the bottomZone lays out the three links and their text paints (accent ink
//      over the band background);
//   4. clicking a link's device rect navigates the browser to that link's url
//      (the whole path: canvas pointer -> HandleClick -> bottomZone -> navigate
//      hook -> window.location.href).
//
// CommonJS so require() finds puppeteer-core via NODE_PATH. Env: PAGE_URL, CHROME,
// SCREENSHOT (optional).
const puppeteer = require("puppeteer-core");

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
    const VIEW = { width: 1200, height: 900, deviceScaleFactor: 2 };
    await page.setViewport(VIEW);
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

    // 1. Full-height: the canvas CSS box fills the viewport below the header.
    const geom = await page.evaluate(() => {
      const c = document.getElementById("gotex-canvas");
      const cb = c.getBoundingClientRect();
      const hb = document.querySelector(".hero").getBoundingClientRect();
      return {
        canvasTop: cb.top,
        canvasBottom: cb.bottom,
        canvasHeight: cb.height,
        headerBottom: hb.bottom,
        viewH: window.innerHeight,
        backingH: c.height,
        dpr: c.height / cb.height,
      };
    });
    check(
      geom.canvasTop >= geom.headerBottom - 1,
      "canvas sits below the blue header (canvasTop " + geom.canvasTop.toFixed(0) + " >= headerBottom " + geom.headerBottom.toFixed(0) + ")",
    );
    // The canvas bottom must reach very near the viewport bottom, and its height
    // must be the lion's share of the space below the header — i.e. it FILLS it.
    const avail = geom.viewH - geom.headerBottom;
    check(
      geom.canvasBottom >= geom.viewH - 24,
      "canvas reaches the viewport bottom (canvasBottom " + geom.canvasBottom.toFixed(0) + " of " + geom.viewH + ")",
    );
    check(
      geom.canvasHeight >= avail - 40,
      "canvas fills the height below the header (" + geom.canvasHeight.toFixed(0) + " of " + avail.toFixed(0) + " available)",
    );

    // Pull the band geometry from the app.
    const z = await page.evaluate(() => globalThis.gotexZones());
    check(/engine ready/.test(z.topStatus), "topZone status reads 'engine ready' (" + z.topStatus + ")");
    check(Array.isArray(z.links) && z.links.length === 3, "bottomZone laid out 3 links (got " + (z.links && z.links.length) + ")");

    const origin = await page.evaluate(() => location.origin + "/");
    const wantURLs = ["https://github.com/go-tex/engine", "https://github.com/go-tex/brand", origin];
    for (const w of wantURLs) {
      check(z.links.some((l) => l.url === w), "a bottomZone link targets " + w);
    }

    // readImage(x,y,w,h) -> flat RGBA of that device-pixel canvas region (the app
    // blits with putImageData, so getImageData reads the painted pixels straight
    // back). Coordinates are backing-store (device) pixels, the same space the
    // gotexZones rects are in.
    const readImage = (r) =>
      page.evaluate((rr) => {
        const c = document.getElementById("gotex-canvas");
        const ctx = c.getContext("2d");
        const d = ctx.getImageData(rr[0], rr[1], rr[2], rr[3]).data;
        return Array.from(d);
      }, r);

    // 2. The topZone ready dot: its exact green must appear in the top band.
    const [dr, dg, db] = z.readyDot;
    const topPix = await readImage(z.topRect);
    let sawDot = false;
    for (let i = 0; i + 3 < topPix.length; i += 4) {
      if (topPix[i] === dr && topPix[i + 1] === dg && topPix[i + 2] === db) {
        sawDot = true;
        break;
      }
    }
    check(sawDot, "topZone ready dot painted (green " + dr + "," + dg + "," + db + " found in the band)");

    // 3. Each bottomZone link's text paints: its rect must carry pixels that differ
    //    from the band's own background (sampled from the band's top-left corner).
    const bgPix = await readImage([z.bottomRect[0], z.bottomRect[1], 3, 3]);
    const bg = [bgPix[0], bgPix[1], bgPix[2]];
    for (const l of z.links) {
      const px = await readImage(l.rect);
      let painted = false;
      for (let i = 0; i + 3 < px.length; i += 4) {
        if (px[i] !== bg[0] || px[i + 1] !== bg[1] || px[i + 2] !== bg[2]) {
          painted = true;
          break;
        }
      }
      check(painted, "bottomZone link text painted for " + l.url);
    }

    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

    // 4. Navigation: intercept navigations, click the go-tex/engine link's device
    //    rect through a real pointer, and confirm the browser tried to go there.
    const engine = z.links.find((l) => /go-tex\/engine/.test(l.url));
    let navURL = null;
    await page.setRequestInterception(true);
    page.on("request", (req) => {
      if (req.isNavigationRequest() && req.frame() === page.mainFrame() && /github\.com\/go-tex\/engine/.test(req.url())) {
        navURL = req.url();
        req.abort();
      } else {
        req.continue();
      }
    });
    const pt = await page.evaluate((rr) => {
      const c = document.getElementById("gotex-canvas");
      const b = c.getBoundingClientRect();
      const dpr = c.width / b.width;
      return { x: b.left + (rr[0] + rr[2] / 2) / dpr, y: b.top + (rr[1] + rr[3] / 2) / dpr };
    }, engine.rect);
    await page.mouse.click(pt.x, pt.y);
    // Give the navigation attempt a moment to fire.
    for (let i = 0; i < 40 && navURL === null; i++) {
      await new Promise((r) => setTimeout(r, 50));
    }
    check(navURL === engine.url, "clicking the go-tex/engine link navigated to it (got " + navURL + ")");

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
