// verify.js — 本地字节级校验工具 (ZIP 中央目录解析 + hash-wasm SHA-256).
//
// 两阶段校验 (Flow):
//   1. 用户点 [🔍 校验已下载zip文件] → 选本地 ZIP (浏览器 file picker)
//   2. verify.js 流式解析 ZIP 中央目录 (parseZipCentralDirectory), 建立
//      fileMap: path → { size, offset }, 单独读出 manifest.json
//   3. 阶段 1 — 大小预筛 (秒级): 遍历 manifest.files, 缺失 / size 不符
//      立即分类, 不进入 SHA-256 (省时间)
//   4. 阶段 2 — 字节级 SHA-256 (对 size 匹配的 entries): 读 local header
//      算出 dataStart, file.slice 出 entry Blob (引用, 不入内存), 发给
//      hash-wasm Worker 流式 1MB 分块哈希, 比对 manifest.files[].sha256
//   5. UI 四类结果: ✔ 字节级完整 / ✗ 字节损坏 (sha 不符) /
//                  ⚠️ 大小不符 / ✗ 缺失
//
// 为什么用 hash-wasm (WASM) 而非 SubtleCrypto: PC 浏览器经 HTTP 局域网
// 访问手机 server (非 HTTPS / 非 localhost), crypto.subtle 不可用
// (secure-context 限制). WASM 不挑 context, vendor 已内置.
//
// 大文件不爆内存: readZipEntry 仅用于读 manifest.json (几 KB). 单个 entry
// 的 SHA-256 走独立路径 — getDataStart 只读 30 字节 local header, 然后
// file.slice 拿 Blob (引用), Worker 内部按 1MB 流式哈希, 峰值内存 ~1MB.
//
// 取消: 用户随时点 [取消] → terminate Worker (pending 全部 reject), 下次
// 校验时 ensureWorker() 重建.
//
// i18n: 所有用户可见文案经 window.I18N.t (verify_* key, 详见 locales/zh|en.js).
// I18N 由 i18n.js 在本文件之前加载, 全局可用.
(function () {
    'use strict';

    const btnVerify = document.getElementById('btn-verify');
    const zipInput = document.getElementById('verify-zip-input');
    const panel = document.getElementById('verify-panel');
    const summary = document.getElementById('verify-summary');
    const progressBox = document.getElementById('verify-progress');
    const progressFill = document.getElementById('verify-progress-fill');
    const progressText = document.getElementById('verify-progress-text');
    const cancelBtn = document.getElementById('verify-cancel');
    const results = document.getElementById('verify-results');

    if (!btnVerify) return;

    // i18n 守卫: i18n.js 加载失败时 I18N 未定义, 这里 return 避免后续事件回调里
    // 调 I18N.t 抛 ReferenceError. 静态 data-i18n 元素仍由 app.js 的兜底逻辑覆盖.
    if (typeof I18N === 'undefined') return;

    let cancelled = false;
    // 当前校验阶段快照 (切语言时 rerender 据此重设 verify-progress-text/verify-summary,
    // 二者无 data-i18n, applyToDOM 跳过). summary = {key, params, strong} 描述当前 summary
    // 元素文案 ('normal'→<strong>, 'gray'→<strong style=color:#6b7280>, null→纯文本),
    // 切语言时据此重建, 避免 parsing/stage/cancelled 态残留旧语言.
    let verifyState = { phase: 'idle', stage: null, summary: null };

    function show() { if (panel) panel.classList.remove('hidden'); }
    function hide() { if (panel) panel.classList.add('hidden'); }
    function reset() {
        if (summary) summary.innerHTML = '';
        if (results) results.innerHTML = '';
        if (progressBox) progressBox.classList.add('hidden');
        if (progressFill) progressFill.style.width = '0%';
        if (progressText) progressText.textContent = '';
        cancelled = false;
        verifyState = { phase: 'idle', stage: null, summary: null };
    }

    function renderSummary(s) {
        if (!s || !summary) return;
        const text = I18N.t(s.key, s.params || {});
        if (s.strong === 'normal') summary.innerHTML = '<strong>' + text + '</strong>';
        else if (s.strong === 'gray') summary.innerHTML = '<strong style="color:#6b7280">' + text + '</strong>';
        else summary.textContent = text;
    }

    // Expose show/hide/rerender for app.js (切语言时 onLocaleChange 调 rerender 重渲动态文案).
    function rerender() {
        if (!verifyState) return;
        renderSummary(verifyState.summary);
        if (verifyState.stage && progressText) {
            if (verifyState.phase === 'stage1') progressText.textContent = I18N.t('verify_stage1', verifyState.stage);
            else if (verifyState.phase === 'stage2') progressText.textContent = I18N.t('verify_stage2', verifyState.stage);
        }
        // done 态走 modal (独立 DOM, 不在此重渲).
    }
    window.photosmoveVerify = { show, hide, rerender };

    // 点 [完整性校验] 直接打开 ZIP 选择器, panel 暂不展开 (避免空白白底).
    // panel 在 onZipPicked 内 (用户选完 ZIP 后) 才 remove('hidden').
    btnVerify.addEventListener('click', () => {
        reset();
        if (zipInput) zipInput.click();
    });

    if (cancelBtn) {
        cancelBtn.addEventListener('click', () => {
            cancelled = true;
            verifyState = { phase: 'cancelled', stage: null, summary: { key: 'verify_cancelled', strong: 'gray' } };
            // terminate Worker 并 reject 所有 in-flight 哈希 (下次校验时重建)
            destroyHasher();
            if (progressBox) progressBox.classList.add('hidden');
            renderSummary(verifyState.summary);
        });
    }

    function fmtSize(b) {
        if (b < 1024) return b + ' B';
        if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
        if (b < 1024 * 1024 * 1024) return (b / (1024 * 1024)).toFixed(1) + ' MB';
        return (b / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
    }

    function escapeHtml(s) {
        return String(s).replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        }[c]));
    }

    // (路径 A "选解压目录" 已移除 — 现在统一只支持 ZIP 选择, 简化交互.)

    // 校验入口 (公用): 接收 fileMap (path → {size, offset}) + ZIP File + manifest.
    // 两阶段: (1) 大小预筛 — 缺失/size 不符立即分类; (2) SHA-256 字节级 — 对
    // size 匹配的 entries 逐个 hash-wasm 校验. sourceLabel 用于结果弹窗标题.
    async function runVerify(fileMap, file, manifest, sourceLabel) {
        const entries = manifest.files;
        let mismatched = 0, missing = 0;
        let byteOk = 0, corrupted = 0, sizeOnly = 0;
        const mismatchedFiles = [];
        const missingFiles = [];
        const corruptedFiles = [];
        const sizeMatched = []; // 通过 size 预筛, 待 SHA-256 校验

        // ===== 阶段 1: 大小预筛 (秒级) =====
        for (let i = 0; i < entries.length; i++) {
            if (cancelled) break;
            const entry = entries[i];
            const f = fileMap.get(entry.path);
            if (!f) {
                missing++;
                missingFiles.push(entry.path);
            } else if (f.size !== entry.size) {
                mismatched++;
                mismatchedFiles.push({ path: entry.path, expected: entry.size, actual: f.size });
            } else {
                sizeMatched.push({ entry, f });
            }
            if (i % 50 === 0 || i === entries.length - 1) {
                if (progressFill) progressFill.style.width = '100%';
                verifyState = { phase: 'stage1', stage: { i: i + 1, n: entries.length }, summary: verifyState.summary };
                if (progressText) progressText.textContent = I18N.t('verify_stage1', { i: i + 1, n: entries.length });
                // Yield 让进度条重绘.
                await new Promise(r => setTimeout(r, 0));
            }
        }

        if (cancelled) {
            verifyState = { phase: 'cancelled', stage: null, summary: { key: 'verify_cancelled', strong: 'gray' } };
            if (progressText) progressText.textContent = I18N.t('verify_cancelled');
            return;
        }

        // ===== 阶段 2: SHA-256 字节级 (对 size 匹配的 entries) =====
        if (sizeMatched.length > 0) {
            ensureHasher();
            const total = sizeMatched.length;
            for (let i = 0; i < total; i++) {
                if (cancelled) break;
                const { entry, f } = sizeMatched[i];

                if (!entry.sha256) {
                    // manifest 无 sha256 留底 — 仅 size 匹配, 无法字节级校验.
                    sizeOnly++;
                } else {
                    let sha = null;
                    try {
                        const dataStart = await getDataStart(file, f);
                        // Blob 是引用 (不读入内存), Worker 内部按 1MB 流式哈希.
                        const entryBlob = file.slice(dataStart, dataStart + entry.size);
                        sha = await hashEntry(entryBlob);
                    } catch (err) {
                        if (cancelled) break;
                        throw err;
                    }
                    if (cancelled) break;
                    if (sha === entry.sha256) {
                        byteOk++;
                    } else {
                        corrupted++;
                        corruptedFiles.push({ path: entry.path, expected: entry.sha256, actual: sha });
                    }
                }

                const pct = Math.round(((i + 1) / total) * 100);
                if (progressFill) progressFill.style.width = pct + '%';
                verifyState = { phase: 'stage2', stage: { i: i + 1, n: total }, summary: verifyState.summary };
                if (progressText) progressText.textContent = I18N.t('verify_stage2', { i: i + 1, n: total });
                await new Promise(r => setTimeout(r, 0));
            }
        }

        if (cancelled) {
            verifyState = { phase: 'cancelled', stage: null, summary: { key: 'verify_cancelled', strong: 'gray' } };
            if (progressText) progressText.textContent = I18N.t('verify_cancelled');
            return;
        }

        // ===== 结果弹窗 (四类) =====
        const totalSize = entries.reduce((s, e) => s + (e.size || 0), 0);
        let html = '<div class="verify-modal-title">' + I18N.t('verify_result_title', { source: escapeHtml(sourceLabel) }) + '</div>';
        html += '<div class="verify-modal-stat ok">' + I18N.t('verify_ok', { count: byteOk }) + '</div>';
        if (corrupted > 0) {
            html += '<div class="verify-modal-stat err">' + I18N.t('verify_corrupted', { count: corrupted }) + '</div>';
        }
        if (mismatched > 0) {
            html += '<div class="verify-modal-stat warn">' + I18N.t('verify_size_mismatch', { count: mismatched }) + '</div>';
        }
        if (missing > 0) {
            html += '<div class="verify-modal-stat err">' + I18N.t('verify_missing', { count: missing }) + '</div>';
        }
        if (sizeOnly > 0) {
            html += '<div class="verify-modal-stat meta">' + I18N.t('verify_size_only', { count: sizeOnly }) + '</div>';
        }
        html += '<div class="verify-modal-meta">' + I18N.t('verify_total', { size: fmtSize(totalSize), count: entries.length }) + '</div>';

        // 详情列表 (损坏 + 不符 + 缺失, 每类 cap 25).
        const DETAIL_CAP = 25;
        const cCap = corruptedFiles.slice(0, DETAIL_CAP);
        const mCap = mismatchedFiles.slice(0, DETAIL_CAP);
        const missCap = missingFiles.slice(0, DETAIL_CAP);
        const all = [];
        for (const c of cCap) {
            const exp = String(c.expected || '').slice(0, 12);
            const act = String(c.actual || '').slice(0, 12);
            all.push('<li class="verify-err">' + escapeHtml(c.path) + ' — ' + I18N.t('verify_sha_mismatch_detail', { exp: exp, act: act }) + '</li>');
        }
        for (const m of mCap) {
            all.push('<li class="verify-warn">' + escapeHtml(m.path) + ' — ' + I18N.t('verify_size_detail', { exp: fmtSize(m.expected), act: fmtSize(m.actual) }) + '</li>');
        }
        for (const m of missCap) {
            all.push('<li class="verify-err">' + escapeHtml(m.path) + ' — ' + I18N.t('verify_missing_short') + '</li>');
        }
        const moreCount = Math.max(0, corruptedFiles.length - DETAIL_CAP)
            + Math.max(0, mismatchedFiles.length - DETAIL_CAP)
            + Math.max(0, missingFiles.length - DETAIL_CAP);
        if (moreCount > 0) {
            all.push('<li class="verify-more">' + I18N.t('verify_more', { count: moreCount }) + '</li>');
        }
        if (all.length > 0) {
            html += '<ul class="verify-modal-list">' + all.join('') + '</ul>';
        }

        showResultModal(html);
        if (panel) panel.classList.add('hidden');
        if (progressBox) progressBox.classList.add('hidden');
    }

    // ============== hash-wasm Worker 管理 (SHA-256 字节级) ==============
    //
    // 单 Worker 串行处理: 简单 + 避免并发拉多个大 Blob 进内存. 每个 entry
    // postMessage 一个 Blob (引用, 不拷贝全量), Worker 流式 1MB 分块哈希.
    // Promise + id 关联 (Map<id, resolve/reject>), 取消时 terminate Worker
    // 并 reject 所有 pending, 下次校验 ensureHasher() 重建.
    let hasherWorker = null;
    let nextHashId = 1;
    const pendingHashes = new Map(); // id → { resolve, reject }

    function ensureHasher() {
        if (hasherWorker) return hasherWorker;
        hasherWorker = new Worker('hash-wasm.worker.js');
        hasherWorker.onmessage = (ev) => {
            const msg = ev.data;
            if (!msg || typeof msg !== 'object') return;
            const p = pendingHashes.get(msg.id);
            if (!p) return;
            pendingHashes.delete(msg.id);
            if (msg.error) p.reject(new Error(msg.error));
            else p.resolve(msg.sha256);
        };
        hasherWorker.onerror = (e) => {
            const err = new Error(String((e && e.message) || 'hash worker error'));
            for (const [, p] of pendingHashes) p.reject(err);
            pendingHashes.clear();
        };
        return hasherWorker;
    }

    function destroyHasher() {
        if (hasherWorker) {
            hasherWorker.terminate();
            hasherWorker = null;
        }
        // terminate 不会触发 onmessage → 主动 reject 所有 in-flight, 唤醒 await.
        for (const [, p] of pendingHashes) p.reject(new Error('cancelled'));
        pendingHashes.clear();
    }

    function hashEntry(blob) {
        const id = nextHashId++;
        return new Promise((resolve, reject) => {
            pendingHashes.set(id, { resolve, reject });
            hasherWorker.postMessage({ type: 'hash', id, file: blob });
        });
    }

    // getDataStart: 读 entry 的 local file header (30 字节) 算出数据起点.
    // 与 readZipEntry 共享 local header 解析逻辑, 但只返回 dataStart 偏移,
    // 不把 entry 内容读成 Uint8Array (避免大文件爆内存). 调用方用 file.slice
    // 拿 Blob 引用后交给 Worker 流式哈希.
    //   local header layout:  [0..4)  签名 0x04034b50
    //                         [26..28) fileNameLen
    //                         [28..30) extraLen
    //   dataStart = offset + 30 + fileNameLen + extraLen
    async function getDataStart(file, entry) {
        const lh = new Uint8Array(await file.slice(entry.offset, entry.offset + 30).arrayBuffer());
        const lhView = new DataView(lh.buffer);
        if (lhView.getUint32(0, true) !== 0x04034b50) {
            throw new Error(I18N.t('verify_err_local_header', { path: entry.path }));
        }
        const fileNameLen = lhView.getUint16(26, true);
        const extraLen = lhView.getUint16(28, true);
        return entry.offset + 30 + fileNameLen + extraLen;
    }

    // ============== 直接选 ZIP 校验 (流式中央目录解析, 不解压内容) ==============
    //
    // PhotosMove ZIP 用 Store 模式 (不压缩), verify 只需要每个 entry 的
    // uncompressed_size, 不需要解压内容. 直接读 ZIP 中央目录 (CD) 提取
    // filename + size, manifest.json 单独用 file.slice 局部读取.
    //
    // 内存占用: 末尾 64KB (找 EOCD) + 中央目录字节 (CD 通常 < 1MB/1000 文件)
    // + manifest.json 本身 (几 KB). 总计 ~几 MB, 与 ZIP 总大小无关.
    // 对比旧实现 (fflate.unzipSync): 1GB ZIP 占 3GB 内存, 9.84GB ZIP 直接 OOM.

    // 读小端 uint64 (ZIP64), 返回 Number. JS Number 最大安全整数 2^53,
    // 单文件 < 8PB 都能表示, 远超实际需求.
    function readU64(view, offset, le) {
        return Number(view.getBigUint64(offset, le));
    }

    // 在 CD entry 的 extra 字段里找 ZIP64 扩展 (tag 0x0001), 按需读取 64-bit 字段.
    // ZIP64 字段顺序固定为: OriginalSize → CompressedSize → LocalHeaderOffset → DiskNumber
    // (CD 主项中对应字段 == 0xFFFFFFFF 时, 该字段才出现在 extra 里).
    // 解析时必须按此顺序累加 q, 不能因 wantCompressed=false 就跳过 — 否则读 offset 会错位.
    function readZip64Extra(view, extraOff, extraLen, uncompIsZ64, compIsZ64, offsetIsZ64) {
        let uncompressed = null, compressed = null, offset = null;
        let p = extraOff;
        const end = extraOff + extraLen;
        while (p + 4 <= end) {
            const tag = view.getUint16(p, true);
            const sz = view.getUint16(p + 2, true);
            if (tag === 0x0001) {
                let q = p + 4;
                // 按 spec 顺序读所有出现的字段, 即使 caller 不关心也要推进 q.
                if (uncompIsZ64) { uncompressed = readU64(view, q, true); q += 8; }
                if (compIsZ64) { compressed = readU64(view, q, true); q += 8; }
                if (offsetIsZ64) { offset = readU64(view, q, true); q += 8; }
                return { uncompressed, compressed, offset };
            }
            p += 4 + sz;
        }
        return { uncompressed, compressed, offset };
    }

    // 显示错误并隐藏进度条 (不再残留"取消"按钮 + 0% 进度条).
    function showError(title, detail) {
        const html = '<div class="verify-modal-title err">' + escapeHtml(title) + '</div>' +
            (detail ? '<div class="verify-modal-meta">' + escapeHtml(detail) + '</div>' : '');
        showResultModal(html);
        if (panel) panel.classList.add('hidden');
        if (progressBox) progressBox.classList.add('hidden');
    }

    // 校验结果弹窗: 不在页面展示 (避免占用 dashboard 空间). 点遮罩或 Esc 关闭.
    let _resultEscHandler = null;
    function showResultModal(innerHTML) {
        hideResultModal();
        const mask = document.createElement('div');
        mask.className = 'verify-modal-mask';
        mask.innerHTML = '<div class="verify-modal">' + innerHTML + '</div>';
        // 点 mask (弹窗外) → 关闭; 点 modal 内容 → stopPropagation 不关闭.
        mask.addEventListener('click', hideResultModal);
        const modal = mask.querySelector('.verify-modal');
        if (modal) modal.addEventListener('click', e => e.stopPropagation());
        document.body.appendChild(mask);
        _resultEscHandler = ev => { if (ev.key === 'Escape') hideResultModal(); };
        document.addEventListener('keydown', _resultEscHandler);
    }
    function hideResultModal() {
        const m = document.querySelector('.verify-modal-mask');
        if (m) m.remove();
        if (_resultEscHandler) {
            document.removeEventListener('keydown', _resultEscHandler);
            _resultEscHandler = null;
        }
    }

    async function onZipPicked(e) {
        const file = e.target.files && e.target.files[0];
        if (!file) return;
        reset();
        if (panel) panel.classList.remove('hidden');
        if (progressBox) progressBox.classList.remove('hidden');
        verifyState = { phase: 'parsing', stage: null, summary: { key: 'verify_parsing_cd', params: { name: escapeHtml(file.name), size: fmtSize(file.size) }, strong: 'normal' } };
        renderSummary(verifyState.summary);

        try {
            const result = await parseZipCentralDirectory(file);
            if (!result.manifestEntry) {
                showError(I18N.t('verify_no_manifest'), I18N.t('verify_no_manifest_hint'));
                return;
            }

            // 读 manifest.json 的实际字节 (用 file.slice 只读这一小块).
            const manifestBytes = await readZipEntry(file, result.manifestEntry);
            let manifest;
            try {
                manifest = JSON.parse(new TextDecoder().decode(manifestBytes));
            } catch (err) {
                showError(I18N.t('verify_manifest_parse_failed'), err.message);
                return;
            }
            if (!manifest.files || !Array.isArray(manifest.files)) {
                showError(I18N.t('verify_manifest_invalid'));
                return;
            }

            await runVerify(result.fileMap, file, manifest, I18N.t('verify_source_zip'));
        } catch (err) {
            showError(I18N.t('verify_zip_parse_failed'), err.message || String(err));
        }
    }

    // parseZipCentralDirectory 流式解析 ZIP 中央目录, 返回 { fileMap, manifestEntry }.
    // fileMap: Map<path, { size, offset, compressed }> (offset 指向 local header)
    // manifestEntry: 第一个 path 包含 'manifest.json' 的 entry.
    // 流式从文件末尾往前扫描 EOCD 签名 (0x06054b50).
    // 为什么不固定读末尾 64KB? 因为 server 端 padToZipSize 可能在 ZIP 末尾追加
    // 零字节 padding (HEIC 3x 估算偏差 / EXIF strip 后实际变小), 如果 padding
    // 超过 64KB, 固定窗口扫不到 EOCD. 改为流式扫描, 每次读 1MB chunk 往前推,
    // 找到签名即停. 最坏情况扫整个文件, 但实际很快 (EOCD 总在末尾附近).
    async function findEocd(file) {
        const chunkSize = 1024 * 1024; // 1 MB
        const minEocdSize = 22; // EOCD 最少 22 字节
        let fileOffset = file.size; // 末尾位置, 往前推
        const chunks = []; // 缓存已读 chunks, 后续 ZIP64 locator/EOCD 解析可能需要
        while (fileOffset > 0) {
            const readSize = Math.min(chunkSize, fileOffset);
            const start = fileOffset - readSize;
            const buf = new Uint8Array(await file.slice(start, fileOffset).arrayBuffer());
            const view = new DataView(buf.buffer);
            // 在当前 chunk 内从后往前扫
            for (let i = buf.length - minEocdSize; i >= 0; i--) {
                if (view.getUint32(i, true) === 0x06054b50) {
                    return { start, position: i, buf, view };
                }
            }
            chunks.unshift({ start, buf, view });
            fileOffset = start;
            // 安全上限: 不扫超过 file.size (整个文件最坏情况). 但实际 EOCD
            // 一定在末尾 64KB 内 (除非有大量 padding), 通常 1-2 个 chunk 找到.
            if (chunks.length > 64) break; // 64 MB 上限, 防极端情况
        }
        return null;
    }

    async function parseZipCentralDirectory(file) {
        // 1. 流式找 EOCD (End of Central Directory)
        const eocd = await findEocd(file);
        if (!eocd) throw new Error(I18N.t('verify_err_no_eocd'));
        const { start: eocdChunkStart, position: eocdOff, view: tailView } = eocd;
        // EOCD 在整个文件中的绝对 offset
        const eocdAbsOffset = eocdChunkStart + eocdOff;

        let totalEntries = tailView.getUint16(eocdOff + 10, true);
        let cdSize = tailView.getUint32(eocdOff + 12, true);
        let cdOffset = tailView.getUint32(eocdOff + 16, true);

        // 2. 检查 ZIP64 (任一字段 == 0xFFFFFFFF 都说明有 ZIP64 扩展)
        if (totalEntries === 0xFFFF || cdOffset === 0xFFFFFFFF || cdSize === 0xFFFFFFFF) {
            // ZIP64 EOCD Locator 在 EOCD 前 20 字节, 签名 0x07064b50
            // locator 可能跨 chunk 边界, 简化处理: 读 eocdAbsOffset - 20 起 20 字节
            const locatorBuf = new Uint8Array(await file.slice(eocdAbsOffset - 20, eocdAbsOffset).arrayBuffer());
            const locatorView = new DataView(locatorBuf.buffer);
            if (locatorView.getUint32(0, true) === 0x07064b50) {
                // locator + 4 (disk) + 8 (zip64 eocd offset, 64-bit)
                const z64EocdOffsetLow = locatorView.getUint32(8, true);
                const z64EocdOffsetHigh = locatorView.getUint32(12, true);
                const z64EocdOffset = z64EocdOffsetHigh * 0x100000000 + z64EocdOffsetLow;

                // ZIP64 EOCD 56 字节起, 签名 0x06064b50
                const z64 = new Uint8Array(await file.slice(z64EocdOffset, z64EocdOffset + 56).arrayBuffer());
                const z64View = new DataView(z64.buffer);
                if (z64View.getUint32(0, true) !== 0x06064b50) throw new Error(I18N.t('verify_err_zip64_eocd'));
                totalEntries = readU64(z64View, 32, true);  // 实际 entries (CD 这一个 disk)
                cdSize = readU64(z64View, 40, true);
                cdOffset = readU64(z64View, 48, true);
            } else {
                throw new Error(I18N.t('verify_err_zip64_locator'));
            }
        }

        // 3. 读中央目录, 遍历 entries
        const cd = new Uint8Array(await file.slice(cdOffset, cdOffset + cdSize).arrayBuffer());
        const cdView = new DataView(cd.buffer);

        const fileMap = new Map();
        let manifestEntry = null;
        let off = 0;
        for (let i = 0; i < totalEntries; i++) {
            if (off + 46 > cd.length) throw new Error(I18N.t('verify_err_cd_oob', { i: i }));
            if (cdView.getUint32(off, true) !== 0x02014b50) throw new Error(I18N.t('verify_err_cd_sig', { i: i }));

            let uncompressedSize = cdView.getUint32(off + 24, true);
            const fileNameLen = cdView.getUint16(off + 28, true);
            const extraLen = cdView.getUint16(off + 30, true);
            const commentLen = cdView.getUint16(off + 32, true);
            let localHeaderOffset = cdView.getUint32(off + 42, true);

            const fileNameBytes = cd.subarray(off + 46, off + 46 + fileNameLen);
            const path = new TextDecoder().decode(fileNameBytes);

            // ZIP64 扩展: 0xFFFFFFFF → 实际值在 extra
            const compSize32 = cdView.getUint32(off + 20, true);
            if (uncompressedSize === 0xFFFFFFFF || compSize32 === 0xFFFFFFFF || localHeaderOffset === 0xFFFFFFFF) {
                const z = readZip64Extra(cdView, off + 46 + fileNameLen, extraLen,
                    uncompressedSize === 0xFFFFFFFF,
                    compSize32 === 0xFFFFFFFF,
                    localHeaderOffset === 0xFFFFFFFF);
                if (z.uncompressed !== null) uncompressedSize = z.uncompressed;
                if (z.offset !== null) localHeaderOffset = z.offset;
            }

            fileMap.set(path, { size: uncompressedSize, offset: localHeaderOffset });

            if (!manifestEntry && (path === 'manifest.json' || path.endsWith('/manifest.json'))) {
                manifestEntry = { path, size: uncompressedSize, offset: localHeaderOffset };
            }

            off += 46 + fileNameLen + extraLen + commentLen;
        }

        return { fileMap, manifestEntry };
    }

    // readZipEntry 用 file.slice 只读单个 entry 的内容字节 (不解压其他文件).
    // Entry 的 offset 指向 local file header (签名 0x04034b50), 30 字节固定
    // + filename + extra 之后才是数据.
    async function readZipEntry(file, entry) {
        const lh = new Uint8Array(await file.slice(entry.offset, entry.offset + 30).arrayBuffer());
        const lhView = new DataView(lh.buffer);
        if (lhView.getUint32(0, true) !== 0x04034b50) throw new Error(I18N.t('verify_err_local_header', { path: entry.path }));
        const fileNameLen = lhView.getUint16(26, true);
        const extraLen = lhView.getUint16(28, true);
        const dataStart = entry.offset + 30 + fileNameLen + extraLen;
        const data = new Uint8Array(await file.slice(dataStart, dataStart + entry.size).arrayBuffer());
        return data;
    }

    if (zipInput) zipInput.addEventListener('change', onZipPicked);
})();
