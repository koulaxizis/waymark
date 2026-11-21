// Waymark Remote window. Plain JS, no framework. Talks to Go through the
// Wails bindings (window.go.main.App) and events (window.runtime).
import { T, setLang, lang, applyI18n, errText } from "./i18n.js";
import * as viewer from "./viewer.js";

const go = () => window.go.main.App;
const on = (name, fn) => window.runtime.EventsOn(name, fn);
const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

let info = null;

// ---------- start ----------
async function boot() {
  info = await go().GetInfo();
  setLang(info.config.lang || "el");
  setTheme(info.config.theme || "dark");
  applyI18n();
  wireCommon();
  const mode = info.running || info.config.mode;
  if (info.running) {
    showMode(info.running);
  } else if (mode === "agent" || (mode === "controller" && info.config.role)) {
    try {
      await go().Start(mode, info.config.role || "", info.config.name);
      showMode(mode);
    } catch (e) {
      showSetup(String(e));
    }
  } else {
    showSetup();
  }
}

function setTheme(t) {
  document.documentElement.dataset.theme = t === "light" ? "light" : "dark";
}

function showSetup(err) {
  $("setup").hidden = false;
  $("setupName").value = info.config.name || "";
  const sync = () => { $("roleBox").hidden = document.querySelector("input[name=mode]:checked").value !== "controller"; };
  document.querySelectorAll("input[name=mode]").forEach((r) => r.addEventListener("change", sync));
  sync();
  if (err) { $("setupErr").textContent = err; $("setupErr").hidden = false; }
  $("btnStart").onclick = async () => {
    const mode = document.querySelector("input[name=mode]:checked").value;
    const role = mode === "controller" ? document.querySelector("input[name=role]:checked").value : "";
    try {
      await go().Start(mode, role, $("setupName").value);
      info = await go().GetInfo();
      $("setup").hidden = true;
      showMode(mode);
    } catch (e) {
      $("setupErr").textContent = String(e);
      $("setupErr").hidden = false;
    }
  };
}

function showMode(mode) {
  $("setup").hidden = true;
  if (mode === "agent") initAgent();
  else initController();
}

// ---------- common: settings, updates, log ----------
function wireCommon() {
  $("btnSettings").onclick = openSettings;
  $("btnSaveSettings").onclick = async () => {
    const c = { ...info.config, lang: $("sLang").value, theme: $("sTheme").value, name: $("sName").value, updateCheck: $("sUpdate").checked };
    await go().SaveSettings(c);
    info = await go().GetInfo();
    setLang(c.lang); setTheme(c.theme); applyI18n(); refreshLabels();
    $("settingsDlg").close();
  };
  $("btnResetMode").onclick = async () => {
    await go().ResetMode();
    $("checkMsg").textContent = T("resetDone");
  };
  $("btnCheckNow").onclick = async () => {
    $("checkMsg").textContent = "…";
    try {
      const u = await go().CheckUpdateNow();
      if (u) { showUpdate(u); $("checkMsg").textContent = ""; }
      else $("checkMsg").textContent = T("upToDate");
    } catch { $("checkMsg").textContent = T("noInternet"); }
  };
  on("update", showUpdate);
  on("log", (line) => {
    for (const id of ["aLog", "cLog"]) {
      const el = $(id);
      el.textContent += line + "\n";
      el.scrollTop = el.scrollHeight;
    }
  });
}

function openSettings() {
  $("sLang").value = lang();
  $("sTheme").value = document.documentElement.dataset.theme;
  $("sName").value = info.config.name || "";
  $("sUpdate").checked = !!info.config.updateCheck;
  $("sVersion").textContent = info.version;
  $("checkMsg").textContent = "";
  $("settingsDlg").showModal();
}

