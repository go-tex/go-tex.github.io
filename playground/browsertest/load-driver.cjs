// Headless driver that times the playground's start-up up to the moment the Git
// panel shows the repository's content.
//
// It loads load-index.html (which does NOT decline the boot clone), and records,
// as milliseconds since navigation start: when playground.wasm finished
// downloading, when git-worker.wasm was requested and finished, when the Worker was
// created, when the main wasm reported ready, and when the panel first held a
// loaded document (gotexGitDebug().loadedPath). It prints one LOAD_RESULT json line.
//
// Env: PAGE_URL (with ?remote=…&prespawn=0|1), CHROME. Exit 0 when the panel
// content arrived.
const puppeteer = require("puppeteer-core");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  if (!url || !executablePath) {
    console.error("DRIVER_FAIL missing PAGE_URL/CHROME");
    process.exit(2);
  }
  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });
  let ok = false;
  const out = { workers: 0 };
  try {
    const page = await browser.newPage();
    page.on("console", (m) => console.log("[page] " + m.text()));
    page.on("pageerror", (e) => console.log("[pageerror] " + e.message));
    page.on("workercreated", (w) => {
      out.workers++;
      w.on("console", (m) => console.log("[w] " + m.text()));
    });
    const net = {};
    // Raw epoch times now; converted to the page's own clock (ms since navigation
    // start, the clock of the marks below) once its timeOrigin is known.
    const at = () => Date.now();
    page.on("request", (r) => {
      const u = r.url();
      if (u.includes("git-worker.wasm")) net.workerWasmRequested = at();
      if (u.includes("playground.wasm")) net.mainWasmRequested = at();
    });
    page.on("requestfinished", (r) => {
      const u = r.url();
      if (u.includes("git-worker.wasm")) net.workerWasmDone = at();
      if (u.includes("playground.wasm")) net.mainWasmDone = at();
    });

    // A 10 ms poller inside the page: the two events measured here have no hook of
    // their own, and polling from node would add its own round-trip to each.
    await page.evaluateOnNewDocument(() => {
      globalThis.__poll = {};
      globalThis.__long = [];
      try {
        new PerformanceObserver((l) => {
          for (const e of l.getEntries()) globalThis.__long.push([Math.round(e.startTime), Math.round(e.duration)]);
        }).observe({ type: "longtask", buffered: true });
      } catch (e) {}
      // The page's thread is busy if this 10 ms tick is late: the longest wait
      // between two ticks is how long a message could sit unhandled.
      globalThis.__poll.maxGap = 0;
      const t = setInterval(() => {
        const now = performance.now();
        if (globalThis.__poll.last !== undefined) {
          globalThis.__poll.maxGap = Math.max(globalThis.__poll.maxGap, now - globalThis.__poll.last);
        }
        globalThis.__poll.last = now;
        if (globalThis.gotexPlaygroundReady === true && !("mainReady" in globalThis.__poll)) {
          globalThis.__poll.mainReady = now;
        }
        if (typeof globalThis.gotexGitDebug === "function") {
          const d = globalThis.gotexGitDebug();
          if (d && d.loadedPath !== "" && d.busy === false && !("panelContent" in globalThis.__poll)) {
            globalThis.__poll.panelContent = now;
            globalThis.__poll.loadedPath = d.loadedPath;
            clearInterval(t);
          }
        }
      }, 10);
    });

    await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30000 });
    // performance.now() is relative to the page's own time origin, which is the
    // navigation start, so it needs no conversion against t0 beyond this offset.
    await page.waitForFunction(() => "panelContent" in globalThis.__poll, {
      timeout: 180000,
      polling: 100,
    });
    const r = await page.evaluate(() => ({
      poll: globalThis.__poll,
      marks: globalThis.__marks,
      origin: performance.timeOrigin,
      long: globalThis.__long,
      progressMsgs: globalThis.__progressMsgs,
    }));
    out.progressMsgs = r.progressMsgs;
    out.maxGap = Math.round(r.poll.maxGap);
    out.longTasks = r.long;
    out.timeOrigin = Math.round(r.origin);
    for (const k of Object.keys(net)) net[k] = Math.round(net[k] - r.origin);
    Object.assign(out, net, {
      mainDownloaded: Math.round(r.marks["main-downloaded"]),
      mainInstantiated: Math.round(r.marks["main-instantiated"]),
      mainReady: Math.round(r.poll.mainReady),
      panelContent: Math.round(r.poll.panelContent),
      loadedPath: r.poll.loadedPath,
    });
    ok = true;
  } catch (err) {
    out.error = String(err && err.message ? err.message : err);
    console.error("DRIVER_FAIL " + out.error);
  } finally {
    console.log("LOAD_RESULT " + JSON.stringify(out));
    await sleep(50);
    await browser.close();
    process.exitCode = ok ? 0 : 1;
  }
})();
