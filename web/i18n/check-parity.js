#!/usr/bin/env node
// web/i18n/check-parity.js — 校验 zh.js / en.js 翻译表一致性 (spec Req6 / tasks 6.1).
//
// design.md Risk: 「strings.xml 默认改英文后, 若漏翻某 key, 中文系统下回退显示英文」
// → 此脚本把 zh/en key 集合一致性固化为可重复的构建/CI 保障, 防回归.
//
// 比对:
//   ① ui + errors 两个命名空间的 key 集合 (多/少 key = 漏翻)
//   ② 每个 key 的 {param} 占位符集合 (en 有 zh 没有 → 插值空, 或反之 → 文案残缺)
// 失败 exit 1; 挂 android preBuild (亦可 node 直接跑).
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const repoRoot = path.join(__dirname, '..', '..');
const dir = path.join(__dirname, 'locales');

// zh.js/en.js 用 `window.PHOTOSMOVE_I18N = ...` 赋值; node 无 window, 用 vm 沙箱 shim.
function load(file) {
    const src = fs.readFileSync(path.join(dir, file), 'utf8');
    const ctx = { window: {} };
    vm.createContext(ctx);
    vm.runInContext(src, ctx);
    return ctx.window.PHOTOSMOVE_I18N;
}

// 从 Android strings.xml 提取 <string name="xxx"> key 集合 (不取值, 仅校验存在性).
function loadAndroidStrings(file) {
    const src = fs.readFileSync(file, 'utf8');
    const keys = [];
    const re = /<string\s+name="([^"]+)"/g;
    let m;
    while ((m = re.exec(src)) !== null) keys.push(m[1]);
    return new Set(keys);
}

// load('xx.js') 返回 { xx: {ui, errors} } (locale 文件写 window.PHOTOSMOVE_I18N.xx), 取 .xx 得 table.
const zh = load('zh.js').zh || {};
const en = load('en.js').en || {};

let failures = 0;
function fail(msg) { console.error('  ✗ ' + msg); failures++; }

const placeholders = s => (String(s).match(/\{(\w+)\}/g) || []).sort().join(',');

// --- web: zh.js / en.js (ui + errors 命名空间 key 集合 + 占位符) ---
for (const ns of ['ui', 'errors']) {
    const zk = Object.keys((zh && zh[ns]) || {});
    const ek = Object.keys((en && en[ns]) || {});
    const zhOnly = zk.filter(k => !ek.includes(k));
    const enOnly = ek.filter(k => !zk.includes(k));
    if (zhOnly.length) fail(`[web.${ns}] zh 有 en 无: ${zhOnly.join(', ')}`);
    if (enOnly.length) fail(`[web.${ns}] en 有 zh 无: ${enOnly.join(', ')}`);
    for (const k of zk.filter(k => ek.includes(k))) {
        const pz = placeholders(zh[ns][k]);
        const pe = placeholders(en[ns][k]);
        if (pz !== pe) fail(`[web.${ns}].${k} 占位符不一致: zh={${pz}} en={${pe}}`);
    }
}

// --- android: values/ (英文默认) / values-zh/ (zh 兜底, 覆盖 zh-TW 等) /
//     values-zh-rCN/ (zh-CN 精确) 三份 strings.xml key 集合须完全一致.
const androidRes = path.join(repoRoot, 'android', 'app', 'src', 'main', 'res');
const androidStringFiles = {
    'values': path.join(androidRes, 'values', 'strings.xml'),
    'values-zh': path.join(androidRes, 'values-zh', 'strings.xml'),
    'values-zh-rCN': path.join(androidRes, 'values-zh-rCN', 'strings.xml'),
    'values-b+zh+Hant': path.join(androidRes, 'values-b+zh+Hant', 'strings.xml'),
};
const androidKeySets = {};
for (const [label, file] of Object.entries(androidStringFiles)) {
    if (!fs.existsSync(file)) { fail(`[android.strings] 缺失 ${file}`); androidKeySets[label] = new Set(); continue; }
    androidKeySets[label] = loadAndroidStrings(file);
}
const androidLabels = Object.keys(androidKeySets);
for (let i = 0; i < androidLabels.length; i++) {
    for (let j = i + 1; j < androidLabels.length; j++) {
        const a = androidLabels[i], b = androidLabels[j];
        const ka = androidKeySets[a], kb = androidKeySets[b];
        const aOnly = [...ka].filter(k => !kb.has(k));
        const bOnly = [...kb].filter(k => !ka.has(k));
        if (aOnly.length) fail(`[android.strings] ${a}/ 有 ${b}/ 无: ${aOnly.join(', ')}`);
        if (bOnly.length) fail(`[android.strings] ${b}/ 有 ${a}/ 无: ${bOnly.join(', ')}`);
    }
}

// --- server↔frontend: handler.go 发射的 E_XXX code 集合 须与 zh.js/en.js errors 命名空间一致.
// 防「服务端新增 writeErr(w,503,"E_NEW_CODE") 但忘更新 zh/en」→ 用户看到原始 code 字符串.
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
    if (serverOnly.length) fail(`[error-contract] server 发射但前端 errors 无翻译: ${serverOnly.join(', ')}`);
    if (frontendOnly.length) fail(`[error-contract] 前端 errors 翻译但 server 不发射: ${frontendOnly.join(', ')}`);
} else {
    fail('[error-contract] server/handler.go 不存在, 跳过 server↔frontend parity');
}

if (failures === 0) {
    console.log('i18n parity OK: web zh/en + android values/values-zh/values-zh-rCN/values-b+zh+Hant + server↔frontend error code 一致');
    process.exit(0);
} else {
    console.error(`i18n parity 失败: ${failures} 处不一致`);
    process.exit(1);
}
