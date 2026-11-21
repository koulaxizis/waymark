// Waymark Remote, browser Controller (flow B of the Doc).
//
// The page loads once from waymarked.site and then talks only to the Agent on
// the local network, over a WebRTC data channel (DTLS). The QR carries the
// Agent's addresses, its DTLS fingerprint and a one-time token. The page
// shows a return code with its own address; the presenter types it on the
// Agent, the Agent opens its firewall path and the page connects.
import { T, setLang, lang, applyI18n, errText } from "./i18n.js";
import { Sha256 } from "./sha256.js";

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const enc = new TextEncoder();
const VERSION = 1;
const CHUNK = 64 << 10;
const CROCK = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

// ---------- small helpers ----------
const store = {
  get(k, d) { try { return localStorage.getItem("wm." + k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem("wm." + k, v); } catch {} },
};
const b64u = (u8) => b64(u8).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const unb64u = (s) => unb64(s.replace(/-/g, "+").replace(/_/g, "/"));
function b64(u8) {
  let s = "";
  for (let i = 0; i < u8.length; i += 0x8000) s += String.fromCharCode.apply(null, u8.subarray(i, i + 0x8000));
  return btoa(s);
}
function unb64(s) {
  const bin = atob(s + "===".slice((s.length + 3) % 4));
  const o = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) o[i] = bin.charCodeAt(i);
  return o;
}
const cat = (...a) => { const o = new Uint8Array(a.reduce((x, y) => x + y.length, 0)); let i = 0; for (const x of a) { o.set(x, i); i += x.length; } return o; };
const hexFp = (u8) => Array.from(u8, (b) => b.toString(16).padStart(2, "0").toUpperCase()).join(":");
const fpBytes = (s) => Uint8Array.from(s.split(":"), (h) => parseInt(h, 16));
const eq = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
const rid = () => Array.from(crypto.getRandomValues(new Uint8Array(6)), (b) => b.toString(16).padStart(2, "0")).join("");