let pendingUpdate = null;
function showUpdate(u) {
  // Never interrupt a running show on the Agent.
  if (!viewer.isShowing()) {
    pendingUpdate = u;
    $("updateText").textContent = T("updateAvail", u.version);
    $("updateBanner").hidden = false;
  }
}
$("btnUpdate").onclick = () => pendingUpdate && go().OpenURL(pendingUpdate.url);
$("btnUpdateLater").onclick = () => { $("updateBanner").hidden = true; };

let refreshLabels = () => {};

// ---------- Agent ----------
let aState = null;

function initAgent() {
  $("agentView").hidden = false;
  $("btnDisconnectAll").hidden = false;
  refreshLabels = () => { $("modeLabel").textContent = T("modeAgent") + " · " + (info.config.name || ""); renderAgentState(); };
  refreshLabels();
  const code = $("codeInput");
  code.addEventListener("input", () => {
    const raw = code.value.toUpperCase().replace(/[^0-9A-Z]/g, "").slice(0, 16);
    code.value = raw.match(/.{1,4}/g)?.join("-") ?? "";
  });
  code.addEventListener("keydown", (e) => { if (e.key === "Enter") connect(); });
  $("btnConnect").onclick = connect;
  $("btnDisconnectAll").onclick = () => go().AgentDisconnectAll();
  $("btnOpenFolder").onclick = () => go().AgentOpenFolder();
  $("btnChangeFolder").onclick = async () => {
    const d = await go().AgentChooseFolder();
    if (d) agentMsg(T("folderNext"), "info");
  };
  $("btnInvite").onclick = showInvite;
  $("btnInviteClose").onclick = closeInvite;
  // For a laptop browser without a camera: send the link another way.
  $("btnCopyLink").onclick = async () => {
    if (await window.runtime.ClipboardSetText($("phoneQR").alt)) phoneMsg(T("phoneCopied"), "ok");
  };
  $("btnAcceptPhone").onclick = acceptPhone;
  $("btnResume").onclick = resumePhone;
  $("resumeInput").addEventListener("keydown", (e) => { if (e.key === "Enter") resumePhone(); });
  $("returnInput").addEventListener("keydown", (e) => { if (e.key === "Enter") acceptPhone(); });
  on("agent:prompt", showPrompt);
  on("agent:prompt-done", () => $("promptDlg").open && $("promptDlg").close());
  on("agent:state", (s) => { aState = s; renderAgentState(); });
  on("agent:event", (e) => { if (e.kind === "file.new") loadAgentFiles(); });
  $("btnAccept").onclick = () => { go().AgentAnswer(true, $("promptRole").value); $("promptDlg").close(); };
  $("btnReject").onclick = () => { go().AgentAnswer(false, ""); $("promptDlg").close(); };
  $("promptDlg").addEventListener("cancel", () => go().AgentAnswer(false, ""));
  viewer.init({ report: (n, total) => go().ViewerReport(n, total), localEnd: () => go().ViewerLocalEnd() });
  on("viewer:open", (m) => viewer.open("/viewer/file?t=" + m.t));
  on("viewer:cmd", (m) => viewer.command(m.cmd, m.slide));
  go().AgentState().then((s) => { aState = s; renderAgentState(); });
  loadAgentFiles();
  setInterval(loadAgentFiles, 5000);
}

function agentMsg(text, kind = "err") {
  const m = $("connectMsg");
  m.className = "msg " + kind;
  m.textContent = text;
  m.hidden = !text;
}

async function connect() {
  const code = $("codeInput").value.trim();
  if (!code) return;
  $("btnConnect").disabled = true;
  agentMsg(T("connecting"), "info");
  try {
    await go().AgentConnect(code);
    agentMsg(T("connectedOk"), "ok");
    $("codeInput").value = "";
  } catch (e) {
    agentMsg(friendlyPairError(String(e)));
  } finally {
    $("btnConnect").disabled = false;
  }
}

let inviteTimer = null;

function phoneMsg(text, kind = "err") {
  const m = $("phoneMsg");
  m.className = "msg " + kind;
  m.textContent = text;
  m.hidden = !text;
}

