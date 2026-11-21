// Built-in PDF viewer: pdf.js renders one page at a time, fitted to the
// screen. The Agent drives it with commands; it reports n/N back.
import * as pdfjs from "./vendor/pdfjs/pdf.min.mjs";

pdfjs.GlobalWorkerOptions.workerSrc = "./vendor/pdfjs/pdf.worker.min.mjs";

let doc = null;
let page = 1;
let showing = false;
let black = false;
let hooks = { report() {}, localEnd() {} };
let renderTask = null;
let loading = null; // the open in progress
let queued = [];    // commands that came while the file was loading
const $ = (id) => document.getElementById(id);

export const isShowing = () => showing;

export function init(h) {
  hooks = h;
  document.addEventListener("keydown", (e) => {
    if (!showing) return;
    // The presenter can also drive the show from the laptop keyboard.
    if (["ArrowRight", "PageDown", " "].includes(e.key)) command("next");
    else if (["ArrowLeft", "PageUp"].includes(e.key)) command("prev");
    else if (e.key === "b" || e.key === "B" || e.key === ".") command("black", !black);
    else if (e.key === "Escape") hooks.localEnd();
    else return;
    e.preventDefault();
  });
  window.addEventListener("resize", () => showing && render());
}

export async function open(url) {
  close();
  const task = (loading = pdfjs.getDocument({
      url,
      cMapUrl: "./vendor/pdfjs/cmaps/",
      cMapPacked: true,
      standardFontDataUrl: "./vendor/pdfjs/standard_fonts/",
      isEvalSupported: false,
    }).promise);
  try {
    const d = await task;
    if (loading !== task) { d.destroy(); return; }
    doc = d;
    page = 1;
    hooks.report(page, doc.numPages);
    const q = queued;
    queued = [];
    q.forEach(([c, a]) => command(c, a));
  } catch (e) {
    console.error("pdf open", e);
    doc = null;
  } finally {
    if (loading === task) loading = null;
  }
}

function close() {
  loading = null;
  queued = [];
  showing = false;
  black = false;
  $("viewer").hidden = true;
  if (doc) doc.destroy();
  doc = null;
}

export function command(cmd, arg) {
  if (!doc && cmd !== "close" && cmd !== "end") {
    if (loading) queued.push([cmd, arg]);
    return;
  }
  switch (cmd) {
    case "start":
      showing = true;
      page = clamp(arg || page);
      $("viewer").hidden = false;
      $("vHint").textContent = "";
      setBlack(false);
      break;
    case "next": page = clamp(page + 1); setBlack(false); break;
    case "prev": page = clamp(page - 1); setBlack(false); break;
    case "goto": page = clamp(arg); setBlack(false); break;
    case "black": setBlack(!!arg); return;
    case "end": showing = false; $("viewer").hidden = true; return;
    case "close": close(); return;
  }
  render();
  hooks.report(page, doc.numPages);
}

function clamp(n) {
  return Math.max(1, Math.min(doc ? doc.numPages : 1, n | 0));
}

function setBlack(on) {
  black = on;
  $("vBlack").hidden = !on;
}

async function render() {
  if (!doc || !showing) return;
  const p = await doc.getPage(page);
  const base = p.getViewport({ scale: 1 });
  const scale = Math.min(window.innerWidth / base.width, window.innerHeight / base.height);
  const dpr = window.devicePixelRatio || 1;
  const vp = p.getViewport({ scale: scale * dpr });
  const c = $("vCanvas");
  c.width = Math.floor(vp.width);
  c.height = Math.floor(vp.height);
  c.style.width = Math.floor(vp.width / dpr) + "px";
  c.style.height = Math.floor(vp.height / dpr) + "px";
  if (renderTask) renderTask.cancel();
  renderTask = p.render({ canvasContext: c.getContext("2d"), viewport: vp });
  try { await renderTask.promise; } catch {}
  renderTask = null;
}
