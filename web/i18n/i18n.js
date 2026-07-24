// PhotosMove i18n core (design D4). Zero-dependency plain JS.
// Exposes window.I18N = { SUPPORTED, getLocale, setLocale, t, te, applyToDOM, currentLabel }
//   getLocale()  : localStorage > navigator.language (fallback match en-US→en) > 'en'
//   t(key,params): ui namespace lookup + {param} interpolation; missing key returns the key itself
//   te(code,parm): errors namespace (server error code) lookup; unknown code returned as-is (spec Req5 fallback)
//   applyToDOM() : [data-i18n]→textContent, [data-i18n-html]→innerHTML (HTML copy keeps its tags, spec Req6)
//   setLocale()  : writes localStorage + applyToDOM + updates <html lang>
// Adding a language = locales/xx.js + one SUPPORTED entry, zero logic changes (spec Req4)
(function () {
    'use strict';

    var STORAGE_KEY = 'photosmove_locale';
    var SUPPORTED = ['zh', 'en'];          // add languages declaratively here (spec Req4)
    var DEFAULT = 'en';

    function tables() { return window.PHOTOSMOVE_I18N || {}; }
    function uiOf(loc) { var t = tables()[loc]; return (t && t.ui) || {}; }
    function errOf(loc) { var t = tables()[loc]; return (t && t.errors) || {}; }

    function match(tag) {
        if (!tag) return null;
        var lang = String(tag).toLowerCase().split('-')[0];
        return SUPPORTED.indexOf(lang) >= 0 ? lang : null;
    }

    function getLocale() {
        try {
            var saved = localStorage.getItem(STORAGE_KEY);
            if (saved && SUPPORTED.indexOf(saved) >= 0) return saved;
        } catch (e) {}
        var nav = match(navigator.language);
        if (!nav && navigator.languages) {
            for (var i = 0; i < navigator.languages.length; i++) {
                nav = match(navigator.languages[i]);
                if (nav) break;
            }
        }
        return nav || DEFAULT;
    }

    function setLocale(lang) {
        if (SUPPORTED.indexOf(lang) < 0) return;
        try { localStorage.setItem(STORAGE_KEY, lang); } catch (e) {}
        // Use the code directly as <html lang> instead of a hardcoded zh/en ternary —
        // satisfies spec Req4 "add a language with zero logic changes".
        document.documentElement.lang = lang;
        applyToDOM();
    }

    function interpolate(s, params) {
        if (!params) return s;
        return String(s).replace(/\{(\w+)\}/g, function (_, k) {
            return (params[k] !== undefined && params[k] !== null) ? params[k] : '';
        });
    }

    // UI copy
    function t(key, params) {
        var s = uiOf(getLocale())[key];
        return (s === undefined) ? key : interpolate(s, params);
    }

    // Server error-code translation; unknown codes returned as-is without crashing (spec Req5)
    function te(code, params) {
        var s = errOf(getLocale())[code];
        return (s === undefined) ? code : interpolate(s, params);
    }

    function applyToDOM() {
        var loc = getLocale();
        // Sync <html lang>: also fixed on initial load (where only applyToDOM runs, not
        // setLocale), so index.html's hardcoded zh-CN never mismatches the actual rendered
        // language (affects screen readers / translation prompts).
        document.documentElement.lang = loc;
        var u = uiOf(loc);
        var i, el, v;
        var textEls = document.querySelectorAll('[data-i18n]');
        for (i = 0; i < textEls.length; i++) {
            el = textEls[i];
            v = u[el.getAttribute('data-i18n')];
            if (v !== undefined) el.textContent = v;
        }
        var htmlEls = document.querySelectorAll('[data-i18n-html]');
        for (i = 0; i < htmlEls.length; i++) {
            el = htmlEls[i];
            v = u[el.getAttribute('data-i18n-html')];
            if (v !== undefined) el.innerHTML = v;
        }
    }

    // Language entry button shows the current language (D6: shows the current language
    // code, EN or the zh label)
    function currentLabel() {
        return getLocale() === 'zh' ? '中文' : 'EN';
    }

    window.I18N = {
        SUPPORTED: SUPPORTED,
        getLocale: getLocale,
        setLocale: setLocale,
        t: t,
        te: te,
        applyToDOM: applyToDOM,
        currentLabel: currentLabel
    };
})();
