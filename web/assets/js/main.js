(function () {
  "use strict";

  function t(key, fallback) {
    return window.MoliSyncI18n ? window.MoliSyncI18n.t(key) : fallback;
  }

  /* ---------- Command tabs ---------- */

  var tabs = Array.prototype.slice.call(document.querySelectorAll("[data-cmd-tab]"));
  var cmdLine = document.getElementById("cmd-line");

  tabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      tabs.forEach(function (item) {
        item.setAttribute("aria-selected", item === tab ? "true" : "false");
      });
      if (cmdLine) cmdLine.textContent = tab.getAttribute("data-cmd") || "";
    });
  });

  /* ---------- Data path state tabs ---------- */

  var pathTabs = Array.prototype.slice.call(document.querySelectorAll("[data-path-tab]"));
  var pathPanels = Array.prototype.slice.call(document.querySelectorAll("[data-path-panel]"));

  pathTabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      var mode = tab.getAttribute("data-path-tab");
      pathTabs.forEach(function (item) {
        item.setAttribute("aria-selected", item === tab ? "true" : "false");
      });
      pathPanels.forEach(function (panel) {
        panel.hidden = panel.getAttribute("data-path-panel") !== mode;
      });
    });
  });

  /* ---------- Copy to clipboard ---------- */

  function legacyCopy(text) {
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

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      // Permission can be denied even after a click; fall back rather than
      // leaving the button silently untouched.
      return navigator.clipboard.writeText(text).catch(function () {
        return legacyCopy(text);
      });
    }
    return legacyCopy(text);
  }

  var copyButton = document.querySelector("[data-copy-target]");
  var resetTimer = null;

  if (copyButton) {
    copyButton.addEventListener("click", function () {
      var target = document.querySelector(copyButton.getAttribute("data-copy-target"));
      if (!target) return;

      copyText(target.textContent).then(function () {
        copyButton.textContent = t("hero.cmd.copied", "Copied");
        copyButton.setAttribute("data-state", "copied");
        window.clearTimeout(resetTimer);
        resetTimer = window.setTimeout(function () {
          copyButton.textContent = t("hero.cmd.copy", "Copy");
          copyButton.removeAttribute("data-state");
        }, 1600);
      }).catch(function () {
        copyButton.textContent = t("hero.cmd.copy", "Copy");
      });
    });

    document.addEventListener("molisync:localechange", function () {
      if (copyButton.getAttribute("data-state") !== "copied") {
        copyButton.textContent = t("hero.cmd.copy", "Copy");
      }
    });
  }

  /* ---------- Sponsor dialog ---------- */

  var sponsorDialog = document.getElementById("sponsor-dialog");
  var sponsorOpeners = Array.prototype.slice.call(document.querySelectorAll("[data-sponsor-open]"));

  if (sponsorDialog && typeof sponsorDialog.showModal === "function") {
    sponsorOpeners.forEach(function (button) {
      button.addEventListener("click", function () {
        sponsorDialog.showModal();
      });
    });

    Array.prototype.slice.call(sponsorDialog.querySelectorAll("[data-sponsor-close]")).forEach(function (button) {
      button.addEventListener("click", function () {
        sponsorDialog.close();
      });
    });

    // Clicking the backdrop closes the dialog; clicks inside must not.
    sponsorDialog.addEventListener("click", function (event) {
      if (event.target === sponsorDialog) {
        sponsorDialog.close();
      }
    });

    // The docs footer links here with ?sponsor=1 so the dialog opens directly.
    if (new URLSearchParams(window.location.search).get("sponsor") === "1") {
      sponsorDialog.showModal();
    }
  }

  Array.prototype.slice.call(document.querySelectorAll("[data-copy-value]")).forEach(function (button) {
    var label = button.textContent;
    var timer = null;

    button.addEventListener("click", function () {
      copyText(button.getAttribute("data-copy-value") || "").then(function () {
        button.textContent = t("sponsor.copied", "Copied");
        window.clearTimeout(timer);
        timer = window.setTimeout(function () {
          button.textContent = label;
        }, 1600);
      });
    });
  });

  /* ---------- Footer year ---------- */

  var year = document.getElementById("year");
  if (year) year.textContent = String(new Date().getFullYear());
})();
