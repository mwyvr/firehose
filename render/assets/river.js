// firehose client JS: read-position marker + clipboard copy. Progressive
// enhancement only — the page is fully functional without it.
(function () {
  // Read marker: remember the newest item seen; on return, mark where the
  // river's head was last visit. Read state is a scroll position.
  try {
    var key = "firehose:" + location.pathname;
    var last = localStorage.getItem(key);
    var first = document.querySelector("article[id]");
    // Draw the high-water mark only when there is water above it: if the
    // stored anchor IS the current top item, nothing is new — a marker
    // above item 1 is noise, not information.
    if (last && first && last !== first.id) {
      var el = document.getElementById(last);
      if (el) {
        var m = document.createElement("div");
        m.className = "readmark";
        m.textContent = "\u00b7 \u00b7 \u00b7 you were here \u00b7 \u00b7 \u00b7";
        el.parentNode.insertBefore(m, el);
      }
    }
    if (first) localStorage.setItem(key, first.id);
  } catch (e) { /* storage unavailable; fine */ }


  // Theme toggle: user choice > config default > OS. The head applied any
  // stored choice pre-paint; this cycles auto -> light -> dark and persists.
  try {
    var KEY = "firehose:theme";
    var rootEl = document.documentElement;
    var toggles = document.querySelectorAll("button.themetoggle");
    function themeState() {
      try {
        var t = localStorage.getItem(KEY);
        if (t === "light" || t === "dark") return t;
      } catch (e) {}
      return "auto";
    }
    function themeGlyph(s) {
      return s === "light" ? "\u2600\ufe0e" : s === "dark" ? "\u263e" : "\u25d0";
    }
    function themeApply(s) {
      var attr = s === "auto" ? (rootEl.getAttribute("data-theme-default") || "") : s;
      if (attr) rootEl.setAttribute("data-theme", attr);
      else rootEl.removeAttribute("data-theme");
      try {
        if (s === "auto") localStorage.removeItem(KEY);
        else localStorage.setItem(KEY, s);
      } catch (e) {}
      for (var i = 0; i < toggles.length; i++) {
        toggles[i].textContent = themeGlyph(s);
        toggles[i].title = "Theme: " + s;
      }
    }
    for (var i = 0; i < toggles.length; i++) {
      toggles[i].textContent = themeGlyph(themeState());
      toggles[i].title = "Theme: " + themeState();
      toggles[i].addEventListener("click", function () {
        var order = ["auto", "light", "dark"];
        themeApply(order[(order.indexOf(themeState()) + 1) % order.length]);
      });
    }
  } catch (e) { /* cosmetic only */ }

  // Humanized times, computed at VIEW time: only the browser knows "now".
  // Relative times baked into static HTML would start lying within the hour;
  // the generated markup stays absolute (and deterministic), and no-JS
  // readers keep it. Under a week: relative; older: the absolute date is the
  // better scanning label anyway. Hover always shows the absolute time.
  try {
    var times = document.querySelectorAll("time[datetime]");
    var now = Date.now();
    for (var i = 0; i < times.length; i++) {
      var t = times[i];
      var d = new Date(t.getAttribute("datetime"));
      if (isNaN(d.getTime())) continue;
      var s = Math.floor((now - d.getTime()) / 1000);
      if (s < 0) continue; // future-dated publisher clock: leave absolute
      var label = null;
      if (s < 60) label = "just now";
      else if (s < 3600) label = Math.floor(s / 60) + " min ago";
      else if (s < 86400) label = Math.floor(s / 3600) + "h ago";
      else if (s < 604800) label = Math.floor(s / 86400) + "d ago";
      if (label) {
        t.setAttribute("title", t.textContent);
        t.textContent = label;
      }
    }
  } catch (e) { /* cosmetic only */ }

  // Clipboard copy buttons (URL / Markdown). navigator.clipboard exists only
  // in secure contexts (https, or localhost) — a LAN hostname over plain http
  // is not one — so fall back to the textarea/execCommand path there.
  function copyText(t) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(t);
    }
    return new Promise(function (resolve, reject) {
      var ta = document.createElement("textarea");
      ta.value = t;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy") ? resolve() : reject(new Error("copy failed"));
      } catch (err) {
        reject(err);
      } finally {
        ta.remove();
      }
    });
  }

  document.addEventListener("click", function (e) {
    var b = e.target.closest("button.copy");
    if (!b) return;
    copyText(b.getAttribute("data-copy") || "").then(function () {
      var old = b.textContent;
      b.textContent = "\u2713";
      setTimeout(function () { b.textContent = old; }, 1200);
    });
  });
})();
