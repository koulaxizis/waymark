// Drives remote/ in headless Chromium for browser_test.go.
// Args: <QR url> <file for the return code> <file to upload> <download dir>
import { createRequire } from "module";
import { writeFileSync } from "fs";
import { join } from "path";
const require = createRequire(import.meta.url);
const { chromium } = require("playwright");

const [url, codeFile, upFile, outDir] = process.argv.slice(2);
const log = (...a) => console.log(...a);
const browser = await chromium.launch({
  args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"],
});
try {
  const ctx = await browser.newContext({ acceptDownloads: true, permissions: ["camera"] });
  const page = await ctx.newPage();
  // WM_SHOTS=<dir> keeps phone-size screenshots of each step.
  const shots = process.env.WM_SHOTS;
  if (shots) await page.setViewportSize({ width: 390, height: 844 });
  const shot = async (n) => shots && page.screenshot({ path: join(shots, n + ".png"), fullPage: true });
  page.on("console", (m) => log("console:", m.text()));
  page.on("pageerror", (e) => log("pageerror:", e.message));
  await page.goto(url);
  await shot("1-setup");
  await page.fill("#nameInput", "Test phone");
  await page.click("#btnSetup");
  await page.waitForFunction(() => /^[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$/.test(document.getElementById("retCode").textContent), null, { timeout: 15000 });
  const code = await page.textContent("#retCode");
  if (page.url().includes("WM1.")) throw new Error("token left in the address bar");
  await shot("2-code");
  writeFileSync(codeFile, code);
  log("code", code);
  await page.waitForTimeout(500);
  await page.click("#btnConnect");
  await page.waitForSelector("#sLive:not([hidden])", { timeout: 30000 });
  const sas = (await page.textContent("#sasNum")).trim();
  log("sas", sas);
  log("live");
  await page.click("#btnTake");
  await page.waitForSelector("#btnRelease:not([hidden])", { timeout: 10000 });
  // Files: upload, then open the PDF.
  await page.click("#tabFiles");
  await page.waitForSelector('tr[data-path="talk.pdf"]', { timeout: 10000 });
  await page.setInputFiles("#fUpload", upFile);
  await page.waitForSelector('tr[data-path="notes.txt"]', { timeout: 30000 });
  log("uploaded");
  await shot("4-files");
  const dl = page.waitForEvent("download", { timeout: 30000 });
  await page.click('tr[data-path="big.bin"] [data-act=get]');
  const d = await dl;
  await d.saveAs(join(outDir, "big.bin"));
  log("downloaded");
  await page.click('tr[data-path="talk.pdf"] [data-act=open]');
  await page.waitForSelector("#panePres:not([hidden])");
  await page.click("#bStart");
  await page.waitForFunction(() => document.getElementById("cSlide").textContent === "1 / 5", null, { timeout: 10000 });
  await page.click("#bNext");
  await page.waitForFunction(() => document.getElementById("cSlide").textContent === "2 / 5", null, { timeout: 10000 });
  const slide = await page.textContent("#cSlide");
  await shot("5-pres");
  log(JSON.stringify({ SAS: sas, Slide: slide }));
} finally {
  await browser.close();
}
