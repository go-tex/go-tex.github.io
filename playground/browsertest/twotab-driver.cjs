// Headless-browser driver for the playground's live collaboration, driven the
// way a real user does it: TWO SEPARATE tabs.
//
// Unlike the one-page proof (collab-browsertest), this opens TWO INDEPENDENT
// puppeteer pages — two page contexts, two Go/wasm instances, two real
// RTCPeerConnections — loads the REAL playground app (cmd/playground-wasm) in
// each, and drives the ACTUAL Collaborate panel: it clicks the launcher and the
// Host / Join / Copy / Paste buttons with genuine pointer events at their device
// rects, and carries the SDP blob between the tabs through the real OS clipboard
// (writeText on the copy side, the app's own readText on the paste side). Then it
// types into tab A's editor and asserts tab B converges and paints A's remote
// caret, and vice-versa. This is the flow the one-page test masks.
//
// Every RTCPeerConnection is wrapped before the wasm boots to log its ICE
// gathering/connection state and candidate types, so a run leaves the evidence a
// root-cause needs on the console.
//
// Env:
//   PAGE_URL       the page both tabs load
//   CHROME         the Chrome (for Testing) binary
//   SCREENSHOT     path prefix for the two proof PNGs (…-A.png / …-B.png)
//   ICE_SERVERS    "" or "EMPTY" -> configure NO ICE servers (reproduce the bug);
//                  a comma list -> gotexSetICEServers(that); unset -> app default
//   DISABLE_MDNS   "1" adds --disable-features=WebRtcHideLocalIpsWithMdns (the
//                  flag the one-page test relied on; OFF here to mirror a user)
//   CONNECT_TIMEOUT_MS  how long to wait for both peers to connect (default 25000)
// CommonJS so require() finds puppeteer-core through NODE_PATH.
const puppeteer = require("puppeteer-core");

const MARKER_A = "ALPHA";
const MARKER_B = "BRAVO";

// Wrap RTCPeerConnection so every page logs what WebRTC actually does: the ICE
// gathering + connection states, and the type of each local candidate (host /
// srflx / relay, and whether a host candidate is an mDNS ".local" address).
const ICE_PROBE = (tag) => `
(() => {
  const Native = window.RTCPeerConnection;
  if (!Native || Native.__wrapped) return;
  function Wrapped(cfg) {
    const pc = new Native(cfg);
    const say = (m) => console.log("[ice ${tag}] " + m);
    say("new RTCPeerConnection iceServers=" + JSON.stringify((cfg && cfg.iceServers) || []));
    pc.addEventListener("icegatheringstatechange", () => say("gathering=" + pc.iceGatheringState));
    pc.addEventListener("iceconnectionstatechange", () => say("connection=" + pc.iceConnectionState));
    pc.addEventListener("connectionstatechange", () => say("pc=" + pc.connectionState));
    pc.addEventListener("icecandidate", (e) => {
      if (!e.candidate) { say("candidate=(end-of-candidates)"); return; }
      const c = e.candidate.candidate || "";
      const typ = (c.match(/ typ (\\w+)/) || [])[1] || "?";
      const mdns = /\\.local/.test(c) ? " mdns" : "";
      say("candidate typ=" + typ + mdns);
    });
    return pc;
  }
  Wrapped.prototype = Native.prototype;
  Wrapped.__wrapped = true;
  window.RTCPeerConnection = Wrapped;
})();
`;

