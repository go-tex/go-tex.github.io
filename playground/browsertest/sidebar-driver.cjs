// Headless-browser render proof for the Git workspace sidebar.
//
// It loads the REAL playground app (cmd/playground-wasm) into the same
// full-height flex host page the zones proof uses (a blue header, then the
// canvas stage flexing to fill the rest of the viewport, exactly like the Hugo
// playground page), and proves, only through the gotex* hooks + real canvas
// pixels:
//
//   1. On load the canvas fills the viewport height below the header AND its full
//      width (the #46 full-width/full-height layout the sidebar must not break —
//      the regression that a synthetic page missed).
//   2. The sidebar is OPEN on load — no click needed: gotexSidebar() (no arg)
//      reports open, reserves a left column of a sane width while the canvas
//      fills the viewport (the sidebar is painted inside the canvas, so the CSS
//      box is unchanged), and the editor+render body sits to the right of the
//      column (editorW < canvasW).
//   3. The sidebar column actually paints on load: a vertical band of
//      non-background pixels on the left of the canvas, at the column's device
//      rect — the workspace is present without any interaction.
//   4. The toggle still works: gotexSidebar(false) closes the column (width 0)
//      and the editor+render body reclaims the full canvas width, and the canvas
//      CSS box is unchanged throughout.
//
// CommonJS so require() finds puppeteer-core via NODE_PATH. Env: PAGE_URL,
// CHROME, SCREENSHOT (optional).
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

    // The canvas CSS box + backing size, and the header, in one read.
    const geom = () =>
      page.evaluate(() => {
        const c = document.getElementById("gotex-canvas");
        const cb = c.getBoundingClientRect();
        const hb = document.querySelector(".hero").getBoundingClientRect();
        return {
          top: cb.top,
          bottom: cb.bottom,
          left: cb.left,
          right: cb.right,
          width: cb.width,
          height: cb.height,
          headerBottom: hb.bottom,
          viewW: window.innerWidth,
          viewH: window.innerHeight,
        };
      });

    // 1. Full-height AND full-width on load.
    const g0 = await geom();
    const avail = g0.viewH - g0.headerBottom;
    check(g0.top >= g0.headerBottom - 1, "canvas sits below the header (top " + g0.top.toFixed(0) + ")");
    check(g0.bottom >= g0.viewH - 24, "canvas reaches the viewport bottom (" + g0.bottom.toFixed(0) + " of " + g0.viewH + ")");
    check(g0.height >= avail - 40, "canvas fills the height below the header (" + g0.height.toFixed(0) + " of " + avail.toFixed(0) + ")");
    // Full-bleed width, allowing the host page's small symmetric inset (the #46
    // regression made the canvas MUCH narrower via auto margins, which this still
    // catches — a near-full width is the guard, not pixel-exact edges).
    check(g0.width >= g0.viewW - 40, "canvas fills the full viewport width (" + g0.width.toFixed(0) + " of " + g0.viewW + ")");

    // 2. The sidebar is OPEN on load — read it with NO argument (no click / no
    //    open call): the workspace column is already reserved on the left while
    //    the canvas box still fills the viewport (the column is painted INSIDE the
    //    canvas), and the editor+render body sits to its right.
    const sb = await page.evaluate(() => globalThis.gotexSidebar());
    const r = sb.rect; // [x, y, w, h] device px
    check(sb.open === true, "sidebar is open on load");
    check(r[0] === 0, "sidebar column is anchored to the left (x=" + r[0] + ")");
    check(r[2] >= 200, "sidebar column has a sane device width (" + r[2] + " px)");
    check(r[2] < sb.canvasW, "sidebar does not swallow the whole canvas (" + r[2] + " < " + sb.canvasW + ")");
    check(r[3] >= sb.canvasH * 0.4, "sidebar column fills most of the body height (" + r[3] + " of " + sb.canvasH + ")");
    check(sb.editorW > 0 && sb.editorW < sb.canvasW - r[2] + 8, "editor body sits to the right of the column (editorW " + sb.editorW + ", canvasW " + sb.canvasW + ")");

    // 3. The column actually paints on load: sample the canvas backing pixels
    //    inside the sidebar rect and confirm a non-background vertical band on the
    //    left — the workspace is present without any interaction.
    const readImage = (rr) =>
      page.evaluate((q) => {
        const c = document.getElementById("gotex-canvas");
        const ctx = c.getContext("2d");
        const d = ctx.getImageData(q[0], q[1], q[2], q[3]).data;
        return Array.from(d);
      }, rr);
    // Background sampled from the far bottom-right corner of the canvas (outside
    // the column, in the render/body region).
    const bgPix = await readImage([sb.canvasW - 4, sb.canvasH - 4, 2, 2]);
    const bg = [bgPix[0], bgPix[1], bgPix[2]];
    // Sample a strip inside the column (skip the left edge + header).
    const strip = await readImage([2, Math.floor(r[3] / 3), Math.min(40, r[2] - 4), 40]);
    let painted = false;
    for (let i = 0; i + 3 < strip.length; i += 4) {
      if (strip[i + 3] !== 0 && (strip[i] !== bg[0] || strip[i + 1] !== bg[1] || strip[i + 2] !== bg[2])) {
        painted = true;
        break;
      }
    }
    check(painted, "sidebar column painted a non-background band on the left on load");

    // 4. The toggle still closes it: gotexSidebar(false) drops the column (width
    //    0) and the editor+render body reclaims the full canvas width.
    const sc = await page.evaluate(() => globalThis.gotexSidebar(false));
    check(sc.open === false, "toggle closes the sidebar");
    check(sc.rect[2] === 0 && sc.width === 0, "closed sidebar reserves no column (width " + sc.width + ")");
    check(sc.editorW > sb.editorW, "editor body widened when the sidebar closed (" + sb.editorW + " -> " + sc.editorW + ")");

    // The canvas CSS box is UNCHANGED by the sidebar toggling (still full-bleed).
    const g1 = await geom();
    check(Math.abs(g1.width - g0.width) < 1 && Math.abs(g1.height - g0.height) < 1, "toggling the sidebar did not resize the canvas box (" + g1.width.toFixed(0) + "x" + g1.height.toFixed(0) + ")");

    // Re-open so the screenshot captures the on-load workspace look.
    await page.evaluate(() => globalThis.gotexSidebar(true));

    if (screenshot) {
      try {
        await page.screenshot({ path: screenshot });
        console.log("SCREENSHOT " + screenshot);
      } catch (e) {
        console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
      }
    }

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
