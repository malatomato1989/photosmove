#!/usr/bin/env node
// web/i18n/check-parity.js — verify consistency of the zh.js / en.js translation tables
// (spec Req6 / tasks 6.1).
//
// design.md Risk: "After strings.xml's default is switched to English, if a key is left
// untranslated, a Chinese-locale system falls back to showing English"
// → this script bakes zh/en key-set consistency into a repeatable build/CI guarantee
// against regressions.
//
// Comparisons:
//   ① key sets of the ui + errors namespaces (extra/missing key = missed translation)
//   ② the {param} placeholder set of each key (present in en but not zh → empty
//     interpolation, or vice versa → broken copy)
// Exits 1 on failure; hooked into android preBuild (can also be run directly with node).
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const repoRoot = path.join(__dirname, '..', '..');
const dir = path.join(__dirname, 'locales');

// zh.js/en.js assign via `window.PHOTOSMOVE_I18N = ...`; node has no window, so shim it
// with a vm sandbox.
function load(file) {
    const src = fs.readFileSync(path.join(dir, file), 'utf8');
    const ctx = { window: {} };
    vm.createContext(ctx);
    vm.runInContext(src, ctx);
    return ctx.window.PHOTOSMOVE_I18N;
}

// Extract the <string name="xxx"> key set from Android strings.xml (keys only, values
// ignored — existence check).
function loadAndroidStrings(file) {
    const src = fs.readFileSync(file, 'utf8');
    const keys = [];
    const re = /<string\s+name="([^"]+)"/g;
    let m;
    while ((m = re.exec(src)) !== null) keys.push(m[1]);
    return new Set(keys);
}

// load('xx.js') returns { xx: {ui, errors} } (locale files write window.PHOTOSMOVE_I18N.xx);
// take .xx to get the table.
const zh = load('zh.js').zh || {};
const en = load('en.js').en || {};

let failures = 0;
function fail(msg) { console.error('  ✗ ' + msg); failures++; }

const placeholders = s => (String(s).match(/\{(\w+)\}/g) || []).sort().join(',');

// --- web: zh.js / en.js (ui + errors namespace key sets + placeholders) ---
for (const ns of ['ui', 'errors']) {
    const zk = Object.keys((zh && zh[ns]) || {});
    const ek = Object.keys((en && en[ns]) || {});
    const zhOnly = zk.filter(k => !ek.includes(k));
    const enOnly = ek.filter(k => !zk.includes(k));
    if (zhOnly.length) fail(`[web.${ns}] in zh but not en: ${zhOnly.join(', ')}`);
    if (enOnly.length) fail(`[web.${ns}] in en but not zh: ${enOnly.join(', ')}`);
    for (const k of zk.filter(k => ek.includes(k))) {
        const pz = placeholders(zh[ns][k]);
        const pe = placeholders(en[ns][k]);
        if (pz !== pe) fail(`[web.${ns}].${k} placeholder mismatch: zh={${pz}} en={${pe}}`);
    }
}

// --- android: values/ (English default) / values-zh/ (zh fallback, covers zh-TW etc.) /
//     values-zh-rCN/ (zh-CN exact) — the key sets of all strings.xml files must match
//     exactly.
const androidRes = path.join(repoRoot, 'android', 'app', 'src', 'main', 'res');
const androidStringFiles = {
    'values': path.join(androidRes, 'values', 'strings.xml'),
    'values-zh': path.join(androidRes, 'values-zh', 'strings.xml'),
    'values-zh-rCN': path.join(androidRes, 'values-zh-rCN', 'strings.xml'),
    'values-b+zh+Hant': path.join(androidRes, 'values-b+zh+Hant', 'strings.xml'),
};
const androidKeySets = {};
for (const [label, file] of Object.entries(androidStringFiles)) {
    if (!fs.existsSync(file)) { fail(`[android.strings] missing ${file}`); androidKeySets[label] = new Set(); continue; }
    androidKeySets[label] = loadAndroidStrings(file);
}
const androidLabels = Object.keys(androidKeySets);
for (let i = 0; i < androidLabels.length; i++) {
    for (let j = i + 1; j < androidLabels.length; j++) {
        const a = androidLabels[i], b = androidLabels[j];
        const ka = androidKeySets[a], kb = androidKeySets[b];
        const aOnly = [...ka].filter(k => !kb.has(k));
        const bOnly = [...kb].filter(k => !ka.has(k));
        if (aOnly.length) fail(`[android.strings] in ${a}/ but not ${b}/: ${aOnly.join(', ')}`);
        if (bOnly.length) fail(`[android.strings] in ${b}/ but not ${a}/: ${bOnly.join(', ')}`);
    }
}

// --- server↔frontend: the set of E_XXX codes emitted by handler.go must match the
// errors namespace in zh.js/en.js.
// Guards against "server adds writeErr(w,503,"E_NEW_CODE") but forgets to update zh/en"
// → the user would see the raw code string.
const handlerGo = path.join(repoRoot, 'server', 'handler.go');
if (fs.existsSync(handlerGo)) {
    const goSrc = fs.readFileSync(handlerGo, 'utf8');
    const serverCodes = new Set();
    const codeRe = /"E_[A-Z_]+"/g;
    let cm;
    while ((cm = codeRe.exec(goSrc)) !== null) serverCodes.add(cm[0].slice(1, -1));
    const frontendCodes = new Set(Object.keys((zh && zh.errors) || {}));
    const serverOnly = [...serverCodes].filter(c => !frontendCodes.has(c));
    const frontendOnly = [...frontendCodes].filter(c => !serverCodes.has(c));
    if (serverOnly.length) fail(`[error-contract] emitted by server but untranslated in frontend errors: ${serverOnly.join(', ')}`);
    if (frontendOnly.length) fail(`[error-contract] translated in frontend errors but never emitted by server: ${frontendOnly.join(', ')}`);
} else {
    fail('[error-contract] server/handler.go does not exist; skipping server↔frontend parity');
}

if (failures === 0) {
    console.log('i18n parity OK: web zh/en + android values/values-zh/values-zh-rCN/values-b+zh+Hant + server↔frontend error codes all consistent');
    process.exit(0);
} else {
    console.error(`i18n parity FAILED: ${failures} inconsistencies`);
    process.exit(1);
}