(async () => {
  const url = process.env.PAGE_URL;
  const executablePath = process.env.CHROME;
  const screenshot = process.env.SCREENSHOT;
  const iceEnv = process.env.ICE_SERVERS; // undefined | "" | "EMPTY" | "url,url"
  const connectTimeout = parseInt(process.env.CONNECT_TIMEOUT_MS || "25000", 10);
  if (!url || !executablePath) {
    console.error("DRIVER_FAIL missing PAGE_URL or CHROME");
    process.exit(2);
  }

  const fails = [];
  const check = (cond, msg) => {
    console.log((cond ? "PASS " : "FAIL ") + msg);
    if (!cond) fails.push(msg);
  };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  const args = ["--no-sandbox", "--disable-dev-shm-usage"];
  if (process.env.DISABLE_MDNS === "1") {
    args.push("--disable-features=WebRtcHideLocalIpsWithMdns");
  }

  const browser = await puppeteer.launch({ executablePath, headless: true, args });
  try {
    const origin = new URL(url).origin;
    // Grant clipboard access through CDP: the app's Copy button calls
    // navigator.clipboard.writeText and the Paste button readText, which headless
    // Chrome refuses without an explicit clipboardReadWrite grant.
    const cdp = await browser.target().createCDPSession();
    await cdp.send("Browser.grantPermissions", {
      origin,
      permissions: ["clipboardReadWrite", "clipboardSanitizedWrite"],
    });

    // Bring up one real tab: instrument WebRTC, load the app, wait for boot, and
    // configure ICE the way this run wants it.
    const openTab = async (tag) => {
      const page = await browser.newPage();
      await page.setViewport({ width: 1200, height: 860, deviceScaleFactor: 2 });
      page.on("console", (m) => console.log("[" + tag + "] " + m.text()));
      page.on("pageerror", (e) => console.log("[" + tag + " pageerror] " + e.message));
      await page.evaluateOnNewDocument(ICE_PROBE(tag));
      await page.goto(url, { waitUntil: "load", timeout: 30000 });
      await page.waitForFunction(
        () => globalThis.gotexPlaygroundReady || globalThis.__bootError,
        { timeout: 60000, polling: 100 },
      );
      const boot = await page.evaluate(() => globalThis.__bootError || null);
      if (boot) throw new Error(tag + " wasm boot error: " + boot);
      if (iceEnv !== undefined) {
        const csv = iceEnv === "EMPTY" ? "" : iceEnv;
        await page.evaluate((c) => globalThis.gotexSetICEServers(c), csv);
      }
      const st = await page.evaluate(() => globalThis.gotexCollabState());
      console.log("[" + tag + "] ICE servers = " + JSON.stringify(st.iceServers));
      return page;
    };

    const A = await openTab("A");
    const B = await openTab("B");

    const state = (p) => p.evaluate(() => globalThis.gotexCollabState());
    const rects = (p) => p.evaluate(() => globalThis.gotexCollabRects());

    // Click the centre of a device-pixel [x,y,w,h] rect with a real pointer, the
    // same device->CSS mapping the app's mousedown handler inverts.
    const clickRect = async (p, r) => {
      const pt = await p.evaluate((rr) => {
        const c = document.getElementById("gotex-canvas");
        const b = c.getBoundingClientRect();
        const dpr = c.width / b.width;
        return { x: b.left + (rr[0] + rr[2] / 2) / dpr, y: b.top + (rr[1] + rr[3] / 2) / dpr };
      }, r);
      await p.mouse.click(pt.x, pt.y);
    };
    const clickButton = async (p, name) => {
      const r = await rects(p);
      if (!r[name]) throw new Error("no collab button " + name + " (have " + Object.keys(r) + ")");
      await clickRect(p, r[name]);
    };
    const waitState = async (p, pred, ms, label) => {
      const deadline = Date.now() + ms;
      let last;
      while (Date.now() < deadline) {
        last = await state(p);
        if (pred(last)) return last;
        await sleep(120);
      }
      throw new Error("timeout waiting for " + label + "; last state=" + JSON.stringify(last));
    };

    // --- the copy-paste handshake across two tabs, through the real UI --------
    await A.bringToFront();
    await clickButton(A, "launcher"); // open the Collaborate panel
    await waitState(A, (s) => s.open, 4000, "A panel open");
    await clickButton(A, "host");
    const aHost = await waitState(A, (s) => s.phase === 1 || s.offer, 22000, "A offer ready (hostWait)");
    check(aHost.phase === 1, "tab A reached hostWait after clicking Host (phase " + aHost.phase + ")");

    // Copy the invitation with the real button, then read it back off the OS
    // clipboard — proving the Copy button actually wrote the blob.
    await clickButton(A, "copyOffer");
    await sleep(150);
    const offer = await A.evaluate(() => navigator.clipboard.readText());
    check(offer && offer.length > 0, "Copy invitation put a " + (offer ? offer.length : 0) + "-byte offer on the clipboard");

    // Tab B joins: open panel, Join, put the offer on B's clipboard, Paste it.
    await B.bringToFront();
    await clickButton(B, "launcher");
    await waitState(B, (s) => s.open, 4000, "B panel open");
    await clickButton(B, "join");
    await waitState(B, (s) => s.phase === 2, 4000, "B guestOffer");
    await B.evaluate((o) => navigator.clipboard.writeText(o), offer);
    await clickButton(B, "pasteOffer");
    const bWait = await waitState(B, (s) => s.phase === 3 || s.answer, 22000, "B answer ready (guestWait)");
    check(bWait.phase === 3, "tab B reached guestWait after pasting the invitation (phase " + bWait.phase + ")");

    // Copy the reply with the real button, read it back, hand it to A, Paste it.
    await clickButton(B, "copyAnswer");
    await sleep(150);
    const answer = await B.evaluate(() => navigator.clipboard.readText());
    check(answer && answer.length > 0 && answer !== offer, "Copy reply put a distinct answer on the clipboard");

    await A.bringToFront();
    await A.evaluate((a) => navigator.clipboard.writeText(a), answer);
    await clickButton(A, "pasteAnswer");

    // --- the moment of truth: do two independent tabs actually connect? -------
    let connected = true;
    try {
      await waitState(A, (s) => s.connected, connectTimeout, "A connected");
      await waitState(B, (s) => s.connected, connectTimeout, "B connected");
    } catch (e) {
      connected = false;
      console.log("NO_CONNECT " + e.message);
    }
    check(connected, "both independent tabs connected over WebRTC");
    if (!connected) {
      // Reproduction path: leave the evidence and stop; the console ICE log above
      // shows why (candidate types, gathering/connection state).
      console.log("RESULT " + JSON.stringify({ ok: false, connected: false, fails }));
      process.exitCode = 1;
      return;
    }

    // Close both panels so the editor + remote carets are unobstructed, and type.
    const typeInto = async (p, marker) => {
      await p.bringToFront();
      await p.keyboard.press("Escape"); // close the modal panel
      await waitState(p, (s) => !s.open, 3000, "panel closed");
      const caret = await p.evaluate(() => globalThis.gotexCaretPixel(0, 0));
      const b = await p.evaluate(() => {
        const c = document.getElementById("gotex-canvas");
        const r = c.getBoundingClientRect();
        return { left: r.left, top: r.top, dpr: c.width / r.width };
      });
      await p.mouse.click(b.left + caret[0] / b.dpr, b.top + caret[1] / b.dpr);
      for (const ch of marker) await p.keyboard.type(ch);
    };
    const source = (p) => p.evaluate(() => globalThis.gotexSource());
    const decos = (p) => p.evaluate(() => globalThis.gotexCollabState().decorations);

    const nameA = (await state(A)).name;
    const nameB = (await state(B)).name;
    console.log("tab A is " + JSON.stringify(nameA) + ", tab B is " + JSON.stringify(nameB));

    // A -> B: type into A, B converges and shows A's caret.
    await typeInto(A, MARKER_A);
    await waitState(B, () => true, 0, "noop").catch(() => {});
    let ok = false;
    for (let i = 0; i < 120 && !ok; i++) {
      ok = (await source(B)).includes(MARKER_A);
      if (!ok) await sleep(120);
    }
    check(ok, "tab B converged on tab A's edit (" + JSON.stringify(MARKER_A) + ")");
    let dB = await decos(B);
    check(dB.some((d) => d.label === nameA), "tab B paints tab A's remote caret (" + JSON.stringify(dB) + ")");

    // B -> A: type into B, A converges and shows B's caret.
    await typeInto(B, MARKER_B);
    ok = false;
    for (let i = 0; i < 120 && !ok; i++) {
      ok = (await source(A)).includes(MARKER_B);
      if (!ok) await sleep(120);
    }
    check(ok, "tab A converged on tab B's edit (" + JSON.stringify(MARKER_B) + ")");
    let dA = await decos(A);
    check(dA.some((d) => d.label === nameB), "tab A paints tab B's remote caret (" + JSON.stringify(dA) + ")");

    // Both buffers now hold both markers and agree.
    const sA = await source(A);
    const sB = await source(B);
    check(sA === sB && sA.includes(MARKER_A) && sA.includes(MARKER_B), "both tabs hold identical, fully-merged buffers");

    if (screenshot) {
      for (const [p, suffix] of [[A, "-A.png"], [B, "-B.png"]]) {
        try {
          await p.bringToFront();
          await clickButton(p, "launcher"); // reopen the panel for the peer list in the shot
          await sleep(200);
          const path = screenshot.replace(/\.png$/, "") + suffix;
          await p.screenshot({ path });
          console.log("SCREENSHOT " + path);
        } catch (e) {
          console.log("SCREENSHOT_FAIL " + (e && e.message ? e.message : e));
        }
      }
    }

    const allOk = fails.length === 0;
    console.log("RESULT " + JSON.stringify({ ok: allOk, connected: true, fails }));
    process.exitCode = allOk ? 0 : 1;
  } catch (err) {
    console.error("DRIVER_FAIL " + (err && err.stack ? err.stack : err));
    process.exitCode = 2;
  } finally {
    await browser.close();
  }
})();
