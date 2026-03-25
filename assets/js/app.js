// OpenTrace client-side utilities

// Theme toggle: persists to localStorage, toggles .dark class on <html>
(function () {
  var stored = localStorage.getItem("opentrace-theme");
  if (stored === "dark") {
    document.documentElement.classList.add("dark");
  }
})();

function toggleTheme() {
  var html = document.documentElement;
  var isDark = html.classList.toggle("dark");
  localStorage.setItem("opentrace-theme", isDark ? "dark" : "light");
}

// CSRF: read token from cookie, patch fetch to auto-include header
(function () {
  function getCSRFToken() {
    var m = document.cookie.match("(?:^|;)\\s*opentrace_csrf=([^;]+)");
    return m ? decodeURIComponent(m[1]) : "";
  }

  var _fetch = window.fetch;
  window.fetch = function (url, opts) {
    opts = opts || {};
    var method = (opts.method || "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD" && method !== "OPTIONS") {
      if (!opts.headers) opts.headers = {};
      if (typeof opts.headers.set === "function") {
        if (!opts.headers.has("X-CSRF-Token"))
          opts.headers.set("X-CSRF-Token", getCSRFToken());
      } else {
        if (!opts.headers["X-CSRF-Token"])
          opts.headers["X-CSRF-Token"] = getCSRFToken();
      }
    }
    return _fetch.call(this, url, opts);
  };

  // Also set CSRF header on HTMX requests
  document.addEventListener("htmx:configRequest", function (e) {
    e.detail.headers["X-CSRF-Token"] = getCSRFToken();
  });
})();