async function showInvite() {
  phoneMsg("");
  try {
    const inv = await go().AgentInvite();
    $("phoneQR").src = inv.qr;
    $("phoneQR").alt = inv.url;
    $("phoneBox").hidden = false;
    $("phoneStart").hidden = true;
    $("returnInput").value = "";
    $("returnInput").focus();
    clearInterval(inviteTimer);
    const tick = () => {
      const left = Math.round((inv.expires - Date.now()) / 60000);
      if (left <= 0) { closeInvite(); phoneMsg(T("phoneExpired"), "info"); return; }
      $("phoneExpires").textContent = T("phoneValid", left);
    };
    tick();
    inviteTimer = setInterval(tick, 15000);
  } catch (e) {
    phoneMsg(String(e));
  }
}

function closeInvite() {
  clearInterval(inviteTimer);
  go().AgentCloseInvite();
  $("phoneBox").hidden = true;
  $("phoneStart").hidden = false;
}

function pathLabel(k) {
  return T("pathNote", T("path_" + (["wired", "wifi", "cell"].includes(k) ? k : "other")));
}

async function resumePhone() {
  const code = $("resumeInput").value.trim();
  if (!code) return;
  try {
    await go().AgentAcceptPhone(code);
    $("resumeInput").value = "";
    phoneMsg(T("w_back"), "info");
  } catch (e) {
    phoneMsg(friendlyPhoneError(String(e)));
  }
}

async function acceptPhone() {
  const code = $("returnInput").value.trim();
  if (!code) return;
  $("btnAcceptPhone").disabled = true;
  phoneMsg(T("phoneWaiting"), "info");
  try {
    await go().AgentAcceptPhone(code);
    clearInterval(inviteTimer);
    $("phoneBox").hidden = true;
    $("phoneStart").hidden = false;
    phoneMsg(T("connectedOk"), "ok");
  } catch (e) {
    phoneMsg(friendlyPhoneError(String(e)));
  } finally {
    $("btnAcceptPhone").disabled = false;
  }
}

function friendlyPhoneError(e) {
  if (/return code|check|invalid character|expected/.test(e)) return T("errReturnCode");
  if (/nothing arrived/.test(e)) return T("errPhoneReach");
  if (/does not hold|certificate/.test(e)) return T("errPhoneQR");
  if (/expired|was used/.test(e)) return T("phoneExpired");
  if (/did not accept/.test(e)) return T("errRejected");
  if (/page protocol/.test(e)) return T("errVersion");
  return e;
}

function friendlyPairError(e) {
  if (/expected 12 or 16|invalid character/.test(e)) return T("errCodeFormat");
  if (/cannot reach/.test(e)) return T("errReach");
  if (/wrong code|key confirmation|no answer/.test(e)) return T("errWrongCode");
  if (/did not accept/.test(e)) return T("errRejected");
  if (/same version/.test(e)) return T("errVersion");
  return e;
}

function showPrompt(p) {
  $("promptWho").textContent = T("promptWho", p.name, T("role_" + p.role));
  $("promptSas").textContent = p.sas;
  const sel = $("promptRole");
  const order = ["secretariat", "operator", "supervisor"];
  sel.innerHTML = order.slice(0, order.indexOf(p.role) + 1).map((r) => `<option value="${r}">${esc(T("role_" + r))}</option>`).join("");
  sel.value = p.role;
  if (!$("promptDlg").open) $("promptDlg").showModal();
}

