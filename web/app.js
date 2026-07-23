// PhotosMove browser client — camera download (i18n: 所有文案经 I18N.t, 组3.3-3.5/5.4/4.3)
(function () {
    'use strict';

    let serverUrl = '';
    let authToken = '';
    let cachedAlbums = null;

    serverUrl = location.origin;
    const TOKEN_KEY = 'photosmove_token_' + serverUrl;

    // --- Token persistence ---
    function saveToken(token) {
        authToken = token;
        try { localStorage.setItem(TOKEN_KEY, token); } catch (e) {}
    }

    function loadToken() {
        try { return localStorage.getItem(TOKEN_KEY) || ''; } catch (e) { return ''; }
    }

    function clearToken() {
        authToken = '';
        try { localStorage.removeItem(TOKEN_KEY); } catch (e) {}
    }

    // --- DOM refs ---
    const connectView = document.getElementById('connect-view');
    const dashboardView = document.getElementById('dashboard-view');
    const connectForm = document.getElementById('connect-form');
    const pinInput = document.getElementById('pin-input');
    // 进入 PIN 页自动聚焦输入框(免用户手动点)
    if (connectView && !connectView.classList.contains('hidden')) { setTimeout(() => pinInput.focus(), 50); }
    const connectError = document.getElementById('connect-error');
    const cardTitle = document.getElementById('card-title');
    const cardDesc = document.getElementById('card-desc');
    const progressSection = document.getElementById('progress-section');
    const progressBar = document.getElementById('progress-bar');
    const progressPct = document.getElementById('progress-pct');
    const progressSpeed = document.getElementById('progress-speed');
    const progressEta = document.getElementById('progress-eta');
    const progressDownloaded = document.getElementById('progress-downloaded');
    const progressAlbum = document.getElementById('progress-album');
    const doneBadge = document.getElementById('done-badge');
    const btnDownload = document.getElementById('btn-download');
    const consoleToggle = document.getElementById('console-toggle');
    const consoleBox = document.getElementById('console-box');

    // --- i18n init (组3.5/5.4): 应用静态文案 + 绑定语言切换入口 ---
    // 守卫: i18n.js 加载失败时 I18N 未定义, 不能让 app.js 在 IIFE 顶部崩溃 ——
    // 否则末尾的 window.error 兜底 (showFatalError) 也用 I18N.t 二次崩溃 → 白屏无错误.
    // 用不依赖 I18N 的原生提示兜底, 至少给用户一行可读信息.
    if (typeof I18N === 'undefined') {
        document.body.innerHTML = '<div style="padding:40px;font-family:system-ui,sans-serif;color:#991b1b;text-align:center">PhotosMove failed to load (i18n). Please reload the page.</div>';
        return;
    }
    I18N.applyToDOM();
    refreshLangLabels();
    // card-desc / progress-downloaded 由 app.js 动态管理: 切语言时 applyToDOM 不能覆盖它们
    // (否则进度跌回 "0 B / 0 B"). 故 index.html 上不带 data-i18n, 初始文案由这里显式设一次,
    // 之后 updateCard / applyProgress 接管.
    cardDesc.textContent = I18N.t('card_desc_loading');
    progressDownloaded.textContent = I18N.t('progress_init');
    document.querySelectorAll('[data-lang-switch]').forEach(function (el) {
        el.addEventListener('click', showLangPicker);
    });

    function refreshLangLabels() {
        document.querySelectorAll('[data-lang-label]').forEach(function (el) {
            el.textContent = I18N.currentLabel();
        });
    }

    function showLangPicker() {
        const current = I18N.getLocale();
        const mask = document.createElement('div');
        mask.className = 'big-file-modal-mask';
        const items = I18N.SUPPORTED.map(function (code) {
            const label = code === 'zh' ? I18N.t('lang_label') : I18N.t('lang_en_label');
            const sel = code === current;
            return '<div data-lang-code="' + code + '" style="padding:14px 20px;cursor:pointer;border-bottom:1px solid #f0f0f0;' + (sel ? 'color:#4f46e5;font-weight:600;background:rgba(99,102,241,0.05)' : '') + '">' + (sel ? '✓ ' : '') + label + '</div>';
        }).join('');
        mask.innerHTML = '<div class="big-file-modal" style="max-width:280px;padding:0"><div style="padding:14px 20px;font-size:16px;text-align:center">🌐</div>' + items + '</div>';
        document.body.appendChild(mask);
        mask.querySelectorAll('[data-lang-code]').forEach(function (el) {
            el.addEventListener('click', function () {
                const code = el.getAttribute('data-lang-code');
                mask.parentNode.removeChild(mask);
                if (code !== current) {
                    I18N.setLocale(code);
                    refreshLangLabels();
                    onLocaleChange();
                }
            });
        });
        mask.addEventListener('click', function (e) { if (e.target === mask) mask.parentNode.removeChild(mask); });
    }

    // 语言切换后重渲染动态文案. 严格保留 downloading 状态 (design Risks ④⑤:
    // 不重启下载/不清进度). applyToDOM 只刷新带 data-i18n 的静态元素; 下载中及刚完成/
    // 中断态动态管理的元素 (card-desc/progressAlbum/progressSpeed/progressEta/
    // progressDownloaded/大文件块) 均无 data-i18n, 必须按当前状态机统一重渲, 否则残留
    // 旧语言 —— 尤其 success/interrupted 态无 poll 刷新, 会永久冻结 (review finding).
    function refreshProgressText() {
        if (dlState.mode === 'init') {
            progressDownloaded.textContent = I18N.t('progress_init');
            progressSpeed.textContent = '--';
            progressEta.textContent = '--';
            return;
        }
        progressDownloaded.textContent = I18N.t('progress_downloaded', { done: formatSize(dlState.overall), total: formatSize(dlState.total) });
        if (dlState.mode === 'complete') {
            progressSpeed.textContent = I18N.t('progress_complete');
            progressEta.textContent = '';
        } else if (dlState.mode === 'interrupted') {
            progressSpeed.textContent = I18N.t('progress_interrupted');
            progressEta.textContent = I18N.t('progress_see_downloader');
        } else if (dlState.mode === 'preparing' || dlState.overall === 0) {
            progressSpeed.textContent = I18N.t('progress_preparing');
            progressEta.textContent = '--';
        } else if (dlState.mode === 'paused') {
            progressSpeed.textContent = I18N.t('progress_paused');
            progressEta.textContent = '--';
        } else {
            progressSpeed.textContent = formatSize(dlState.speed) + '/s';
            progressEta.textContent = I18N.t('remaining', { time: formatTime(dlState.remaining) });
        }
    }

    function refreshDynamicText() {
        // card-desc (无 data-i18n, updateCard 管理): 必须与 updateCard 的全部分支一致,
        // 否则切语言后空状态/无相机相册态会被覆盖成 loading (review 第三轮 finding).
        if (!cachedAlbums || cachedAlbums.length === 0) {
            cardDesc.textContent = I18N.t('no_photos');
        } else {
            const cameraAlbums = cachedAlbums.filter(a => a.category === 'camera');
            if (cameraAlbums.length === 0) {
                cardDesc.textContent = I18N.t('no_camera_photos');
            } else {
                const totalFiles = cameraAlbums.reduce((s, a) => s + a.file_count, 0);
                const totalSize = cameraAlbums.reduce((s, a) => s + a.total_size, 0);
                cardDesc.innerHTML = I18N.t('card_files_html', { count: totalFiles.toLocaleString(), size: formatSize(totalSize) });
            }
        }

        // 按钮: 与 updateCard 一致 — downloading→cancel, complete→redownload,
        // 无相册/无相机相册→nothing_to_download, 否则→download_all.
        // 不调 updateCard —— 否则 success 态会把 redownload 覆盖回 download_all.
        if (downloading) {
            btnDownload.textContent = I18N.t('cancel_transfer');
        } else if (dlState.mode === 'complete') {
            btnDownload.textContent = I18N.t('redownload');
        } else if (!cachedAlbums || cachedAlbums.length === 0) {
            btnDownload.textContent = I18N.t('nothing_to_download');
        } else {
            const cameraAlbums = cachedAlbums.filter(a => a.category === 'camera');
            if (cameraAlbums.length === 0) {
                btnDownload.textContent = I18N.t('nothing_to_download');
            } else {
                const totalSize = cameraAlbums.reduce((s, a) => s + a.total_size, 0);
                btnDownload.textContent = I18N.t('download_all', { size: formatSize(totalSize) });
            }
        }

        // 进度区动态元素 (无 data-i18n): 仅在进度区可见时重渲 (覆盖 downloading/success/interrupted).
        if (!progressSection.classList.contains('hidden')) {
            if (activeAlbumMeta) progressAlbum.textContent = I18N.t(activeAlbumMeta.key, activeAlbumMeta.params);
            refreshProgressText();
        }

        // 大文件块 (无 data-i18n, 可能停留数十分钟).
        if (activeBigFileBatch) renderBigFileBatch(activeBigFileBatch);

        // verify.js 动态元素 (无 data-i18n, 独立 IIFE): 暴露 rerender 钩子时调用.
        if (window.photosmoveVerify && typeof window.photosmoveVerify.rerender === 'function') {
            window.photosmoveVerify.rerender();
        }
    }

    function onLocaleChange() {
        refreshDynamicText();
    }

    // --- Connect ---
    connectForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        connectError.innerHTML = '';
        connectError.classList.remove('show');
        const pin = pinInput.value.trim();
        const submitBtn = connectForm.querySelector('button[type="submit"]');
        submitBtn.disabled = true;
        submitBtn.textContent = I18N.t('connecting');

        const authUrl = `${serverUrl}/api/auth`;
        try {
            const res = await fetch(authUrl, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ pin }),
            });
            if (!res.ok) {
                // 4.3 ①: 服务端 error code 契约 → I18N.te(code), 未知 code 原样返回
                let errMsg = I18N.t('err_server_status', { status: res.status });
                try { const d = await res.json(); if (d.code) errMsg = I18N.te(d.code, { detail: d.detail }); else if (d.error) errMsg = d.error; } catch (e) {}
                throw new Error(errMsg);
            }
            const data = await res.json();
            saveToken(data.token);
            showDashboard();
        } catch (err) {
            // 4.3 ③: fetch 网络层失败 (TypeError, 无响应体如 Failed to fetch) → 本地化文案
            const isNetworkErr = err instanceof TypeError;
            const displayMsg = isNetworkErr ? I18N.t('network_error') : (err.message || I18N.t('err_default'));
            const hint = isNetworkErr ? '<div class="err-hint">' + I18N.t('err_network_hint') + '</div>' : '';
            connectError.innerHTML = `
                <div class="err-title">${displayMsg}</div>
                <div class="err-url">${I18N.t('err_target', { url: authUrl })}</div>
                ${hint}
                <div class="err-actions">
                    <button type="button" id="err-retry">${I18N.t('err_retry')}</button>
                    <button type="button" id="err-copy">${I18N.t('err_copy')}</button>
                </div>`;
            connectError.classList.add('show');
            const retryBtn = document.getElementById('err-retry');
            if (retryBtn) retryBtn.addEventListener('click', () => {
                connectError.innerHTML = '';
                connectError.classList.remove('show');
                pinInput.focus();
            });
            const copyBtn = document.getElementById('err-copy');
            if (copyBtn) copyBtn.addEventListener('click', () => {
                // 诊断 dump 保持技术英文 (供反馈/排错), 不翻译
                const txt = `PhotosMove connect failed\nURL: ${authUrl}\nPIN: ${pin}\nError: ${err.message}\nUA: ${navigator.userAgent}\nTime: ${new Date().toISOString()}`;
                navigator.clipboard?.writeText(txt).then(() => {
                    copyBtn.textContent = I18N.t('err_copied');
                    setTimeout(() => copyBtn.textContent = I18N.t('err_copy'), 1500);
                }).catch(() => {});
            });
        } finally {
            submitBtn.disabled = false;
            submitBtn.textContent = I18N.t('connect_btn');
        }
    });

    // --- Dashboard ---
    async function showDashboard() {
        connectView.classList.add('hidden');
        dashboardView.classList.remove('hidden');
        appendLog(I18N.t('service_ready'));
        await loadAlbums();
        updateCard();
    }

    async function loadAlbums() {
        try {
            const res = await fetch(`${serverUrl}/api/albums`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            });
            if (!res.ok) throw new Error(I18N.t('fetch_albums_failed'));
            const data = await res.json();
            cachedAlbums = data.albums;
        } catch (err) {
            appendLog(I18N.t('load_failed', { msg: err.message }), 'error');
        }
    }

    function updateCard() {
        const thumbRow = document.getElementById('thumb-row');
        if (!cachedAlbums || cachedAlbums.length === 0) {
            cardTitle.textContent = I18N.t('card_title');
            cardDesc.textContent = I18N.t('no_photos');
            btnDownload.disabled = true;
            btnDownload.textContent = I18N.t('nothing_to_download');
            if (thumbRow) thumbRow.innerHTML = '';
            return;
        }

        const cameraAlbums = cachedAlbums.filter(a => a.category === 'camera');
        if (cameraAlbums.length === 0) {
            cardTitle.textContent = I18N.t('card_title');
            cardDesc.textContent = I18N.t('no_camera_photos');
            btnDownload.disabled = true;
            btnDownload.textContent = I18N.t('nothing_to_download');
            if (thumbRow) thumbRow.innerHTML = '';
            return;
        }

        const totalFiles = cameraAlbums.reduce((s, a) => s + a.file_count, 0);
        const totalSize = cameraAlbums.reduce((s, a) => s + a.total_size, 0);

        cardTitle.textContent = I18N.t('card_title');

        // Free/Pro unified per spec §5.2 — both include videos, so the card
        // always shows total file count + size and a single "下载全部" button.
        cardDesc.innerHTML = I18N.t('card_files_html', { count: totalFiles.toLocaleString(), size: formatSize(totalSize) });
        btnDownload.textContent = I18N.t('download_all', { size: formatSize(totalSize) });

        // verify.js 免费信任工具: verify-panel 仅在用户点 [校验] 提交 ZIP 后才展开.
        btnDownload.disabled = false;

        // 缩略图行: 遍历所有 camera 相册, 每个相册加载其内多张单图缩略图.
        if (thumbRow) {
            thumbRow.innerHTML = '';
            const MAX_THUMBS = 4; // 用户反馈: 6 张偏多, 4 张足够
            let placed = 0;
            for (const a of cameraAlbums) {
                if (placed >= MAX_THUMBS) break;
                const idx = cachedAlbums.indexOf(a);
                if (idx < 0) continue;
                const count = Math.min(a.thumb_count || 1, MAX_THUMBS - placed);
                for (let n = 0; n < count; n++) {
                    const img = document.createElement('img');
                    img.className = 'card-thumb';
                    img.src = `${serverUrl}/api/thumb/${idx}/${n}?token=${encodeURIComponent(authToken)}`;
                    img.alt = a.name || '';
                    img.loading = 'lazy';
                    img.onerror = () => { img.remove(); };
                    thumbRow.appendChild(img);
                    placed++;
                }
            }
        }
    }

    // --- Download ---
    let downloading = false;
    let dlGeneration = 0;
    let currentBatchId = null;
    let dlSpeedSamples = [];
    // 切语言时重渲染动态块用: 大文件块 / 当前相册标题 (二者无 data-i18n, 不补渲染残留旧语言).
    let activeBigFileBatch = null;
    let activeAlbumMeta = null;   // {key, params} — progressAlbum 当前文案 (albums_count/album_progress)
    // 进度区状态机快照: 切语言时 refreshProgressText 据此重渲 speed/eta/downloaded
    // (这些元素无 data-i18n, applyToDOM 跳过; 不快照则 success/interrupted 态切语言后文案冻结).
    let dlState = { mode: 'idle', overall: 0, total: 0, speed: 0, remaining: Infinity };

    btnDownload.addEventListener('click', () => {
        // 单按钮多状态: 传输中点击 = 取消, 否则 = 开始下载.
        if (downloading) { cancelDownload(); return; }
        if (!cachedAlbums) return;
        // HEIC 差评防护: Free 模式 HEIC 原样保留, Windows 默认打不开. 首次下载提示一次.
        if (!localStorage.getItem('photosmove_heic_warned')) {
            const msg = I18N.t('heic_warn');
            if (!confirm(msg)) return;
            localStorage.setItem('photosmove_heic_warned', '1');
        }
        const paths = cachedAlbums.filter(a => a.category === 'camera').map(a => a.path);
        if (paths.length > 0) startDownload(paths);
    });

    async function startDownload(albumPaths) {
        if (downloading) return;
        downloading = true;
        const myGen = ++dlGeneration;

        btnDownload.disabled = false;
        btnDownload.textContent = I18N.t('cancel_transfer');
        btnDownload.classList.add('downloading');
        progressSection.classList.remove('hidden');
        doneBadge.classList.add('hidden');
        cancelBadge.classList.add('hidden');
        progressBar.style.width = '0%';
        progressBar.classList.remove('done');
        progressPct.textContent = '0%';
        activeAlbumMeta = null;
        activeBigFileBatch = null;
        dlState = { mode: 'init', overall: 0, total: 0, speed: 0, remaining: Infinity };
        refreshProgressText();
        progressAlbum.textContent = '';

        appendLog(I18N.t('transfer_start', { count: albumPaths.length }), 'primary');

        try {
            const selectRes = await fetch(`${serverUrl}/api/select`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${authToken}`,
                },
                body: JSON.stringify({ paths: albumPaths }),
            });
            if (myGen !== dlGeneration) return;
            if (!selectRes.ok) {
                let errMsg = I18N.t('scan_failed_short');
                try { const d = await selectRes.json(); if (d.code) errMsg = I18N.te(d.code, { detail: d.detail }); else if (d.error) errMsg = d.error; } catch (e) {}
                throw new Error(errMsg);
            }

            const batchRes = await fetch(`${serverUrl}/api/batches`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            });
            if (myGen !== dlGeneration) return;
            if (!batchRes.ok) throw new Error(I18N.t('fetch_batches_failed'));
            const batches = await batchRes.json();

            if (batches.length === 0) {
                downloading = false;
                btnDownload.disabled = false;
                appendLog(I18N.t('no_files_to_download'), 'warning');
                updateCard();
                return;
            }

            const totalSize = batches.reduce((s, b) => s + b.total_size, 0);
            let totalDownloaded = 0;
            let downloadedBatches = 0;
            const t0 = Date.now();
            dlState = { mode: 'preparing', overall: 0, total: totalSize, speed: 0, remaining: Infinity };
            refreshProgressText();
            activeAlbumMeta = { key: 'albums_count', params: { count: batches.length } };
            progressAlbum.textContent = I18N.t('albums_count', { count: batches.length });

            for (let i = 0; i < batches.length; i++) {
                if (myGen !== dlGeneration) return;

                const batch = batches[i];
                appendLog(I18N.t('batch_progress', { i: i + 1, n: batches.length, name: batch.album_name, size: formatSize(batch.total_size) }));

                const ok = await downloadOneBatch(batch, i, batches.length, totalDownloaded, totalSize, t0);
                if (myGen !== dlGeneration) return;

                if (ok === true) {
                    totalDownloaded += batch.total_size;
                    downloadedBatches++;
                    if (i < batches.length - 1) {
                        await new Promise(r => setTimeout(r, 1500));
                    }
                } else {
                    break;
                }
            }

            if (myGen !== dlGeneration) return;

            if (totalDownloaded > 0) {
                const elapsed = ((Date.now() - t0) / 1000).toFixed(1);
                const avgSpeed = totalSize / parseFloat(elapsed);
                const filesInDone = batches.slice(0, downloadedBatches)
                    .reduce((s, b) => s + (b.file_count || 0), 0);
                appendLog(I18N.t('all_done', { files: filesInDone, size: formatSize(totalDownloaded), elapsed: elapsed, speed: formatSize(avgSpeed) }), 'success');
                finishDownload(true);
            } else {
                showCancelled();
            }
        } catch (err) {
            if (myGen !== dlGeneration) return;
            appendLog(I18N.t('download_failed', { msg: err.message }), 'error');
            btnDownload.classList.remove('downloading');
            finishDownload(false);
        }
    }

    function downloadOneBatch(batch, batchIdx, batchTotal, startBytes, totalSize, globalT0) {
        return new Promise((resolve) => {
            dlSpeedSamples = [];
            activeAlbumMeta = { key: 'album_progress', params: { i: batchIdx + 1, n: batchTotal, name: batch.album_name } };
            progressAlbum.textContent = I18N.t('album_progress', activeAlbumMeta.params);

            const isBigFileBatch = !!batch.big_file;
            if (isBigFileBatch) renderBigFileBatch(batch);

            const downloadUrl = `${serverUrl}/api/batch/${batch.id}?token=${encodeURIComponent(authToken)}`;
            const gen = dlGeneration;
            const batchStartTime = Date.now();

            // 轮询进度 (1s/次, 替代 SSE 长连接 — 避免 TCP send buffer 积压导致进度卡).
            currentBatchId = String(batch.id);
            const pollUrl = `${serverUrl}/api/progress-poll?token=${encodeURIComponent(authToken)}&batch=${encodeURIComponent(batch.id)}`;
            let completed = false;
            let lastMsg = Date.now();
            let lastSent = -1;
            let pollInterrupted = false;

            const applyProgress = (data) => {
                const sent = data.sent || 0;
                const total = (data.total && data.total > 0) ? data.total : totalSize;
                const overall = startBytes + sent;
                const pct = total > 0 ? Math.min(Math.round((overall / total) * 100), 100) : 0;
                const now = Date.now();
                const hasProgress = sent !== lastSent;
                if (hasProgress) {
                    lastSent = sent;
                    dlSpeedSamples.push({ t: now, bytes: overall });
                    while (dlSpeedSamples.length > 1 && now - dlSpeedSamples[0].t > 5000) dlSpeedSamples.shift();
                }
                let speed = 0;
                if (dlSpeedSamples.length >= 2) {
                    const first = dlSpeedSamples[0];
                    const dt = (now - first.t) / 1000;
                    if (dt > 0) speed = (overall - first.bytes) / dt;
                }
                const remaining = speed > 0 ? (total - overall) / speed : Infinity;
                progressBar.style.width = pct + '%';
                progressPct.textContent = pct + '%';
                dlState.overall = overall;
                dlState.total = total;
                dlState.speed = speed;
                dlState.remaining = remaining;
                dlState.mode = sent === 0 ? 'preparing' : (hasProgress ? 'active' : 'paused');
                refreshProgressText();
            };

            const pollOnce = async () => {
                if (completed) return;
                if (gen !== dlGeneration) { clearInterval(pollTimer); resolve(false); return; }
                try {
                    const res = await fetch(pollUrl, { headers: { 'Authorization': `Bearer ${authToken}` } });
                    if (!res.ok) return;
                    const data = await res.json();
                    lastMsg = Date.now();
                    if (pollInterrupted) {
                        pollInterrupted = false;
                        appendLog(I18N.t('poll_resumed', { name: batch.album_name }), 'success');
                    }
                    applyProgress(data);
                    if (data.cancelled) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        appendLog(I18N.t('batch_cancelled', { name: batch.album_name }), 'warning');
                        resolve(false);
                        return;
                    }
                    if (data.done) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        const batchElapsed = ((Date.now() - batchStartTime) / 1000).toFixed(1);
                        appendLog(I18N.t('batch_done', { name: batch.album_name, size: formatSize(batch.total_size), elapsed: batchElapsed }), 'success');
                        resolve(true);
                        return;
                    }
                } catch (e) {
                    console.warn('[poll] fetch failed:', e.message);
                }
            };

            const pollTimer = setInterval(pollOnce, 1000);
            const watchdog = setInterval(() => {
                if (completed) { clearInterval(watchdog); return; }
                if (gen !== dlGeneration) { clearInterval(watchdog); clearInterval(pollTimer); resolve(false); return; }
                if (Date.now() - lastMsg > 30000 && !pollInterrupted) {
                    pollInterrupted = true;
                    appendLog(I18N.t('poll_timeout', { name: batch.album_name }), 'warning');
                    appendLog(I18N.t('poll_check_downloader'), 'info');
                    dlState.mode = 'interrupted';
                    refreshProgressText();
                }
            }, 5000);

            (async () => {
                if (isBigFileBatch) {
                    const ok = await confirmBigFileDownload(batch);
                    if (gen !== dlGeneration) return;
                    if (!ok) {
                        clearInterval(pollTimer);
                        clearInterval(watchdog);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        appendLog(I18N.t('batch_bigfile_cancelled', { name: batch.album_name }), 'warning');
                        resolve(false);
                        return;
                    }
                }

                const a = document.createElement('a');
                a.href = downloadUrl;
                a.download = batch.album_name.replace(/[/\\:*?"<>|]/g, '_') + '.zip';
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
            })();
        });
    }

    function cancelDownload() {
        dlGeneration++;
        downloading = false;
        clearBigFileBatch();

        const bid = currentBatchId;
        currentBatchId = null;
        if (bid) {
            const cancelUrl = `${serverUrl}/api/cancel`;
            const cancelBody = JSON.stringify({ batch_id: bid });
            const cancelHeaders = {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${authToken}`,
            };
            fetch(cancelUrl, { method: 'POST', headers: cancelHeaders, body: cancelBody })
                .catch(err => {
                    console.warn('cancel fetch failed, trying sendBeacon:', err);
                    const blob = new Blob([cancelBody], { type: 'application/json' });
                    const sent = navigator.sendBeacon(
                        cancelUrl + '?token=' + encodeURIComponent(authToken),
                        blob
                    );
                    if (!sent) {
                        appendLog(I18N.t('cancel_not_sent'), 'warning');
                    }
                });
        }

        progressSection.classList.add('hidden');
        doneBadge.classList.add('hidden');
        showCancelled();
        appendLog(I18N.t('cancelled_notice'), 'info');
        btnDownload.disabled = true;
        setTimeout(() => { btnDownload.disabled = false; }, 1500);
    }

    const cancelBadge = document.getElementById('cancel-badge');

    function finishDownload(success) {
        downloading = false;
        cancelBadge.classList.add('hidden');
        btnDownload.classList.remove('downloading');

        if (success) {
            progressBar.style.width = '100%';
            progressBar.classList.add('done');
            progressPct.textContent = '100%';
            dlState.mode = 'complete';
            refreshProgressText(); // speed=完成, eta 空, downloaded 保留当前 overall/total
            doneBadge.textContent = I18N.t('done_badge');
            doneBadge.classList.remove('hidden');
            btnDownload.textContent = I18N.t('redownload');
            btnDownload.disabled = false;
        } else {
            doneBadge.classList.add('hidden');
            cancelBadge.classList.add('hidden');
            progressSection.classList.add('hidden');
            btnDownload.classList.remove('downloading');
            btnDownload.disabled = false;
            dlState.mode = 'idle';
            updateCard();
        }
    }

    function showCancelled() {
        downloading = false;
        dlState.mode = 'idle';
        doneBadge.classList.add('hidden');
        cancelBadge.classList.remove('hidden');
        progressSection.classList.add('hidden');
        btnDownload.classList.remove('downloading');
        btnDownload.disabled = false;
        updateCard();
    }

    // --- Console ---
    consoleToggle.addEventListener('click', () => {
        consoleBox.classList.toggle('show');
        consoleToggle.classList.toggle('open');
    });

    function appendLog(text, type) {
        type = type || 'info';
        const container = document.getElementById('live-logs');
        const p = document.createElement('p');
        const ts = new Date().toLocaleTimeString(I18N.getLocale() === 'zh' ? 'zh-CN' : 'en-US', { hour12: false });
        const classMap = {
            info: 'log-info',
            success: 'log-success',
            warning: 'log-warning',
            error: 'log-error',
            primary: 'log-primary',
        };
        p.className = classMap[type] || 'log-info';
        p.textContent = ts + ' ' + text;
        container.appendChild(p);
        while (container.children.length > 500) {
            container.removeChild(container.firstChild);
        }
        consoleBox.scrollTop = consoleBox.scrollHeight;
        return p;
    }

    // --- Helpers ---
    function formatSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
        return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
    }

    function formatGB(bytes) {
        return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
    }

    function formatMinutes(seconds) {
        if (!seconds || seconds <= 0) return '--';
        return Math.max(1, Math.round(seconds / 60)) + 'm';
    }

    // --- Big-file batch highlight ---
    const BIG_FILE_CONFIRM_KEY = 'photosmove_big_file_confirmed';

    function getBigFileConfirmed() {
        try { return localStorage.getItem(BIG_FILE_CONFIRM_KEY) === '1'; } catch (e) { return false; }
    }

    function setBigFileConfirmed() {
        try { localStorage.setItem(BIG_FILE_CONFIRM_KEY, '1'); } catch (e) {}
    }

    function ensureBigFileBlock() {
        let block = document.getElementById('big-file-info-block');
        if (!block) {
            block = document.createElement('div');
            block.id = 'big-file-info-block';
            cardDesc.parentNode.insertBefore(block, cardDesc.nextSibling);
        }
        return block;
    }

    function renderBigFileBatch(batch) {
        activeBigFileBatch = batch;
        const block = ensureBigFileBlock();
        const cardEl = document.querySelector('.main-card');
        if (cardEl) cardEl.classList.add('big-file');

        const sizeStr = formatGB(batch.total_size);
        const name = (batch.biggest_file && batch.biggest_file.name) || batch.album_name;
        const wifiMin = formatMinutes(batch.estimated_wifi_seconds);
        const usbMin = formatMinutes(batch.estimated_usb_seconds);

        block.innerHTML =
            '<div style="margin-top:10px;text-align:left">' +
              '<span style="color:#f59e0b;font-size:14px">⚠️</span>' +
              '<span class="big-file-chip">' + I18N.t('bigfile_chip') + '</span>' +
              '<div class="estimated-time">' + I18N.t('bigfile_batch_size', { size: sizeStr, name: escapeHtml(name) }) + '</div>' +
              '<div class="estimated-time">' + I18N.t('bigfile_estimate', { wifi: wifiMin, usb: usbMin }) + '</div>' +
              '<div class="big-file-warning">' + I18N.t('bigfile_warning') + '</div>' +
            '</div>';
    }

    function clearBigFileBatch() {
        activeBigFileBatch = null;
        const block = document.getElementById('big-file-info-block');
        if (block) block.innerHTML = '';
        const cardEl = document.querySelector('.main-card');
        if (cardEl) cardEl.classList.remove('big-file');
    }

    function escapeHtml(s) {
        return String(s).replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        }[c]));
    }

    function confirmBigFileDownload(batch) {
        return new Promise((resolve) => {
            if (getBigFileConfirmed()) { resolve(true); return; }

            const sizeStr = formatGB(batch.total_size);
            const wifiMin = formatMinutes(batch.estimated_wifi_seconds);
            const usbMin = formatMinutes(batch.estimated_usb_seconds);

            const mask = document.createElement('div');
            mask.className = 'big-file-modal-mask';
            mask.innerHTML =
                '<div class="big-file-modal">' +
                  '<h3>' + I18N.t('bigfile_modal_title') + '</h3>' +
                  '<div class="big-file-modal-row"><span class="label">' + I18N.t('bigfile_modal_size') + '</span><span>' + escapeHtml(sizeStr) + '</span></div>' +
                  '<div class="big-file-modal-row"><span class="label">' + I18N.t('bigfile_modal_estimate') + '</span><span>WiFi ' + escapeHtml(wifiMin) + ' / USB ' + escapeHtml(usbMin) + '</span></div>' +
                  '<div class="big-file-modal-warn">' + I18N.t('bigfile_modal_warn') + '</div>' +
                  '<div class="big-file-modal-actions">' +
                    '<button class="btn-cancel-modal">' + I18N.t('bigfile_modal_cancel') + '</button>' +
                    '<button class="btn-start-modal">' + I18N.t('bigfile_modal_start') + '</button>' +
                  '</div>' +
                '</div>';
            document.body.appendChild(mask);

            const close = (ok) => {
                document.body.removeChild(mask);
                resolve(ok);
            };
            mask.querySelector('.btn-cancel-modal').addEventListener('click', () => close(false));
            mask.querySelector('.btn-start-modal').addEventListener('click', () => {
                setBigFileConfirmed();
                close(true);
            });
        });
    }

    function formatTime(seconds) {
        if (!isFinite(seconds) || seconds < 0) return I18N.t('time_calculating');
        const totalSec = Math.ceil(seconds);
        if (totalSec < 60) return I18N.t('time_seconds', { n: totalSec });
        const m = Math.floor(totalSec / 60);
        const s = totalSec % 60;
        if (m < 60) return I18N.t('time_minutes', { m: m, s: s });
        const h = Math.floor(m / 60);
        const rm = m % 60;
        return I18N.t('time_hours', { h: h, m: rm });
    }

    // --- Auto-restore ---
    (async function tryRestore() {
        const saved = loadToken();
        if (!saved) return;
        authToken = saved;
        try {
            const res = await fetch(`${serverUrl}/api/albums`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            });
            if (res.ok) {
                const data = await res.json();
                cachedAlbums = data.albums;
                showDashboard();
            } else {
                clearToken();
            }
        } catch (e) {
            clearToken();
        }
    })();

    // 全局错误兜底: 任何未捕获异常都显示在 #connect-error 上, 避免"白屏无错误".
    function showFatalError(source, err) {
        const msg = err && err.message ? err.message : String(err);
        const stack = err && err.stack ? err.stack : '';
        const target = document.getElementById('connect-error') || document.body;
        const box = document.createElement('div');
        box.style.cssText = 'background:#fef2f2;border:1px solid #fecaca;border-radius:12px;padding:16px 18px;margin:18px;text-align:left;color:#991b1b;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:13px;line-height:1.6';
        box.innerHTML =
            '<div style="font-weight:700;font-size:15px;margin-bottom:8px">' + I18N.t('fatal_title', { source: source }) + '</div>' +
            '<div style="margin-bottom:6px">' + I18N.t('fatal_error', { msg: msg.replace(/</g, '&lt;') }) + '</div>' +
            '<pre style="white-space:pre-wrap;word-break:break-all;font-size:11px;max-height:240px;overflow:auto;background:#fff;padding:8px;border-radius:6px;border:1px solid #fee2e2">' + stack.replace(/</g, '&lt;') + '</pre>' +
            '<div style="margin-top:8px;color:#6b7280;font-size:11px">UA: ' + navigator.userAgent + '</div>' +
            '<div style="margin-top:6px;color:#6b7280;font-size:11px">URL: ' + location.href + '</div>' +
            '<button id="fatal-reload" style="margin-top:10px;padding:8px 16px;background:#7c3aed;color:white;border:none;border-radius:8px;cursor:pointer">' + I18N.t('fatal_reload') + '</button>';
        target.appendChild(box);
        const btn = box.querySelector('#fatal-reload');
        if (btn) btn.addEventListener('click', () => location.reload());
    }
    window.addEventListener('error', (e) => {
        showFatalError('window.error', e.error || e.message);
    });
    window.addEventListener('unhandledrejection', (e) => {
        showFatalError('Promise', e.reason);
    });
})();
