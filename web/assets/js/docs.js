(function () {
  "use strict";

  function t(key, fallback) {
    return window.MoliSyncI18n ? window.MoliSyncI18n.t(key) : fallback;
  }

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.className = "copy-buffer";
      document.body.appendChild(area);
      area.select();
      try {
        var ok = document.execCommand("copy");
        document.body.removeChild(area);
        if (ok) resolve();
        else reject(new Error("Copy command failed"));
      } catch (err) {
        document.body.removeChild(area);
        reject(err);
      }
    });
  }

  var tocObserver = null;
  var tocRoot = null;

  function buildToc() {
    var content = document.querySelector(".docs-prose");
    var toc = document.getElementById("page-toc");
    if (!content || !toc) return;

    var headings = Array.prototype.slice.call(content.querySelectorAll("h2[id], h3[id]"));
    toc.textContent = "";
    tocRoot = toc;

    headings.forEach(function (heading) {
      var link = document.createElement("a");
      link.className = "toc-link" + (heading.tagName === "H3" ? " is-h3" : "");
      link.href = "#" + heading.id;
      link.textContent = heading.textContent;
      toc.appendChild(link);
    });

    if (tocObserver) tocObserver.disconnect();
    if (!("IntersectionObserver" in window) || !headings.length) return;

    tocObserver = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        var link = tocRoot.querySelector('a[href="#' + entry.target.id + '"]');
        if (!link) return;
        tocRoot.querySelectorAll(".toc-link").forEach(function (item) {
          item.classList.remove("is-active");
        });
        link.classList.add("is-active");
      });
    }, { rootMargin: "-80px 0px -65% 0px", threshold: 0 });

    headings.forEach(function (heading) { tocObserver.observe(heading); });

    var first = toc.querySelector(".toc-link");
    if (first) first.classList.add("is-active");
  }

  function enhanceCode() {
    document.querySelectorAll(".docs-prose pre").forEach(function (pre) {
      if (pre.closest(".code-block")) return;

      var language = pre.getAttribute("data-lang") || "shell";
      var block = document.createElement("div");
      block.className = "code-block";

      var head = document.createElement("div");
      head.className = "code-head";

      var label = document.createElement("span");
      label.textContent = language;

      var button = document.createElement("button");
      button.type = "button";
      button.className = "doc-copy";
      button.textContent = t("manual.ui.copy", "Copy");
      button.setAttribute("aria-live", "polite");

      head.appendChild(label);
      head.appendChild(button);
      pre.parentNode.insertBefore(block, pre);
      block.appendChild(head);
      block.appendChild(pre);

      button.addEventListener("click", function () {
        copyText(pre.textContent).then(function () {
          button.textContent = t("manual.ui.copied", "Copied");
          button.setAttribute("data-state", "copied");
          window.setTimeout(function () {
            button.textContent = t("manual.ui.copy", "Copy");
            button.removeAttribute("data-state");
          }, 1600);
        }).catch(function () {
          button.textContent = t("manual.ui.copy", "Copy");
        });
      });
    });
  }

  function boot() {
    enhanceCode();
    buildToc();
  }

  document.addEventListener("molisync:localechange", boot);

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot, { once: true });
  } else {
    boot();
  }
})();