function renderAgentState() {
  const s = aState;
  if (!s) return;
  const peers = s.peers || [];
  $("topStatus").innerHTML = peers.length
    ? `<span class="status ok">${esc(T("statusConnected", peers.length))}</span>`
    : `<span class="status off">${esc(T("statusWaiting"))}</span>`;
  const waiting = s.waiting || [];
  const hasKey = (p) => s.holder && s.holder.id === p.id ? ` · <span class="status ok">${esc(T("hasControl"))}</span>` : "";
  $("peerList").innerHTML = peers.length || waiting.length
    ? peers.map((p) => `<div class="row" style="justify-content:space-between">
        <span><b>${esc(p.name)}</b> · ${esc(T("role_" + p.role))}${p.path ? ` · ${esc(pathLabel(p.path))}` : ""}${hasKey(p)}</span>
        <button class="btn ghost small" data-drop="${esc(p.id)}">${esc(T("disconnect"))}</button></div>`).join("") +
      waiting.map((p) => `<div class="row" style="justify-content:space-between">
        <span><b>${esc(p.name)}</b> · <span class="status wait">${esc(T("w_back"))}</span>${hasKey(p)}</span>
        <button class="btn ghost small" data-drop="${esc(p.id)}">${esc(T("disconnect"))}</button></div>`).join("")
    : `<span class="muted">${esc(T("nobody"))}</span>`;
  $("peerList").querySelectorAll("[data-drop]").forEach((b) => (b.onclick = () => go().AgentDisconnect(b.dataset.drop)));
  // A phone that cannot get back through this laptop's firewall shows a
  // code: it goes in this box.
  $("resumeBox").hidden = !waiting.some((p) => p.browser);
  $("folderPath").textContent = s.folder;
  $("aFile").textContent = s.file || T("noFile");
  $("aApp").textContent = s.app ? T("app_" + s.app) : "";
  $("aSlide").textContent = slideText(s);
  $("aStatus").innerHTML = statusBadge(s.status);
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

async function loadAgentFiles() {
  try {
    const l = await go().AgentFiles();
    $("aFiles").innerHTML = (l || []).map((e) => fileRow(e, false)).join("") || `<tr><td class="muted">${esc(T("emptyFolder"))}</td></tr>`;
  } catch {}
}

const ICON_DIR = `<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>`;
const ICON_FILE = `<svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/></svg>`;

function fmtSize(n) {
  if (n == null) return "";
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(0) + " KB";
  if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
  return (n / 1073741824).toFixed(2) + " GB";
}

function fileRow(e, actions) {
  const depth = e.path.split("/").length - 1;
  const name = e.path.split("/").pop();
  const openable = /\.(pptx|ppt|key|odp|pdf)$/i.test(name);
  const acts = !actions ? "" : `<td class="act">
      ${!e.dir && openable ? `<button class="btn ghost small" data-act="open" title="${esc(T("openPres"))}">${esc(T("openPres"))}</button>` : ""}
      <button class="btn ghost small" data-act="get" title="${esc(T("download"))}">↓</button>
      <button class="btn ghost small" data-act="ren" title="${esc(T("rename"))}">✎</button>
      <button class="btn ghost small" data-act="del" title="${esc(T("delete"))}">🗑</button></td>`;
  return `<tr data-path="${esc(e.path)}" data-dir="${e.dir ? 1 : 0}">
    <td><span class="fname" style="padding-left:${depth * 18}px">${e.dir ? ICON_DIR : ICON_FILE}<span>${esc(name)}</span>${e.open ? `<span class="open-tag">${esc(T("inUse"))}</span>` : ""}</span></td>
    <td class="small muted num">${e.dir ? "" : fmtSize(e.size)}</td>
    <td class="small muted num">${new Date(e.mtime * 1000).toLocaleString(lang() === "el" ? "el-GR" : "en-GB", { hour12: false, dateStyle: "short", timeStyle: "short" })}</td>
    ${acts}</tr>`;
}

// ---------- Controller ----------
let agents = [];
let selected = null;
let files = [];

function initController() {
  $("ctrlView").hidden = false;
  refreshLabels = () => {
    $("modeLabel").textContent = T("role_" + info.config.role) + " · " + (info.config.name || "");
    renderAgents();
  };
  refreshLabels();
  go().CtrlCodes().then(renderCodes);
  on("ctrl:codes", (c) => { renderCodes(c); $("sasBox").hidden = true; });
  on("ctrl:sas", (m) => {
    $("sasNum").textContent = m.sas;
    $("sasBox").hidden = false;
    setTimeout(() => ($("sasBox").hidden = true), 120000);
  });
  on("ctrl:agents", (l) => {
    agents = l || [];
    if (agents.some((a) => a.online)) $("sasBox").hidden = true;
    renderAgents();
  });
  on("ctrl:event", ({ agent, event }) => {
    if (event.kind === "file.new" && agent === selected) loadFiles();
    if (event.kind === "control.taken") cmdMsg(T("takenBySupervisor", event.by?.name || ""));
  });
  on("ctrl:progress", renderProgress);
  on("ctrl:refresh", (id) => id === selected && loadFiles());
  on("ctrl:drop", (paths) => selected && go().CtrlUploadPaths(selected, paths, curDir));
  $("btnNewCode").onclick = () => go().CtrlNewCode();
  $("btnTake").onclick = async () => { const r = await go().CtrlCall(selected, "control.take", null); handle(r); };
  $("btnRelease").onclick = async () => { const r = await go().CtrlCall(selected, "control.release", null); handle(r); };
  $("tabPres").onclick = () => tab("pres");
  $("tabFiles").onclick = () => { tab("files"); loadFiles(); };
  const cmd = (c, args) => async () => handle(await go().CtrlCall(selected, c, args ?? null));
  $("bPrev").onclick = cmd("pres.prev");
  $("bNext").onclick = cmd("pres.next");
  $("bStart").onclick = cmd("pres.start");
  $("bEnd").onclick = cmd("pres.end");
  $("bClose").onclick = cmd("pres.close");
  $("bBlack").onclick = async () => {
    const st = agentById(selected)?.state?.status;
    handle(await go().CtrlCall(selected, "pres.black", { on: st !== "black" }));
  };
  $("bGoto").onclick = async () => {
    const n = parseInt($("gotoNum").value, 10);
    if (n > 0) handle(await go().CtrlCall(selected, "pres.goto", { slide: n }));
  };
  // Clicker keys: arrows, PageUp/PageDown, B, Esc, F5.
  document.addEventListener("keydown", (e) => {
    if (!selected || $("paneFiles").hidden === false || e.target.matches("input,select,textarea") || document.querySelector("dialog[open]")) return;
    const map = { ArrowRight: "pres.next", PageDown: "pres.next", " ": "pres.next", ArrowLeft: "pres.prev", PageUp: "pres.prev", Escape: "pres.end", F5: "pres.start" };
    if (map[e.key]) { e.preventDefault(); cmd(map[e.key])(); }
    else if (e.key === "b" || e.key === "B" || e.key === ".") { e.preventDefault(); $("bBlack").click(); }
  });
  $("fUpload").onclick = () => go().CtrlUploadPick(selected, curDir);
  $("fUploadDir").onclick = () => go().CtrlUploadFolderPick(selected, curDir);
  $("fRefresh").onclick = loadFiles;
  $("fOpFolder").onclick = () => { const a = agentById(selected); a && go().CtrlOpenOperatorFolder(a.name); };
  $("fMkdir").onclick = async () => {
    const n = await ask(T("mkdirName"), "");
    if (n) { fileResult(await go().CtrlCall(selected, "file.mkdir", { path: join(curDir, n) })); loadFiles(); }
  };
  const dz = $("dropZone");
  dz.addEventListener("dragover", (e) => { e.preventDefault(); dz.classList.add("over"); });
  dz.addEventListener("dragleave", () => dz.classList.remove("over"));
  dz.addEventListener("drop", (e) => { e.preventDefault(); dz.classList.remove("over"); });
  $("cFiles").addEventListener("click", onFileAction);
  $("cFiles").addEventListener("dblclick", (e) => {
    const tr = e.target.closest("tr[data-path]");
    if (!tr) return;
    if (tr.dataset.dir === "1") { curDir = tr.dataset.path; renderFiles(); }
    else openPres(tr.dataset.path);
  });
}

// ask shows a question; with a value it also shows a text field.
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

let curDir = "";
const join = (a, b) => (a ? a + "/" + b : b);
const agentById = (id) => agents.find((a) => a.id === id);

function renderCodes(codes) {
  if (!codes || !codes.length) { $("bigCode").textContent = T("noNetwork"); return; }
  // Prefer a private LAN address for the main code.
  const pick = codes.find((c) => /^(192\.168|10\.|172\.(1[6-9]|2\d|3[01]))/.test(c.ip)) || codes[0];
  $("bigCode").textContent = pick.short;
  $("codeIface").textContent = `${pick.iface} · ${pick.ip}`;
  $("codesAlt").innerHTML = codes.map((c) => `<div><b>${esc(c.iface)}</b> ${esc(c.ip)}<br><span class="num">${esc(c.short)}</span> · <span class="num muted">${esc(c.long)}</span></div>`).join("");
}

function renderAgents() {
  $("noAgents").hidden = agents.length > 0;
  $("agentList").innerHTML = agents.map((a) => {
    const s = a.state;
    const st = !a.online ? `<span class="status wait">${esc(T("w_back"))}</span>` : statusBadge(s?.status);
    return `<button class="agent ${a.id === selected ? "sel" : ""}" data-id="${esc(a.id)}">
      <span class="name">${esc(a.name)}</span>
      <span class="small muted">${esc(T("role_" + a.role))}${a.online ? ` · ${a.latencyMs} ms${a.path ? ` · ${esc(pathLabel(a.path))}` : ""}` : ""}</span>
      <span class="small">${st} ${s && s.file ? `· ${esc(s.file.split("/").pop())} · <span class="num">${slideText(s)}</span>` : ""}</span>
    </button>`;
  }).join("");
  $("agentList").querySelectorAll(".agent").forEach((b) => (b.onclick = () => select(b.dataset.id)));
  if (!selected && agents.length === 1) select(agents[0].id);
  renderPanel();
}

function select(id) {
  if (selected !== id) { curDir = ""; files = []; }
  selected = id;
  $("agentPanel").hidden = false;
  renderAgents();
  if (!$("paneFiles").hidden) loadFiles();
}

function renderPanel() {
  const a = agentById(selected);
  if (!a) { $("agentPanel").hidden = true; selected = null; return; }
  const s = a.state || {};
  $("pName").textContent = a.name;
  $("pInfo").innerHTML = `${esc(T("role_" + a.role))} · ${a.online ? `<span class="status ok">${esc(T("online"))}</span>` : `<span class="status err">${esc(T("offline"))}</span>`}` +
    (s.holder ? ` · ${esc(T("controlBy", s.holder.name))}` : "");
  const canPres = a.role !== "secretariat";
  $("btnTake").hidden = !canPres || a.holder;
  $("btnRelease").hidden = !canPres || !a.holder;
  $("tabPres").hidden = !canPres;
  if (!canPres && $("paneFiles").hidden) tab("files");
  $("cFile").textContent = s.file ? s.file : T("noFile");
  $("cStatus").innerHTML = statusBadge(s.status) + (s.app ? ` · ${esc(T("app_" + s.app))}` : "");
  $("cSlide").textContent = slideText(s);
  document.querySelectorAll("#panePres .pad button").forEach((b) => (b.disabled = !a.online || !a.holder));
  $("bBlack").textContent = s.status === "black" ? T("unblack") : T("black");
}

function tab(which) {
  $("tabPres").setAttribute("aria-selected", which === "pres");
  $("tabFiles").setAttribute("aria-selected", which === "files");
  $("panePres").hidden = which !== "pres";
  $("paneFiles").hidden = which !== "files";
}

function cmdMsg(text) {
  $("cmdMsg").textContent = text;
  $("cmdMsg").hidden = !text;
  if (text) setTimeout(() => ($("cmdMsg").hidden = true), 8000);
}

function handle(r) {
  if (r && r.code) cmdMsg(errText(r.code, r.err));
  else cmdMsg("");
  return r;
}

function fileResult(r) {
  const m = $("fileMsg");
  if (r && r.code) { m.textContent = errText(r.code, r.err); m.hidden = false; setTimeout(() => (m.hidden = true), 8000); }
  return r;
}

async function loadFiles() {
  if (!selected) return;
  const r = await go().CtrlCall(selected, "file.list", null);
  if (r.code) { fileResult(r); return; }
  files = r.data || [];
  renderFiles();
}

function renderFiles() {
  const inDir = files.filter((e) => {
    const parent = e.path.includes("/") ? e.path.slice(0, e.path.lastIndexOf("/")) : "";
    return parent === curDir;
  }).map((e) => ({ ...e, path: e.path }));
  const up = curDir ? `<tr data-up="1"><td colspan="4"><button class="btn ghost small" id="dirUp">↑ ${esc(T("up"))}</button></td></tr>` : "";
  $("curDir").textContent = "/" + curDir;
  $("cFiles").innerHTML = up + (inDir.map((e) => fileRow({ ...e, path: e.path }, true).replace(/padding-left:\d+px/, "padding-left:0")).join("") || `<tr><td class="muted">${esc(T("emptyFolder"))}</td></tr>`);
  const b = $("dirUp");
  if (b) b.onclick = () => { curDir = curDir.includes("/") ? curDir.slice(0, curDir.lastIndexOf("/")) : ""; renderFiles(); };
}

async function openPres(path) {
  const a = agentById(selected);
  if (!a?.holder) { const r = await go().CtrlCall(selected, "control.take", null); if (r.code) return fileResult(r); }
  const r = await go().CtrlCall(selected, "pres.open", { path });
  if (!fileResult(r).code) tab("pres");
}

async function onFileAction(e) {
  const btn = e.target.closest("button[data-act]");
  if (!btn) return;
  const tr = btn.closest("tr");
  const path = tr.dataset.path;
  const isDir = tr.dataset.dir === "1";
  switch (btn.dataset.act) {
    case "open": return openPres(path);
    case "get": return go().CtrlDownload(selected, path, isDir);
    case "ren": {
      const old = path.split("/").pop();
      const n = await ask(T("renameTo"), old);
      if (n && n !== old) { fileResult(await go().CtrlCall(selected, "file.rename", { from: path, to: join(curDir, n) })); loadFiles(); }
      return;
    }
    case "del":
      if (await ask(T("deleteConfirm", path.split("/").pop()))) { fileResult(await go().CtrlCall(selected, "file.delete", { path })); loadFiles(); }
  }
}

const transfers = new Map();
function renderProgress(p) {
  const key = (p.up ? "u:" : "d:") + p.agent + ":" + p.path;
  transfers.set(key, p);
  if (p.state !== "running") setTimeout(() => { transfers.delete(key); drawProgress(); }, p.state === "ok" ? 4000 : 15000);
  drawProgress();
}
function drawProgress() {
  $("progress").innerHTML = [...transfers.values()].map((p) => {
    const pct = p.total ? Math.floor((p.done / p.total) * 100) : 100;
    const st = p.state === "ok" ? `<span class="status ok">${esc(T("hashOk"))}</span>` : p.state === "error" ? `<span class="status err">${esc(p.err || "")}</span>` : `<span class="num">${pct}%</span>`;
    return `<div class="small"><div class="row" style="justify-content:space-between"><span>${p.up ? "↑" : "↓"} ${esc(p.path)}</span>${st}</div><div class="bar"><i style="width:${pct}%"></i></div></div>`;
  }).join("");
}

boot();
