// Heimdall visual capture (X1 vision grading, docs/visual-grading.md).
// Run by the bench, never by the agent, with `node -e` inside the pinned
// Playwright image: in a clean-room container for svg and html artifacts,
// or inside the run's browser service container for a browser capture.
// It opens one URL in a fresh headless Chromium with a fixed viewport,
// device scale factor, colour scheme, locale, time zone and font hinting,
// waits for fonts, takes one PNG screenshot with animations and the caret
// off, and prints one JSON line: {"png": base64, "fonts_sha256": hex} or
// {"error": text}.
const { chromium } = require("/app/node_modules/playwright-core");
const crypto = require("crypto");
const cp = require("child_process");

function fontsHash() {
  try {
    const out = cp.execFileSync("fc-list", [":", "family", "style", "file"], { encoding: "utf8", timeout: 10000 });
    const lines = out.split("\n").filter(Boolean).sort();
    return crypto.createHash("sha256").update(lines.join("\n")).digest("hex");
  } catch (e) {
    return "";
  }
}

(async () => {
  const o = JSON.parse(process.argv[1]);
  const browser = await chromium.launch({
    headless: true,
    channel: "chromium",
    args: ["--no-sandbox", "--font-render-hinting=none", "--disable-lcd-text", "--force-color-profile=srgb", "--hide-scrollbars", "--disable-gpu"],
  });
  try {
    const ctx = await browser.newContext({
      viewport: { width: o.width, height: o.height },
      deviceScaleFactor: o.scale,
      colorScheme: "light",
      reducedMotion: "reduce",
      locale: "en-US",
      timezoneId: "UTC",
      serviceWorkers: "block",
    });
    await ctx.route("**/*", (route) => {
      const u = route.request().url();
      const ok = u.startsWith("data:") || (o.origin ? u.startsWith(o.origin + "/") || u === o.origin : u.startsWith("file:///in/"));
      return ok ? route.continue() : route.abort();
    });
    const page = await ctx.newPage();
    await page.goto(o.url, { waitUntil: "load", timeout: 30000 });
    await page.evaluate(() => (document.fonts ? document.fonts.ready.then(() => true) : true));
    if (o.wait_ms > 0) await page.waitForTimeout(o.wait_ms);
    const png = await page.screenshot({ fullPage: !!o.full_page, animations: "disabled", caret: "hide", type: "png", scale: "device" });
    process.stdout.write(JSON.stringify({ png: png.toString("base64"), fonts_sha256: fontsHash() }) + "\n");
  } finally {
    await browser.close();
  }
})().catch((e) => {
  process.stdout.write(JSON.stringify({ error: String((e && e.message) || e).split("\n")[0] }) + "\n");
  process.exit(3);
});