async function hmac(key, ...parts) {
  const k = await crypto.subtle.importKey("raw", key, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return new Uint8Array(await crypto.subtle.sign("HMAC", k, cat(...parts)));
}
function crockford(v, n) {
  let s = "";
  for (let i = 0; i < n; i++) { s = CROCK[Number(v & 31n)] + s; v >>= 5n; }
  return s;
}

function show(id) {
  for (const s of ["sStart", "sSetup", "sCode", "sSas", "sLive", "sEnd"]) $(s).hidden = s !== id;
}
function msg(id, text, kind = "err") {
  const m = $(id);
  m.className = "msg " + kind;
  m.textContent = text || "";
  m.hidden = !text;
}

// ---------- language and theme ----------
function setTheme(t) {
  document.documentElement.dataset.theme = t === "light" ? "light" : "dark";
  store.set("theme", t);
}
function initChrome() {
  setLang(store.get("lang", (navigator.language || "el").startsWith("el") ? "el" : "en"));
  setTheme(store.get("theme", "dark"));
  const relabel = () => {
    applyI18n();
    $("btnLang").textContent = lang() === "el" ? "EN" : "EL";
    if (agent) renderLive();
  };
  relabel();
  $("btnLang").onclick = () => { setLang(lang() === "el" ? "en" : "el"); store.set("lang", lang()); relabel(); };
  $("btnTheme").onclick = () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
}

// ---------- 1. the QR ----------
let A = null; // { ips, port, fp, token }

function parsePayload(s) {
  s = decodeURIComponent(String(s).trim());
  const i = s.indexOf("WM1.");
  if (i < 0) throw new Error(T("w_badQR"));
  // WM1.<ip,ip>.<port>.<fp>.<token>: IPs contain dots, so split from the right.
  const p = s.slice(i).split(/[\s#&?]/)[0].split(".");
  const token = unb64u(p.pop()), fp = unb64u(p.pop()), port = parseInt(p.pop(), 10);
  const ips = p.slice(1).join(".").split(",").filter((x) => /^\d+\.\d+\.\d+\.\d+$/.test(x));
  if (token.length !== 16 || fp.length !== 32 || !(port > 0 && port < 65536) || !ips.length) throw new Error(T("w_badQR"));
  return { ips, port, fp, token };
}

function takeQR(text) {
  try {
    A = parsePayload(text);
  } catch (e) {
    show("sStart");
    msg("startMsg", e.message);
    return;
  }
  // Keep the one-time token out of the address bar and of shared links.
  history.replaceState(null, "", location.pathname);
  $("nameInput").value = store.get("name", defaultName());
  const r = store.get("role", "operator");
  document.querySelectorAll('input[name=role]').forEach((x) => (x.checked = x.value === r));
  show("sSetup");
}

function defaultName() {
  const ua = navigator.userAgent;
  if (/Android/.test(ua)) return "Android";
  if (/iPhone|iPad/.test(ua)) return "iPhone";
  return "Browser";
}

let scanStream = null;
async function scan() {
  msg("startMsg", "");
  if (!("BarcodeDetector" in window)) { msg("startMsg", T("w_noScan"), "info"); return; }
  const v = $("video"), det = new BarcodeDetector({ formats: ["qr_code"] });
  scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
  v.srcObject = scanStream;
  v.hidden = false;
  await v.play();
  const tick = async () => {
    if (!scanStream) return;
    const r = await det.detect(v).catch(() => []);
    const hit = r.find((x) => x.rawValue.includes("WM1."));
    if (hit) { stopScan(); takeQR(hit.rawValue); return; }
    requestAnimationFrame(tick);
  };
  tick();
}
function stopScan() {
  if (scanStream) scanStream.getTracks().forEach((t) => t.stop());
  scanStream = null;
  $("video").hidden = true;
}

// ---------- 2-3. offer, return code ----------
let pc = null, dc = null, myFp = null, myIP = null, myPort = 0, myName = "", myRole = "operator";

async function prepare() {
  myName = $("nameInput").value.trim() || defaultName();
  myRole = document.querySelector("input[name=role]:checked")?.value || "operator";
  store.set("name", myName);
  store.set("role", myRole);
  if (!window.RTCPeerConnection) { msg("setupMsg", T("w_noWebRTC")); return; }
  $("btnSetup").disabled = true;
  show("sCode");
  $("retCode").textContent = "…";
  // Camera permission makes Chrome show real local IPs instead of .local
  // names. The stream is never displayed or recorded and stops at once.
  let st = null;
  try { st = await navigator.mediaDevices.getUserMedia({ video: true }); } catch {}
  try { await makeOffer(); } finally { if (st) st.getTracks().forEach((t) => t.stop()); $("btnSetup").disabled = false; }
}

async function makeOffer() {
  const ufrag = b64u(await hmac(A.token, enc.encode("ice-ufrag"))).slice(0, 8);
  const pwd = b64u(await hmac(A.token, enc.encode("ice-pwd"))).slice(0, 24);
  pc = new RTCPeerConnection({ iceServers: [] });
  dc = pc.createDataChannel("waymark", { negotiated: true, id: 0 });
  dc.onopen = onOpen;
  // Messages are handled in order: the hello-ack check is async and the
  // confirm may already be behind it.
  let q = Promise.resolve();
  dc.onmessage = (ev) => { q = q.then(() => onMsg(JSON.parse(ev.data))).catch((e) => console.error(e)); };
  dc.onclose = () => ended("w_ended");
  pc.onconnectionstatechange = () => { if (pc && pc.connectionState === "failed") ended("w_ended"); };
  // The Agent is an ICE-lite offerer; this SDP is rebuilt from the QR.
  const offer = [
    "v=0", "o=- 1 1 IN IP4 127.0.0.1", "s=-", "t=0 0", "a=group:BUNDLE 0", "a=ice-lite",
    "m=application 9 UDP/DTLS/SCTP webrtc-datachannel", "c=IN IP4 0.0.0.0",
    "a=ice-ufrag:" + ufrag, "a=ice-pwd:" + pwd, "a=fingerprint:sha-256 " + hexFp(A.fp),
    "a=setup:actpass", "a=mid:0", "a=sctp-port:5000", "a=max-message-size:262144", "",
  ].join("\r\n");
  await pc.setRemoteDescription({ type: "offer", sdp: offer });
  const answer = await pc.createAnswer();
  const gathered = new Promise((res) => {
    pc.onicegatheringstatechange = () => pc.iceGatheringState === "complete" && res();
    setTimeout(res, 3000);
  });
  await pc.setLocalDescription(answer);
  await gathered;
  const sdp = pc.localDescription.sdp;
  myFp = fpBytes(/a=fingerprint:sha-256 ([0-9A-F:]+)/i.exec(sdp)[1]);
  const cands = [...sdp.matchAll(/a=candidate:\S+ 1 udp \d+ (\S+) (\d+) typ host/gi)].map((m) => ({ ip: m[1], port: +m[2] }));
  const v4 = cands.filter((c) => /^\d+\.\d+\.\d+\.\d+$/.test(c.ip));
  // Prefer the address on the Agent's subnet.
  const score = (ip) => Math.max(...A.ips.map((a) => { const x = a.split("."), y = ip.split("."); let n = 0; while (n < 4 && x[n] === y[n]) n++; return n; }));
  v4.sort((a, b) => score(b.ip) - score(a.ip));
  if (v4.length) {
    myIP = v4[0].ip;
    myPort = v4[0].port;
    await showCode();
  } else if (cands.length) {
    myPort = cands[0].port;
    $("manualIP").hidden = false;
  } else {
    msg("codeMsg", T("w_noWebRTC"));
  }
}

async function showCode() {
  const ip = myIP.split(".").map(Number);
  if (ip.length !== 4 || ip.some((x) => !(x >= 0 && x <= 255))) return;
  const s = await hmac(A.token, enc.encode("wm-return"), Uint8Array.from(ip), Uint8Array.of(myPort >> 8, myPort & 255));
  const chk = ((s[0] << 8) | s[1]) >> 4;
  let v = (BigInt(((ip[0] << 24) >>> 0) + (ip[1] << 16) + (ip[2] << 8) + ip[3]) << 16n) | BigInt(myPort);
  v = (v << 12n) | BigInt(chk);
  const c = crockford(v, 12);
  $("retCode").textContent = c.slice(0, 4) + "-" + c.slice(4, 8) + "-" + c.slice(8);
  $("manualIP").hidden = true;
  $("btnConnect").disabled = false;
}

let openTimer = null;
async function connect() {
  $("btnConnect").disabled = true;
  msg("codeMsg", T("connecting"), "info");
  for (const ip of A.ips) {
    try {
      await pc.addIceCandidate({ candidate: `candidate:1 1 udp 2130706431 ${ip} ${A.port} typ host`, sdpMid: "0", sdpMLineIndex: 0 });
    } catch {}
  }
  clearTimeout(openTimer);
  openTimer = setTimeout(() => {
    if (dc.readyState !== "open") { msg("codeMsg", T("w_notOpen")); $("btnConnect").disabled = false; }
  }, 20000);
}

// ---------- 4. hello and the check number ----------
let phase = "pair"; // pair, live, ended
let acked = false;  // the Agent proved it holds the token
async function onOpen() {
  clearTimeout(openTimer);
  const mac = await hmac(A.token, enc.encode("wm-b-hello"), myFp, A.fp);
  send({ t: "hello", v: VERSION, fp: b64u(myFp), mac: b64u(mac), name: myName, role: myRole });
}

async function onAck(m) {
  const want = await hmac(A.token, enc.encode("wm-b-ack"), myFp, A.fp);
  if (!m.mac || !eq(unb64u(m.mac), want)) { ended("w_badAgent"); return; }
  acked = true;
  const s = await hmac(A.token, enc.encode("sas"), myFp, A.fp);
  $("sasNum").textContent = String((((s[0] << 24) >>> 0) + (s[1] << 16) + (s[2] << 8) + s[3]) % 10000).padStart(4, "0");
  show("sSas");
}

async function onMsg(m) {
  lastSeen = Date.now();
  if (phase === "pair") {
    if (m.t === "hello-ack") await onAck(m);
    else if (m.t === "confirm" && acked) goLive(m);
    else if (m.t === "reject") ended("w_rejected");
    else if (m.t === "err") ended(m.code === "VERSION" ? "errVersion" : null, m.err);
    return;
  }
  switch (m.t) {
    case "ok":
    case "error": {
      const p = pending.get(m.id);
      if (!p) return;
      if (m.t === "error" || !p.stream) { pending.delete(m.id); clearTimeout(p.timer); }
      // An error during a download ends that download.
      if (m.t === "error" && p.started) p.stream({ error: m });
      else p.resolve(m);
      if (m.t === "ok" && p.stream) p.started = true;
      return;
    }
    case "file.data": {
      const p = pending.get(m.id);
      if (p && p.stream) p.stream(m.data);
      return;
    }
    case "state":
      agent.state = m.data;
      renderLive();
      return;
    case "event":
      if (m.data?.kind === "file.new") loadFiles();
      if (m.data?.kind === "control.taken") cmdMsg(T("takenBySupervisor", m.data.by?.name || ""));
      return;
    case "bye":
      ended("w_ended");
  }
}

function send(o) { dc.send(JSON.stringify(o)); }

// ---------- 5. live ----------
let agent = null; // { id, role, state }
let lastSeen = 0, latency = 0, seq = 0, wakeLock = null, pinger = null;
const pending = new Map();

function goLive(m) {
  phase = "live";
  agent = { id: m.id, role: m.role, state: null };
  show("sLive");
  wireLive();
  call("state.get").then((r) => { if (r.t === "ok") { agent.state = r.data; renderLive(); } });
  pinger = setInterval(async () => {
    if (dc.readyState !== "open") return;
    const t0 = performance.now();
    const r = await call("ping", { t0: Date.now() }, 4000);
    if (r.t === "ok") latency = Math.round(performance.now() - t0);
    renderTop();
  }, 2000);
  keepAwake();
  document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && keepAwake());
}

async function keepAwake() {
  try { if (phase === "live" && "wakeLock" in navigator) wakeLock = await navigator.wakeLock.request("screen"); } catch {}
}

// call sends one command. The answer is the raw message: t is "ok" or
// "error". A presentation command carries a sequence number so the Agent
// drops an old one that arrives late.
function call(cmd, args, timeout = 15000, stream) {
  return new Promise((resolve) => {
    if (!dc || dc.readyState !== "open") { resolve({ t: "error", code: "LINK" }); return; }
    const id = rid();
    const m = { v: VERSION, t: "cmd", id, cmd };
    if (args !== undefined && args !== null) m.args = args;
    if (cmd.startsWith("pres.")) m.seq = ++seq;
    const timer = setTimeout(() => { if (pending.delete(id)) resolve({ t: "error", code: "LINK" }); }, timeout);
    pending.set(id, { resolve, stream, timer });
    try { send(m); } catch { pending.delete(id); clearTimeout(timer); resolve({ t: "error", code: "LINK" }); }
  });
}

const isHolder = () => !!(agent?.state?.holder && agent.state.holder.id === agent.id);

function renderTop() {
  if (phase !== "live") { $("topStatus").innerHTML = ""; return; }
  const stale = Date.now() - lastSeen > 6000;
  $("topStatus").innerHTML = stale
    ? `<span class="status wait">${esc(T("w_lost"))}</span>`
    : `<span class="status ok">${esc(T("online"))} · <span class="num">${latency} ms</span></span>`;
}

function slideText(s) {
  if (!s || !s.file) return "–";
  if (!s.slide) return s.slides ? `? / ${s.slides}` : "?";
  return `${s.slide} / ${s.slides || "?"}`;
}
function statusBadge(st) {
  const cls = { idle: "off", open: "wait", showing: "ok", black: "wait" }[st] || "off";
  return `<span class="status ${cls}">${esc(T("st_" + (st || "idle")))}</span>`;
}

function renderLive() {
  if (!agent || phase !== "live") return;
  const s = agent.state || {};
  $("pName").textContent = s.device || "Agent";
  $("pInfo").innerHTML = esc(T("role_" + agent.role)) + (s.holder ? ` · ${esc(T("controlBy", s.holder.name))}` : "");
  const canPres = agent.role !== "secretariat";
  const holder = isHolder();
  $("btnTake").hidden = !canPres || holder;
  $("btnRelease").hidden = !canPres || !holder;
  $("tabPres").hidden = !canPres;
  if (!canPres && $("paneFiles").hidden) tab("files");
  $("cFile").textContent = s.file ? s.file : T("noFile");
  $("cStatus").innerHTML = statusBadge(s.status) + (s.app ? ` · ${esc(T("app_" + s.app))}` : "");
  $("cSlide").textContent = slideText(s);
  document.querySelectorAll("#panePres .pad button").forEach((b) => (b.disabled = !holder));
  $("bBlack").textContent = s.status === "black" ? T("unblack") : T("black");
  renderTop();
  if (!$("paneFiles").hidden) renderFiles();
}

function tab(which) {
  $("tabPres").setAttribute("aria-selected", which === "pres");
  $("tabFiles").setAttribute("aria-selected", which === "files");
  $("panePres").hidden = which !== "pres";
  $("paneFiles").hidden = which !== "files";
}

let cmdTimer = null;
function cmdMsg(text) {
  $("cmdMsg").textContent = text || "";
  $("cmdMsg").hidden = !text;
  clearTimeout(cmdTimer);
  if (text) cmdTimer = setTimeout(() => ($("cmdMsg").hidden = true), 8000);
}
function handle(r) {
  cmdMsg(r.t === "error" ? errText(r.code, r.err) : "");
  return r;
}
let fileTimer = null;
function fileResult(r) {
  const m = $("fileMsg");
  if (r.t === "error") {
    m.textContent = errText(r.code, r.err);
    m.hidden = false;
    clearTimeout(fileTimer);
    fileTimer = setTimeout(() => (m.hidden = true), 8000);
  }
  return r;
}

let wired = false;
function wireLive() {
  if (wired) return;
  wired = true;
  $("btnTake").onclick = async () => { handle(await call("control.take")); };
  $("btnRelease").onclick = async () => { handle(await call("control.release")); };
  $("tabPres").onclick = () => tab("pres");
  $("tabFiles").onclick = () => { tab("files"); loadFiles(); };
  const cmd = (c, args) => async () => handle(await call(c, args));
  $("bPrev").onclick = cmd("pres.prev");
  $("bNext").onclick = cmd("pres.next");
  $("bStart").onclick = cmd("pres.start");
  $("bEnd").onclick = cmd("pres.end");
  $("bClose").onclick = cmd("pres.close");
  $("bBlack").onclick = async () => handle(await call("pres.black", { on: agent.state?.status !== "black" }));
  $("bGoto").onclick = async () => {
    const n = parseInt($("gotoNum").value, 10);
    if (n > 0) handle(await call("pres.goto", { slide: n }));
  };
  // Clicker keys on a laptop browser: arrows, PageUp/PageDown, B, Esc, F5.
  document.addEventListener("keydown", (e) => {
    if (phase !== "live" || !$("paneFiles").hidden || e.target.matches("input,select,textarea") || document.querySelector("dialog[open]")) return;
    const map = { ArrowRight: "pres.next", PageDown: "pres.next", " ": "pres.next", ArrowLeft: "pres.prev", PageUp: "pres.prev", Escape: "pres.end", F5: "pres.start" };
    if (map[e.key]) { e.preventDefault(); cmd(map[e.key])(); }
    else if (e.key === "b" || e.key === "B" || e.key === ".") { e.preventDefault(); $("bBlack").click(); }
  });
  $("fUpload").onchange = (e) => { uploadList([...e.target.files].map((f) => ({ file: f, rel: f.name }))); e.target.value = ""; };
  $("fUploadDir").onchange = (e) => { uploadList([...e.target.files].map((f) => ({ file: f, rel: f.webkitRelativePath || f.name }))); e.target.value = ""; };
  if (!("webkitdirectory" in document.createElement("input")) || /Android|iPhone|iPad/.test(navigator.userAgent)) $("fUploadDirBtn").hidden = true;
  $("fRefresh").onclick = loadFiles;
  $("fMkdir").onclick = async () => {
    const n = await ask(T("mkdirName"), "");
    if (n) { fileResult(await call("file.mkdir", { path: join(curDir, n) })); loadFiles(); }
  };
  const dz = $("dropZone");
  dz.addEventListener("dragover", (e) => { e.preventDefault(); dz.classList.add("over"); });
  dz.addEventListener("dragleave", () => dz.classList.remove("over"));
  dz.addEventListener("drop", (e) => {
    e.preventDefault();
    dz.classList.remove("over");
    uploadList([...e.dataTransfer.files].map((f) => ({ file: f, rel: f.name })));
  });
  if (/Android|iPhone|iPad/.test(navigator.userAgent)) dz.hidden = true;
  $("cFiles").addEventListener("click", onFileAction);
  $("btnBye").onclick = () => { try { send({ v: VERSION, t: "bye" }); } catch {} setTimeout(() => ended("w_ended"), 150); };
}

function ask(text, value) {
  return new Promise((resolve) => {
    const d = $("askDlg"), inp = $("askInput");
    $("askText").textContent = text;
    inp.hidden = value === undefined;
    inp.value = value ?? "";
    const done = (v) => { d.close(); resolve(v); };
    $("askYes").onclick = () => done(value === undefined ? true : inp.value.trim());
    $("askNo").onclick = () => done(null);
    d.oncancel = () => resolve(null);
    inp.onkeydown = (e) => { if (e.key === "Enter") $("askYes").click(); };
    d.showModal();
    if (!inp.hidden) inp.select();
  });
}

// ---------- files ----------
let files = [], curDir = "";
const join = (a, b) => (a ? a + "/" + b : b);
const ICON_DIR = `<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>`;
const ICON_FILE = `<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/></svg>`;

function fmtSize(n) {
  if (n == null) return "";
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(0) + " KB";
  if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
  return (n / 1073741824).toFixed(2) + " GB";
}

async function loadFiles() {
  const r = await call("file.list");
  if (r.t === "error") { fileResult(r); return; }
  files = r.data || [];
  renderFiles();
}

function renderFiles() {
  const inDir = files.filter((e) => (e.path.includes("/") ? e.path.slice(0, e.path.lastIndexOf("/")) : "") === curDir);
  $("curDir").textContent = "/" + curDir;
  const locale = lang() === "el" ? "el-GR" : "en-GB";
  const rows = inDir.map((e) => {
    const name = e.path.split("/").pop();
    const openable = /\.(pptx|ppt|key|odp|pdf)$/i.test(name);
    return `<tr data-path="${esc(e.path)}" data-dir="${e.dir ? 1 : 0}">
      <td><span class="fname">${e.dir ? ICON_DIR : ICON_FILE}<span>${e.dir ? `<a href="#" data-act="cd">${esc(name)}</a>` : esc(name)}</span>${e.open ? `<span class="open-tag">${esc(T("inUse"))}</span>` : ""}</span></td>
      <td class="small muted num">${e.dir ? "" : fmtSize(e.size)}</td>
      <td class="small muted num">${new Date(e.mtime * 1000).toLocaleString(locale, { hour12: false, dateStyle: "short", timeStyle: "short" })}</td>
      <td class="act">
        ${!e.dir && openable && agent.role !== "secretariat" ? `<button class="btn ghost small" type="button" data-act="open">${esc(T("openPres"))}</button>` : ""}
        ${e.dir ? "" : `<button class="btn ghost small" type="button" data-act="get" title="${esc(T("download"))}" aria-label="${esc(T("download"))}">↓</button>`}
        <button class="btn ghost small" type="button" data-act="ren" title="${esc(T("rename"))}" aria-label="${esc(T("rename"))}">✎</button>
        <button class="btn ghost small" type="button" data-act="del" title="${esc(T("delete"))}" aria-label="${esc(T("delete"))}">🗑</button></td></tr>`;
  });
  const up = curDir ? `<tr><td colspan="4"><button class="btn ghost small" type="button" data-act="up">↑ ${esc(T("up"))}</button></td></tr>` : "";
  $("cFiles").innerHTML = up + (rows.join("") || `<tr><td class="muted">${esc(T("emptyFolder"))}</td></tr>`);
}

async function openPres(path) {
  if (!isHolder()) { const r = await call("control.take"); if (r.t === "error") return fileResult(r); }
  const r = await call("pres.open", { path });
  if (fileResult(r).t !== "error") tab("pres");
}

async function onFileAction(e) {
  const el = e.target.closest("[data-act]");
  if (!el) return;
  e.preventDefault();
  if (el.dataset.act === "up") { curDir = curDir.includes("/") ? curDir.slice(0, curDir.lastIndexOf("/")) : ""; renderFiles(); return; }
  const tr = el.closest("tr");
  const path = tr.dataset.path;
  switch (el.dataset.act) {
    case "cd": curDir = path; renderFiles(); return;
    case "open": return openPres(path);
    case "get": return download(path);
    case "ren": {
      const old = path.split("/").pop();
      const n = await ask(T("renameTo"), old);
      if (n && n !== old) { fileResult(await call("file.rename", { from: path, to: join(curDir, n) })); loadFiles(); }
      return;
    }
    case "del":
      if (await ask(T("deleteConfirm", path.split("/").pop()))) { fileResult(await call("file.delete", { path })); loadFiles(); }
  }
}

// Uploads go one file at a time, chunks pipelined a few deep. The Agent
// checks the SHA-256 at the end, so the whole file is hashed first.
async function uploadList(list) {
  for (const it of list) {
    const ok = await upload(it.file, join(curDir, it.rel.replace(/\\/g, "/")));
    if (!ok) break;
  }
  loadFiles();
}

async function upload(file, rel) {
  const pr = progress(true, rel, file.size);
  const h = new Sha256();
  for (let off = 0; off < file.size; off += 4 << 20) {
    h.update(new Uint8Array(await file.slice(off, off + (4 << 20)).arrayBuffer()));
  }
  const sum = h.hex();
  const r = await call("file.offer", { path: rel, size: file.size, sha256: sum });
  if (r.t === "error") { pr.fail(errText(r.code, r.err)); return false; }
  const { id, offset } = r.data;
  let off = offset || 0;
  const inflight = [];
  let failed = null;
  while (off < file.size && !failed) {
    const data = new Uint8Array(await file.slice(off, off + CHUNK).arrayBuffer());
    const at = off;
    off += data.length;
    inflight.push(call("file.chunk", { id, offset: at, data: b64(data) }).then((c) => {
      if (c.t === "error") failed = c;
      else pr.set(at + data.length);
    }));
    if (inflight.length >= 8) await inflight.shift();
  }
  await Promise.all(inflight);
  if (failed) { pr.fail(errText(failed.code, failed.err)); return false; }
  const d = await call("file.done", { id });
  if (d.t === "error") { pr.fail(errText(d.code, d.err)); return false; }
  pr.ok();
  return true;
}

async function download(path) {
  const name = path.split("/").pop();
  const parts = [];
  const h = new Sha256();
  let size = 0, sha = "", got = 0, pr = null;
  const done = new Promise((resolve) => {
    let finish = null;
    const first = call("file.get", { path, offset: 0 }, 30000, (d) => {
      if (d.error) { resolve(d.error); return; }
      const b = unb64(d.data || "");
      parts.push(b);
      h.update(b);
      got += b.length;
      if (pr) pr.set(got);
      if (d.last) finish && finish();
    });
    first.then((r) => {
      if (r.t === "error") { resolve(r); return; }
      size = r.data.size;
      sha = r.data.sha256;
      pr = progress(false, path, size);
      finish = () => {
        const p = pending.get(r.id);
        if (p) { clearTimeout(p.timer); pending.delete(r.id); }
        resolve(r);
      };
      if (size === 0) finish();
      // The reply stays pending for the data; renew its timeout as chunks come.
      const p = pending.get(r.id);
      if (p) {
        const watch = () => {
          clearTimeout(p.timer);
          const last = got;
          p.timer = setTimeout(() => {
            if (got === last && pending.delete(r.id)) resolve({ t: "error", code: "LINK" });
            else watch();
          }, 15000);
        };
        watch();
      }
    });
  });
  const r = await done;
  if (r.t === "error") { (pr || progress(false, path, 0)).fail(errText(r.code, r.err)); return; }
  if (h.hex() !== sha) { pr.fail(errText("HASH_MISMATCH")); return; }
  pr.ok();
  const url = URL.createObjectURL(new Blob(parts));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
}

function progress(up, path, total) {
  const el = document.createElement("div");
  el.className = "small";
  el.innerHTML = `<div class="row" style="justify-content:space-between"><span>${up ? "↑" : "↓"} ${esc(path)}</span><span class="st num">0%</span></div><div class="bar"><i></i></div>`;
  $("progress").prepend(el);
  const st = el.querySelector(".st"), bar = el.querySelector(".bar > i");
  const remove = (ms) => setTimeout(() => el.remove(), ms);
  return {
    set(n) { const p = total ? Math.floor((n / total) * 100) : 100; st.textContent = p + "%"; bar.style.width = p + "%"; },
    ok() { bar.style.width = "100%"; st.className = "status ok"; st.textContent = T("hashOk"); remove(4000); },
    fail(t) { st.className = "status err"; st.textContent = t || ""; remove(15000); },
  };
}

// ---------- 6. end ----------
function ended(key, detail) {
  if (phase === "ended") return;
  phase = "ended";
  clearInterval(pinger);
  clearTimeout(openTimer);
  for (const [, p] of pending) { clearTimeout(p.timer); p.resolve({ t: "error", code: "LINK" }); }
  pending.clear();
  try { wakeLock && wakeLock.release(); } catch {}
  try { pc && pc.close(); } catch {}
  A = null;
  $("topStatus").innerHTML = "";
  $("endTitle").textContent = key ? T(key) : detail || T("w_ended");
  show("sEnd");
}

function restart() {
  phase = "pair";
  acked = false;
  agent = null;
  pc = dc = null;
  $("btnConnect").disabled = true;
  msg("codeMsg", "");
  show("sStart");
}

// ---------- boot ----------
function boot() {
  initChrome();
  $("btnScan").onclick = () => scan().catch((e) => msg("startMsg", e.message));
  $("btnPaste").onclick = () => takeQR($("pasteBox").value);
  $("btnSetup").onclick = () => prepare().catch((e) => msg("codeMsg", e.message));
  $("btnIP").onclick = async () => { myIP = $("ipInput").value.trim(); await showCode(); };
  $("btnConnect").onclick = () => connect().catch((e) => msg("codeMsg", e.message));
  $("btnAgain").onclick = restart;
  setInterval(() => phase === "live" && renderTop(), 1000);
  if (location.hash.includes("WM1.")) takeQR(location.hash.slice(1));
  else show("sStart");
}

boot();
