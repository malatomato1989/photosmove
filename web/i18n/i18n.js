// PhotosMove i18n core (design D4). 零依赖纯 JS.
// 暴露 window.I18N = { SUPPORTED, getLocale, setLocale, t, te, applyToDOM, currentLabel }
//   getLocale()  : localStorage > navigator.language(降级匹配 en-US→en) > 'en'
//   t(key,params): ui 命名空间查找 + {param} 插值; 缺 key 返回 key 本身
//   te(code,parm): errors 命名空间(服务端 error code)查找; 未知 code 原样返回(spec Req5 兜底)
//   applyToDOM() : [data-i18n]→textContent, [data-i18n-html]→innerHTML (HTML 文案保留标签, spec Req6)
//   setLocale()  : 写 localStorage + applyToDOM + 更新 <html lang>
// 加新语言 = locales/xx.js + SUPPORTED 加一项, 零逻辑改动 (spec Req4)
(function () {
    'use strict';

    var STORAGE_KEY = 'photosmove_locale';
    var SUPPORTED = ['zh', 'en'];          // 加语言在此声明性新增 (spec Req4)
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
        // 直接用 code 作 <html lang>, 不硬编码 zh/en 三元 —— 满足 spec Req4「加新语言零逻辑改动」.
        document.documentElement.lang = lang;
        applyToDOM();
    }

    function interpolate(s, params) {
        if (!params) return s;
        return String(s).replace(/\{(\w+)\}/g, function (_, k) {
            return (params[k] !== undefined && params[k] !== null) ? params[k] : '';
        });
    }

    // UI 文案
    function t(key, params) {
        var s = uiOf(getLocale())[key];
        return (s === undefined) ? key : interpolate(s, params);
    }

    // 服务端 error code 翻译; 未知 code 原样返回不崩溃 (spec Req5)
    function te(code, params) {
        var s = errOf(getLocale())[code];
        return (s === undefined) ? code : interpolate(s, params);
    }

    function applyToDOM() {
        var loc = getLocale();
        // 同步 <html lang>: 初始加载 (只调 applyToDOM 不调 setLocale) 时也修正, 避免
        // index.html 硬编码 zh-CN 与实际渲染语言不符 (影响屏幕阅读器/翻译提示).
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

    // 语言入口按钮显示当前语言 (D6: 显示当前语言代码 EN/中文)
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
