// PhotosMove browser client — camera download
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

    // --- Connect ---
    connectForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        connectError.innerHTML = '';
        connectError.classList.remove('show');
        const pin = pinInput.value.trim();
        const submitBtn = connectForm.querySelector('button[type="submit"]');
        submitBtn.disabled = true;
        submitBtn.textContent = '连接中...';

        const authUrl = `${serverUrl}/api/auth`;
        try {
            const res = await fetch(authUrl, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ pin }),
            });
            if (!res.ok) {
                let errMsg = `服务器返回 ${res.status}`;
                try { const d = await res.json(); if (d.error) errMsg = d.error; } catch (e) {}
                throw new Error(errMsg);
            }
            const data = await res.json();
            saveToken(data.token);
            showDashboard();
        } catch (err) {
            // fetch TypeError (网络层失败) 显示完整诊断，避免只显示 "Failed to fetch"
            const isNetworkErr = err instanceof TypeError;
            const hint = isNetworkErr
                ? `<div class="err-hint">⚠ 网络层无法到达手机服务。<br>常见原因:<br>· 浏览器/系统代理 (含 VPN/Clash/V2Ray) 劫持了局域网请求 → 关闭代理或加入直连白名单<br>· 电脑和手机不在同一 WiFi / 公司或学校 WiFi 开启了客户端隔离<br>· PhotosMove APP 被系统冻结或已退出</div>`
                : '';
            connectError.innerHTML = `
                <div class="err-title">${err.message || '连接失败'}</div>
                <div class="err-url">目标: ${authUrl}</div>
                ${hint}
                <div class="err-actions">
                    <button type="button" id="err-retry">再试一次</button>
                    <button type="button" id="err-copy">复制诊断信息</button>
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
                const txt = `PhotosMove 连接失败\nURL: ${authUrl}\nPIN: ${pin}\n错误: ${err.message}\nUA: ${navigator.userAgent}\n时间: ${new Date().toISOString()}`;
                navigator.clipboard?.writeText(txt).then(() => {
                    copyBtn.textContent = '✓ 已复制';
                    setTimeout(() => copyBtn.textContent = '复制诊断信息', 1500);
                }).catch(() => {});
            });
        } finally {
            submitBtn.disabled = false;
            submitBtn.textContent = '连接手机';
        }
    });

    // --- Dashboard ---
    async function showDashboard() {
        connectView.classList.add('hidden');
        dashboardView.classList.remove('hidden');
        appendLog('服务已就绪');
        await loadAlbums();
        updateCard();
    }

    async function loadAlbums() {
        try {
            const res = await fetch(`${serverUrl}/api/albums`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            });
            if (!res.ok) throw new Error('获取相册失败');
            const data = await res.json();
            cachedAlbums = data.albums;
        } catch (err) {
            appendLog('加载失败: ' + err.message, 'error');
        }
    }

    function updateCard() {
        const thumbRow = document.getElementById('thumb-row');
        if (!cachedAlbums || cachedAlbums.length === 0) {
            cardTitle.textContent = '相机拍摄';
            cardDesc.textContent = '未发现照片';
            btnDownload.disabled = true;
            btnDownload.textContent = '没有可下载的内容';
            if (thumbRow) thumbRow.innerHTML = '';
            return;
        }

        const cameraAlbums = cachedAlbums.filter(a => a.category === 'camera');
        if (cameraAlbums.length === 0) {
            cardTitle.textContent = '相机拍摄';
            cardDesc.textContent = '未发现相机照片';
            btnDownload.disabled = true;
            btnDownload.textContent = '没有可下载的内容';
            if (thumbRow) thumbRow.innerHTML = '';
            return;
        }

        const totalFiles = cameraAlbums.reduce((s, a) => s + a.file_count, 0);
        const totalSize = cameraAlbums.reduce((s, a) => s + a.total_size, 0);
        const totalVideos = cameraAlbums.reduce((s, a) => s + (a.video_count || 0), 0);
        const totalVideoSize = cameraAlbums.reduce((s, a) => s + (a.video_size || 0), 0);

        cardTitle.textContent = '相机拍摄';

        // Free/Pro unified per spec §5.2 — both include videos, so the card
        // always shows total file count + size and a single "下载全部" button.
        // The legacy "Free excludes videos → 下载全部图片" branch was a Plan A
        // leftover that contradicted the spec.
        cardDesc.innerHTML = `共 <strong>${totalFiles.toLocaleString()}</strong> 个文件 · <strong>${formatSize(totalSize)}</strong>`;
        btnDownload.textContent = `下载全部 (${formatSize(totalSize)})`;

        // verify.js 免费信任工具 (single-zip-trust-tcp Phase 2):
        // verify-panel 仅在用户点 [完整性校验] 提交 ZIP 后才展开,
        // 不在 dashboard 加载时主动 show() (否则会显示空白的白底面板).
        // btn-verify 的 click handler 内部自己 panel.classList.remove('hidden').

        btnDownload.disabled = false;

        // 缩略图行: 遍历所有 camera 相册, 每个相册加载其内多张单图缩略图
        // (/api/thumb/{idx}/{n}), 按容器宽度自适应排列 (grid auto-fill) 填满.
        // 不再用单张 composite (camera 常只有 1 个大相册, composite 只 1 张填
        // 不满 grid). thumb_count 由服务端 Album.ThumbCount 暴露.
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

    btnDownload.addEventListener('click', () => {
        // 单按钮多状态: 传输中点击 = 取消, 否则 = 开始下载.
        if (downloading) { cancelDownload(); return; }
        if (!cachedAlbums) return;
        // HEIC 差评防护 (4.9): Free 模式 HEIC 原样保留 (不转换), Windows 默认打不开.
        // 首次下载提示一次, localStorage 记忆"不再提示", 覆盖所有用户.
        if (!localStorage.getItem('photosmove_heic_warned')) {
            const msg = '提示：HEIC 照片将原样保留（不转换）。Windows 默认无法打开 .heic，需安装"HEIF 图像扩展"或用在线工具转换。\n\n是否继续下载？';
            if (!confirm(msg)) return;
            localStorage.setItem('photosmove_heic_warned', '1');
        }
        const paths = cachedAlbums.filter(a => a.category === 'camera').map(a => a.path);
        if (paths.length > 0) startDownload(paths);
    });

    // btnCancel 已合并到 btnDownload (单按钮多状态): 传输中点击 btnDownload 触发 cancelDownload.
    // Failed files panel removed (single-zip-trust-tcp Phase 2):
    // 信任 TCP 完整性, 不再有"失败文件"概念, 无需补传 UI.

    async function startDownload(albumPaths) {
        if (downloading) return;
        downloading = true;
        const myGen = ++dlGeneration;

        // UI: downloading state — 同一按钮显示"取消传输", 点击则取消.
        btnDownload.disabled = false;
        btnDownload.textContent = '取消传输';
        btnDownload.classList.add('downloading');
        progressSection.classList.remove('hidden');
        doneBadge.classList.add('hidden');
        cancelBadge.classList.add('hidden');
        progressBar.style.width = '0%';
        progressBar.classList.remove('done');
        progressPct.textContent = '0%';
        progressSpeed.textContent = '--';
        progressEta.textContent = '--';
        progressDownloaded.textContent = '已传 0 B / 0 B';
        progressAlbum.textContent = '';

        appendLog(`传输启动 · ${albumPaths.length} 个相册`, 'primary');

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
                let errMsg = '扫描失败';
                try { const d = await selectRes.json(); if (d.error) errMsg = d.error; } catch (e) {}
                throw new Error(errMsg);
            }

            const batchRes = await fetch(`${serverUrl}/api/batches`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            });
            if (myGen !== dlGeneration) return;
            if (!batchRes.ok) throw new Error('获取批次失败');
            const batches = await batchRes.json();

            if (batches.length === 0) {
                downloading = false;
                btnDownload.disabled = false;
                appendLog('没有需要下载的文件', 'warning');
                updateCard();
                return;
            }

            const totalSize = batches.reduce((s, b) => s + b.total_size, 0);
            const totalLive = batches.reduce((s, b) => s + (b.live_count || 0), 0);
            const totalFiles = batches.reduce((s, b) => s + (b.file_count || 0), 0);
            let totalDownloaded = 0;
            let downloadedBatches = 0;
            const t0 = Date.now();
            progressDownloaded.textContent = `已传 0 B / ${formatSize(totalSize)}`;
            progressAlbum.textContent = `共 ${batches.length} 个相册`;

            for (let i = 0; i < batches.length; i++) {
                if (myGen !== dlGeneration) return;

                const batch = batches[i];
                appendLog(`批次 ${i + 1}/${batches.length}: ${batch.album_name} (${formatSize(batch.total_size)})`);

                const ok = await downloadOneBatch(batch, i, batches.length, totalDownloaded, totalSize, t0);
                if (myGen !== dlGeneration) return;

                if (ok === true) {
                    totalDownloaded += batch.total_size;
                    downloadedBatches++;
                    if (i < batches.length - 1) {
                        await new Promise(r => setTimeout(r, 1500));
                    }
                } else if (ok === 'disconnected') {
                    downloading = false;
                    doneBadge.classList.add('hidden');
                    cancelBadge.classList.add('hidden');
                    progressSection.classList.add('hidden');
                    btnDownload.disabled = false;
                    btnDownload.textContent = '重新下载';
                    return;
                } else {
                    break;
                }
            }

            if (myGen !== dlGeneration) return;

            if (totalDownloaded > 0) {
                const elapsed = ((Date.now() - t0) / 1000).toFixed(1);
                const avgSpeed = totalSize / parseFloat(elapsed);
                // spec download-summary: include file count alongside size/time/speed.
                // "跳过 N 张" omitted (only meaningful for Pro incremental sync,
                // which has its own appendLog path above; full-download never skips).
                const filesInDone = batches.slice(0, downloadedBatches)
                    .reduce((s, b) => s + (b.file_count || 0), 0);
                appendLog(`✓ 全部完成 · ${filesInDone} 个文件 · ${formatSize(totalDownloaded)} · ${elapsed}s · ${formatSize(avgSpeed)}/s`, 'success');
                finishDownload(true);
            } else {
                showCancelled();
            }
        } catch (err) {
            if (myGen !== dlGeneration) return;
            appendLog('下载失败: ' + err.message, 'error');
            // 显式清理 downloading class (finishDownload(false) 也清, 双重保险),
            // 避免 catch 路径 UI 残留红色按钮.
            btnDownload.classList.remove('downloading');
            finishDownload(false);
        }
    }

    function downloadOneBatch(batch, batchIdx, batchTotal, startBytes, totalSize, globalT0) {
        return new Promise((resolve) => {
            dlSpeedSamples = [];
            progressAlbum.textContent = `相册 ${batchIdx + 1}/${batchTotal}: ${batch.album_name}`;

            // T-ui-5: big-file batches get a visual highlight + a one-time
            // confirm modal before the <a>.click() fires. Small-file
            // batches skip this entirely.
            const isBigFileBatch = !!batch.big_file;
            if (isBigFileBatch) renderBigFileBatch(batch);

            // Free 模式: 原样打包 (HEIC 原格式 / 视频字节级 / 保留目录结构),
            // 无 convert_heic / strip_exif / live_photo_mode 参数.
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
                progressDownloaded.textContent = `已传 ${formatSize(overall)} / ${formatSize(total)}`;
                if (sent === 0) {
                    progressSpeed.textContent = '准备中...'; progressEta.textContent = '--';
                } else if (hasProgress) {
                    progressSpeed.textContent = formatSize(speed) + '/s';
                    progressEta.textContent = '剩余 ' + formatTime(remaining);
                } else {
                    progressSpeed.textContent = '已暂停'; progressEta.textContent = '--';
                }
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
                        appendLog(`✓ ${batch.album_name} 轮询恢复, 进度继续`, 'success');
                    }
                    applyProgress(data);
                    if (data.cancelled) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        appendLog(`${batch.album_name} 下载已取消`, 'warning');
                        resolve(false);
                        return;
                    }
                    if (data.done) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        const batchElapsed = ((Date.now() - batchStartTime) / 1000).toFixed(1);
                        appendLog(`✓ ${batch.album_name} · ${formatSize(batch.total_size)} · ${batchElapsed}s`, 'success');
                        resolve(true);
                        return;
                    }
                } catch (e) {
                    console.warn('[轮询] fetch 失败:', e.message);
                }
            };

            const pollTimer = setInterval(pollOnce, 1000);
            // 看门狗: 30s 无成功响应 → 中断提示 (不假定完成)
            const watchdog = setInterval(() => {
                if (completed) { clearInterval(watchdog); return; }
                if (gen !== dlGeneration) { clearInterval(watchdog); clearInterval(pollTimer); resolve(false); return; }
                if (Date.now() - lastMsg > 30000 && !pollInterrupted) {
                    pollInterrupted = true;
                    appendLog(`⚠ ${batch.album_name} 轮询 30s 无响应, 浏览器下载可能仍在进行`, 'warning');
                    appendLog('👉 查看浏览器下载管理器确认实际进度', 'info');
                    progressSpeed.textContent = '轮询中断';
                    progressEta.textContent = '见下载器';
                }
            }, 5000);

            // Trigger native download. For big-file batches, gate the
            // <a>.click() behind a one-time confirm modal (铁律 1: native
            // download only, no fetch+Blob).
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
                        appendLog(`${batch.album_name} 下载已取消 (大文件确认)`, 'warning');
                        resolve(false);
                        return;
                    }
                }

                const a = document.createElement('a');
                a.href = downloadUrl;
                // spec batch-zip-pipeline Requirement "Free 模式切批行为":
                // Free 和 Pro 的 a.download 文件名 MUST 一致 (批次命名, 不都是 photos.zip).
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

        // P0-2 race fix: 先快照 bid, 避免 done 分支并发清空
        // currentBatchId 导致 cancel fetch 漏发, 服务端继续推 ZIP.
        const bid = currentBatchId;
        currentBatchId = null;
        if (bid) {
            const cancelUrl = `${serverUrl}/api/cancel`;
            const cancelBody = JSON.stringify({ batch_id: bid });
            const cancelHeaders = {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${authToken}`,
            };
            // 主通道: fetch
            fetch(cancelUrl, { method: 'POST', headers: cancelHeaders, body: cancelBody })
                .catch(err => {
                    console.warn('cancel fetch failed, trying sendBeacon:', err);
                    // 备选通道: sendBeacon (不受页面卸载/浏览器网络限制影响)
                    const blob = new Blob([cancelBody], { type: 'application/json' });
                    const sent = navigator.sendBeacon(
                        cancelUrl + '?token=' + encodeURIComponent(authToken),
                        blob
                    );
                    if (!sent) {
                        appendLog('取消请求可能未送达, 请检查手机端状态', 'warning');
                    }
                });
        }

        progressSection.classList.add('hidden');
        doneBadge.classList.add('hidden');
        showCancelled();
        // a.click() 触发的浏览器原生下载无法被 JS 立即中止, 服务端 cancel 后
        // HTTP 响应中断, 浏览器会在几秒内停止接收. 提示用户避免困惑.
        appendLog('已取消, 浏览器下载管理器可能仍显示几秒进度后停止', 'info');
        // UX 节流: 服务端 handleBatch 退出 + activeDownloads-- 有延迟 (archiver
        // ctx.Done → writeBatchZip 返回 → defer). 这期间用户立即点新下载会被
        // /api/select 拒绝 (409 "下载进行中"). 禁用按钮 1.5s 避免误触.
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
            progressSpeed.textContent = '✓ 完成';
            progressEta.textContent = '';
            progressDownloaded.textContent = progressDownloaded.textContent.split(' / ')[0] + ' / ' + progressDownloaded.textContent.split(' / ')[1];
            doneBadge.textContent = '✓ 传输完成';
            doneBadge.classList.remove('hidden');
            btnDownload.textContent = '重新下载';
            btnDownload.disabled = false;
        } else {
            // 失败/中断: 必须完整重置 UI 状态. 之前只调 updateCard() 导致
            // progressSection 卡在显示、btnDownload.downloading class 残留、
            // 按钮文字虽然重置但视觉状态错乱 (前端 QA 在连续 start→cancel
            // + /api/select 返回"下载进行中"场景下复现).
            doneBadge.classList.add('hidden');
            cancelBadge.classList.add('hidden');
            progressSection.classList.add('hidden');
            btnDownload.classList.remove('downloading');
            btnDownload.disabled = false;
            updateCard();
        }
    }

    function showCancelled() {
        downloading = false;
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
        const ts = new Date().toLocaleTimeString('zh-CN', { hour12: false });
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

    // --- Big-file batch highlight (T-ui-5) ---
    // Renders an inline highlight block inside the main card while a
    // big_file batch is being downloaded, plus a one-time confirm modal
    // before the first such download. Field shape (server T-big-3.3):
    //   batch.big_file: true
    //   batch.biggest_file: { name, size }
    //   batch.estimated_wifi_seconds, batch.estimated_usb_seconds
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
              '<span class="big-file-chip">★ 大文件</span>' +
              '<div class="estimated-time">批次大小: ' + sizeStr + ' · ' + escapeHtml(name) + ' (单文件, 不可拆分)</div>' +
              '<div class="estimated-time">预估传输: WiFi ~ ' + wifiMin + ' / USB ~ ' + usbMin + '</div>' +
              '<div class="big-file-warning">⚠️ 中断需重传整批, 建议保持手机唤醒</div>' +
            '</div>';
    }

    function clearBigFileBatch() {
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
                  '<h3>⚠️ 大文件下载确认</h3>' +
                  '<div class="big-file-modal-row"><span class="label">批次大小</span><span>' + escapeHtml(sizeStr) + '</span></div>' +
                  '<div class="big-file-modal-row"><span class="label">预估传输</span><span>WiFi ' + escapeHtml(wifiMin) + ' / USB ' + escapeHtml(usbMin) + '</span></div>' +
                  '<div class="big-file-modal-warn">⚠️ 中断需重传整批, 建议保持手机唤醒.<br>' +
                    '浏览器"恢复下载"按钮无效, 中断请重新点 [下载].</div>' +
                  '<div class="big-file-modal-actions">' +
                    '<button class="btn-cancel-modal">取消</button>' +
                    '<button class="btn-start-modal">开始下载</button>' +
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
        if (!isFinite(seconds) || seconds < 0) return '计算中...';
        const totalSec = Math.ceil(seconds);
        if (totalSec < 60) return totalSec + '秒';
        const m = Math.floor(totalSec / 60);
        const s = totalSec % 60;
        if (m < 60) return m + '分' + s + '秒';
        const h = Math.floor(m / 60);
        const rm = m % 60;
        return h + '时' + rm + '分';
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

    // 全局错误兜底 (single-zip-trust-tcp 调试): 任何未捕获异常都显示在
    // #connect-error 上, 避免"白屏无错误"无法诊断.
    function showFatalError(source, err) {
        const msg = err && err.message ? err.message : String(err);
        const stack = err && err.stack ? err.stack : '';
        const target = document.getElementById('connect-error') || document.body;
        const box = document.createElement('div');
        box.style.cssText = 'background:#fef2f2;border:1px solid #fecaca;border-radius:12px;padding:16px 18px;margin:18px;text-align:left;color:#991b1b;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:13px;line-height:1.6';
        box.innerHTML = `
            <div style="font-weight:700;font-size:15px;margin-bottom:8px">⚠ PhotosMove 前端崩溃 (${source})</div>
            <div style="margin-bottom:6px">错误: ${msg.replace(/</g, '&lt;')}</div>
            <pre style="white-space:pre-wrap;word-break:break-all;font-size:11px;max-height:240px;overflow:auto;background:#fff;padding:8px;border-radius:6px;border:1px solid #fee2e2">${stack.replace(/</g, '&lt;')}</pre>
            <div style="margin-top:8px;color:#6b7280;font-size:11px">UA: ${navigator.userAgent}</div>
            <div style="margin-top:6px;color:#6b7280;font-size:11px">URL: ${location.href}</div>
            <button id="fatal-reload" style="margin-top:10px;padding:8px 16px;background:#7c3aed;color:white;border:none;border-radius:8px;cursor:pointer">刷新页面</button>
        `;
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
