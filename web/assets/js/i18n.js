/* ------------------------------------------------------------------
   Tiny client-side i18n runtime.

   - Detects the browser language, falls back to English.
   - `?lang=xx` wins, then an explicit user choice, then navigator.
   - Locale dictionaries are plain scripts so the page also works
     when opened directly from disk (file://), not just on a server.
   - Adding a language: create assets/i18n/<code>.js, add one entry
     to LOCALES, and optionally preload it in index.html.
   ------------------------------------------------------------------ */

(function () {
  "use strict";

  var DEFAULT = "en";
  var STORAGE_KEY = "molisync.locale";
  var currentScript = document.currentScript;
  if (!currentScript) {
    currentScript = Array.prototype.slice.call(document.scripts).filter(function (script) {
      return /assets\/js\/i18n\.js/.test(script.src);
    }).pop();
  }
  var scriptSrc = currentScript ? currentScript.src : "";
  var ASSET_BASE = scriptSrc.replace(/assets\/js\/i18n\.js(?:\?.*)?$/, "");

  var LOCALES = [
    {
      code: "en",
      label: "EN",
      name: "English",
      file: "assets/i18n/en.js",
      aliases: ["en-us", "en-gb"]
    },
    {
      code: "zh-CN",
      label: "中文",
      name: "简体中文",
      file: "assets/i18n/zh-CN.js",
      aliases: ["zh", "zh-hans", "zh-sg", "zh-my"]
    }
  ];

  var dicts = Object.create(null);
  var active = DEFAULT;
  var switcherBuilt = false;
  var booted = false;
  var refreshTimer = null;

  function normalizeTag(tag) {
    return String(tag || "").trim().toLowerCase().replace(/_/g, "-");
  }

  function findLocale(tag) {
    var normalized = normalizeTag(tag);
    if (!normalized) return null;

    for (var i = 0; i < LOCALES.length; i++) {
      if (normalizeTag(LOCALES[i].code) === normalized) return LOCALES[i].code;
    }

    for (var j = 0; j < LOCALES.length; j++) {
      var aliases = LOCALES[j].aliases || [];
      for (var k = 0; k < aliases.length; k++) {
        if (normalizeTag(aliases[k]) === normalized) return LOCALES[j].code;
      }
    }

    var base = normalized.split("-")[0];
    for (var m = 0; m < LOCALES.length; m++) {
      if (normalizeTag(LOCALES[m].code).split("-")[0] === base) return LOCALES[m].code;
    }
    return null;
  }

  function metaFor(code) {
    for (var i = 0; i < LOCALES.length; i++) {
      if (LOCALES[i].code === code) return LOCALES[i];
    }
    return null;
  }

  function detectLocale() {
    var params = new URLSearchParams(window.location.search);
    var fromQuery = findLocale(params.get("lang"));
    if (fromQuery) return fromQuery;

    try {
      var saved = findLocale(window.localStorage.getItem(STORAGE_KEY));
      if (saved) return saved;
    } catch (err) {
      /* storage can be unavailable in private modes */
    }

    var candidates = navigator.languages && navigator.languages.length
      ? navigator.languages
      : [navigator.language || navigator.userLanguage];

    for (var i = 0; i < candidates.length; i++) {
      var matched = findLocale(candidates[i]);
      if (matched) return matched;
    }
    return DEFAULT;
  }

  function lookup(dict, key) {
    if (!dict || !key) return undefined;
    var parts = key.split(".");
    var node = dict;
    for (var i = 0; i < parts.length; i++) {
      if (node == null || typeof node !== "object") return undefined;
      node = node[parts[i]];
    }
    return node;
  }

  function translate(key, vars) {
    var value = lookup(dicts[active], key);
    if (value === undefined) value = lookup(dicts[DEFAULT], key);
    if (typeof value !== "string") return key;
    if (vars) {
      Object.keys(vars).forEach(function (name) {
        value = value.split("{" + name + "}").join(String(vars[name]));
      });
    }
    return value;
  }

  function setMeta(selector, attribute, value) {
    if (!value) return;
    var el = document.querySelector(selector);
    if (el) el.setAttribute(attribute, value);
  }

  function applyTranslations() {
    var dict = dicts[active] || {};
    var fallback = dicts[DEFAULT] || {};
    var titleKey = document.documentElement.getAttribute("data-title-key") || "meta.title";
    var descriptionKey = document.documentElement.getAttribute("data-description-key") || "meta.description";
    if (document.documentElement.hasAttribute("data-title-key") && !lookup(dict, titleKey) && !lookup(fallback, titleKey)) {
      titleKey = "meta.title";
      descriptionKey = "meta.description";
    }

    document.querySelectorAll("[data-i18n]").forEach(function (el) {
      var value = lookup(dict, el.getAttribute("data-i18n"));
      if (value === undefined) value = lookup(fallback, el.getAttribute("data-i18n"));
      if (typeof value === "string") el.textContent = value;
    });

    document.querySelectorAll("[data-i18n-html]").forEach(function (el) {
      var value = lookup(dict, el.getAttribute("data-i18n-html"));
      if (value === undefined) value = lookup(fallback, el.getAttribute("data-i18n-html"));
      if (typeof value === "string") el.innerHTML = value;
    });

    document.querySelectorAll("[data-i18n-attr]").forEach(function (el) {
      el.getAttribute("data-i18n-attr").split(",").forEach(function (pair) {
        var bits = pair.split(":");
        if (bits.length < 2) return;
        var attribute = bits[0].trim();
        var key = bits.slice(1).join(":").trim();
        var value = lookup(dict, key);
        if (value === undefined) value = lookup(fallback, key);
        if (typeof value === "string") el.setAttribute(attribute, value);
      });
    });

    document.title = translate(titleKey);
    setMeta('meta[name="description"]', "content", translate(descriptionKey));
    setMeta('meta[property="og:title"]', "content", translate(titleKey));
    setMeta('meta[property="og:description"]', "content", translate(descriptionKey));

    var meta = metaFor(active);
    document.documentElement.lang = active;
    document.documentElement.dir = (meta && meta.dir) || "ltr";
    document.documentElement.setAttribute("data-i18n-state", "ready");

    var group = document.getElementById("lang-switch");
    if (group) group.setAttribute("aria-label", translate("a11y.langSwitch"));
    document.querySelectorAll("[data-lang]").forEach(function (button) {
      button.setAttribute("aria-pressed", button.getAttribute("data-lang") === active ? "true" : "false");
    });
  }

  function buildSwitcher() {
    var group = document.getElementById("lang-switch");
    if (!group) return;

    group.textContent = "";
    LOCALES.forEach(function (locale) {
      var button = document.createElement("button");
      button.type = "button";
      button.className = "lang-btn";
      button.setAttribute("data-lang", locale.code);
      button.setAttribute("lang", locale.code);
      button.setAttribute("title", locale.name);
      button.textContent = locale.label;
      button.addEventListener("click", function () {
        chooseLocale(locale.code);
      });
      group.appendChild(button);
    });

    group.setAttribute("aria-label", translate("a11y.langSwitch"));
    switcherBuilt = true;
  }

  function updateUrl(code) {
    try {
      var url = new URL(window.location.href);
      url.searchParams.set("lang", code);
      window.history.replaceState(null, "", url);
    } catch (err) {
      /* replaceState may fail on file:// in some browsers */
    }
  }

  function chooseLocale(code) {
    var canonical = findLocale(code) || DEFAULT;
    loadLocale(canonical).then(function () {
      active = canonical;
      try {
        window.localStorage.setItem(STORAGE_KEY, canonical);
      } catch (err) {
        /* ignore unavailable storage */
      }
      updateUrl(canonical);
      applyTranslations();
      document.dispatchEvent(new CustomEvent("molisync:localechange", { detail: { locale: canonical } }));
    });
  }

  function loadLocale(code) {
    if (dicts[code]) return Promise.resolve();
    var meta = metaFor(code);
    if (!meta) return Promise.reject(new Error("Unsupported locale: " + code));

    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.src = ASSET_BASE + meta.file;
      script.async = true;
      script.onload = function () {
        if (dicts[code]) resolve();
        else reject(new Error("Locale file did not register " + code));
      };
      script.onerror = function () {
        reject(new Error("Failed to load locale " + code));
      };
      document.head.appendChild(script);
    });
  }

  function init() {
    if (!switcherBuilt) buildSwitcher();

    var target = detectLocale();
    loadLocale(target)
      .then(function () {
        active = target;
        booted = true;
        applyTranslations();
        document.dispatchEvent(new CustomEvent("molisync:localechange", { detail: { locale: target } }));
      })
      .catch(function (err) {
        if (window.console) console.warn("[i18n]", err.message);
        active = DEFAULT;
        booted = true;
        applyTranslations();
        document.dispatchEvent(new CustomEvent("molisync:localechange", { detail: { locale: DEFAULT, fallback: true } }));
      });
  }

  function scheduleRefresh() {
    if (!booted) return;
    if (refreshTimer) window.clearTimeout(refreshTimer);
    refreshTimer = window.setTimeout(function () {
      applyTranslations();
      document.dispatchEvent(new CustomEvent("molisync:localechange", { detail: { locale: active } }));
    }, 0);
  }

  window.MoliSyncI18n = {
    add: function (code, dict) {
      if (!dicts[code]) dicts[code] = {};
      Object.keys(dict).forEach(function (key) {
        var current = dicts[code][key];
        var next = dict[key];
        if (current && next && typeof current === "object" && typeof next === "object" &&
            !Array.isArray(current) && !Array.isArray(next)) {
          Object.assign(current, next);
        } else {
          dicts[code][key] = next;
        }
      });
      scheduleRefresh();
    },
    t: translate,
    get current() {
      return active;
    },
    setLocale: chooseLocale,
    supported: LOCALES.map(function (locale) {
      return { code: locale.code, name: locale.name };
    })
  };

  document.documentElement.classList.add("js");

  if (document.readyState === "complete") {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  }
})();
