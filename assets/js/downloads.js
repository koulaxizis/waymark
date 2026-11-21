// Waymark download count. Reads the public numbers of the Koulaxizis Apps
// download counter (https://apps.koulaxizis.workers.dev/stats.json, kept in
// the orOS repository, tools/apps-counter/): every channel of the Waymark app
// (Koulaxizis Apps, F-Droid clients, the direct APK, Obtainium, other).
// The line stays hidden until there are more than 10 downloads (Chris
// 2026-11-21). Nothing about the visitor is sent or stored.
(function () {
  "use strict";
  var URL = "https://apps.koulaxizis.workers.dev/stats.json";
  var PREFIX = "site.waymarked.";
  var MIN = 11;
  var el = document.getElementById("wm-downloads");
  if (!el || !window.fetch) return;
  function num(v) {
    if (typeof v === "number") return v;
    if (v && typeof v.n === "number") return v.n;
    return 0;
  }
  fetch(URL, { credentials: "omit", referrerPolicy: "no-referrer" })
    .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
    .then(function (s) {
      if (!s || s.v !== 1 || !s.apps) return;
      var n = 0;
      Object.keys(s.apps).forEach(function (pkg) {
        if (pkg.indexOf(PREFIX) !== 0) return;
        var row = s.apps[pkg];
        Object.keys(row).forEach(function (ch) { n += num(row[ch]); });
      });
      if (n < MIN) return;
      el.querySelectorAll("[data-n]").forEach(function (b) {
        var l = b.closest("[lang]");
        try { b.textContent = new Intl.NumberFormat(l ? l.getAttribute("lang") : "en").format(n); }
        catch (e) { b.textContent = String(n); }
      });
      el.hidden = false;
    })
    .catch(function () { /* no number: the line stays hidden */ });
})();
