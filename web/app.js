// PhotosMove browser client — camera download (i18n: all copy via I18N.t, group 3.3-3.5/5.4/4.3)
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
    // Auto-focus the PIN input when entering the PIN page (saves the user a tap)
    if (connectView && !connectView.classList.contains('hidden')) { setTimeout(() => pinInput.focus(), 50); }
    const connectError = document.getElementById('connect-error');
    const selectionCount = document.getElementById('selection-count');
    const albumGrid = document.getElementById('album-grid');
    const albumSearch = document.getElementById('album-search');
    const clearSearch = document.getElementById('clear-search');
    const albumEmpty = document.getElementById('album-empty');
    const emptyClear = document.getElementById('empty-clear');
    const selectAllBtn = document.getElementById('select-all');
    const clearAllBtn = document.getElementById('clear-all');
    const footerCount = document.getElementById('footer-count');
    const progressSection = document.getElementById('progress-section');
    const progressBar = document.getElementById('progress-bar');
    const progressPct = document.getElementById('progress-pct');
    const progressSpeed = document.getElementById('progress-speed');
    const progressEta = document.getElementById('progress-eta');
    const progressDownloaded = document.getElementById('progress-downloaded');
    const progressAlbum = document.getElementById('progress-album');
    const doneBadge = document.getElementById('done-badge');
    const doneVerify = document.getElementById('done-verify');
    const verifiedBadge = document.getElementById('verified-badge');
    const btnDownload = document.getElementById('btn-download');
    const btnVerifyDone = document.getElementById('btn-verify-done');
    const btnVerify = document.getElementById('btn-verify');
    const consoleToggle = document.getElementById('console-toggle');
    const consoleBox = document.getElementById('console-box');

    if (!albumGrid || !albumSearch || !selectionCount) {
        document.body.innerHTML = '<div style="padding:40px;font-family:system-ui,sans-serif;text-align:center;color:#991b1b">页面版本过旧，请刷新页面重新加载。<br><br>Page is outdated. Please reload.</div>';
        return;
    }

    // --- i18n init (group 3.5/5.4): apply static copy + bind the language switch entry ---
    // Guard: if i18n.js fails to load, I18N is undefined — app.js must not crash at the top
    // of the IIFE, or the trailing window.error fallback (showFatalError) would also crash on
    // I18N.t → white screen with no error. Fall back to a native prompt that doesn't depend
    // on I18N, so the user at least gets one readable line.
    if (typeof I18N === 'undefined') {
        document.body.innerHTML = '<div style="padding:40px;font-family:system-ui,sans-serif;color:#991b1b;text-align:center">PhotosMove failed to load (i18n). Please reload the page.</div>';
        return;
    }
    I18N.applyToDOM();
    refreshLangLabels();
    // Dynamic selection/progress copy is managed by app.js. The initial placeholders are
    // set here once, then updateCard / applyProgress take over.
    albumSearch.placeholder = I18N.t('search_albums');
    clearSearch.setAttribute('aria-label', I18N.t('clear_search'));
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

    // Re-render dynamic copy after a locale change. Strictly preserve the downloading
    // state (design Risks ④⑤: do not restart the download / do not clear progress).
    // applyToDOM only refreshes static elements with data-i18n; dynamically managed
    // elements (card-desc/progressAlbum/progressSpeed/progressEta/progressDownloaded/
    // big-file block) have no data-i18n and must be re-rendered uniformly from the current
    // state machine, or stale language remains — especially the success/interrupted states,
    // which get no poll refresh and would freeze permanently (review finding).
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
        albumSearch.placeholder = I18N.t('search_albums');
        clearSearch.setAttribute('aria-label', I18N.t('clear_search'));
        updateSelectionUI();

        // Dynamic elements in the progress section (no data-i18n): re-render only while
        // the progress section is visible (covers downloading/success/interrupted).
        if (!progressSection.classList.contains('hidden')) {
            if (activeAlbumMeta) {
                progressAlbum.textContent = activeAlbumMeta.pluralCount != null
                    ? I18N.tn(activeAlbumMeta.key, activeAlbumMeta.pluralCount, activeAlbumMeta.params)
                    : I18N.t(activeAlbumMeta.key, activeAlbumMeta.params);
            }
            refreshProgressText();
        }

        // Big-file block (no data-i18n, may stay on screen for tens of minutes).
        if (activeBigFileBatch) renderBigFileBatch(activeBigFileBatch);

        // verify.js dynamic elements (no data-i18n, separate IIFE): call when the
        // rerender hook is exposed.
        if (dlState.mode === 'complete') {
            doneBadge.textContent = I18N.tn('done_albums_badge', selectedAlbumCount, { count: selectedAlbumCount });
        }
        if (!cancelBadge.classList.contains('hidden')) {
            cancelBadge.textContent = cancelBadge.dataset.reason === 'incomplete'
                ? I18N.t('download_incomplete')
                : I18N.t('cancel_selection_badge');
        }

        if (window.photosmoveVerify && typeof window.photosmoveVerify.rerender === 'function') {
            window.photosmoveVerify.rerender();
        }

        // Transfer log entries (no data-i18n): re-render all stored entries from the new locale,
        // otherwise log lines produced before the switch keep their old language.
        renderAllLogs();
    }

    function onLocaleChange() {
        refreshDynamicText();
    }

    function fetchWithTimeout(url, options, timeoutMs) {
        const controller = new AbortController();
        const timer = setTimeout(() => controller.abort(), timeoutMs);
        return fetch(url, Object.assign({}, options, { signal: controller.signal, cache: 'no-store' }))
            .finally(() => clearTimeout(timer));
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
            const res = await fetchWithTimeout(authUrl, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ pin }),
            }, 10000);
            if (!res.ok) {
                // 4.3 ①: server error-code contract → I18N.te(code); unknown codes pass through
                let errMsg = I18N.t('err_server_status', { status: res.status });
                try { const d = await res.json(); if (d.code) errMsg = I18N.te(d.code, { detail: d.detail }); else if (d.error) errMsg = d.error; } catch (e) {}
                throw new Error(errMsg);
            }
            const data = await res.json();
            saveToken(data.token);
            showDashboard();
        } catch (err) {
            // 4.3 ③: fetch network-layer failure (TypeError, no response body, e.g. Failed
            // to fetch) → localized copy
            const isTimeout = err && err.name === 'AbortError';
            const isNetworkErr = err instanceof TypeError || isTimeout;
            const displayMsg = isTimeout ? I18N.t('connect_timeout') : (isNetworkErr ? I18N.t('network_error') : (err.message || I18N.t('err_default')));
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
                // Keep the diagnostic dump in technical English (for feedback/debugging); do not translate
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
        appendLog('service_ready');
        await loadAlbums();
        updateCard();
    }

    async function loadAlbums() {
        try {
            const res = await fetchWithTimeout(`${serverUrl}/api/albums`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            }, 15000);
            if (!res.ok) throw new Error(I18N.t('fetch_albums_failed'));
            const data = await res.json();
            cachedAlbums = data.albums;
            selectionInitialized = false;
            selectedAlbumPaths = new Set();
            albumQuery = '';
            albumSearch.value = '';
        } catch (err) {
            cachedAlbums = [];
            appendLog('load_failed', { msg: err.message }, 'error');
        }
    }

    let selectionInitialized = false;
    let selectedAlbumPaths = new Set();
    let albumQuery = '';

    const ALBUM_TONES = ['amber', 'blue', 'green', 'emerald', 'violet', 'sky', 'pink', 'slate'];

    function selectedAlbums() {
        return (cachedAlbums || []).filter(a => selectedAlbumPaths.has(a.path));
    }

    function selectedStats() {
        const list = selectedAlbums();
        return {
            list: list,
            files: list.reduce((s, a) => s + a.file_count, 0),
            size: list.reduce((s, a) => s + a.total_size, 0),
        };
    }

    function ensureSelection() {
        if (!cachedAlbums || cachedAlbums.length === 0) {
            selectedAlbumPaths = new Set();
            selectionInitialized = true;
            return;
        }
        const valid = new Set(cachedAlbums.map(a => a.path));
        if (!selectionInitialized) {
            selectedAlbumPaths = new Set(valid);
            selectionInitialized = true;
            return;
        }
        selectedAlbumPaths = new Set([...selectedAlbumPaths].filter(p => valid.has(p)));
    }

    function createAlbumCard(album, index) {
        const card = document.createElement('button');
        card.type = 'button';
        card.className = 'album-card';
        card.dataset.path = album.path;
        card.dataset.name = (album.name || '').toLowerCase();
        card.setAttribute('role', 'checkbox');
        card.setAttribute('aria-checked', 'false');

        const cover = document.createElement('span');
        cover.className = 'album-cover tone-' + ALBUM_TONES[index % ALBUM_TONES.length];
        let thumbUrl = '';
        if (album.thumb_count > 0) {
            thumbUrl = `${serverUrl}/api/thumb/${index}/0?token=${encodeURIComponent(authToken)}`;
        } else if (album.video_count > 0) {
            thumbUrl = `${serverUrl}/api/videothumb/${index}?token=${encodeURIComponent(authToken)}`;
        }
        if (thumbUrl) {
            const img = document.createElement('img');
            img.src = thumbUrl;
            img.alt = album.name || '';
            img.loading = 'lazy';
            img.decoding = 'async';
            img.onerror = () => {
                img.remove();
                cover.classList.add('thumb-failed');
            };
            cover.appendChild(img);
        } else {
            cover.classList.add('thumb-failed');
        }
        cover.insertAdjacentHTML('beforeend', '<svg class="album-fallback" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 16l5-5 4 4 3-3 6 6"/><circle cx="8.5" cy="8.5" r="1.2"/></svg>');

        const body = document.createElement('span');
        body.className = 'album-body';
        const name = document.createElement('span');
        name.className = 'album-name';
        name.textContent = album.name || '';
        const meta = document.createElement('span');
        meta.className = 'album-meta';
        meta.textContent = I18N.tn('library_meta', album.file_count || 0, {
            count: (album.file_count || 0).toLocaleString(),
            size: formatSize(album.total_size || 0),
        });
        body.append(name, meta);

        const check = document.createElement('span');
        check.className = 'album-check';
        check.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.2 4.2L19 7"/></svg>';

        card.append(cover, body, check);
        return card;
    }

    function renderAlbumGrid() {
        albumGrid.innerHTML = '';
        if (!cachedAlbums || cachedAlbums.length === 0) return;
        cachedAlbums.forEach((album, index) => {
            albumGrid.appendChild(createAlbumCard(album, index));
        });
    }

    function updateAlbumFilter() {
        const keyword = albumQuery.trim().toLowerCase();
        let visible = 0;
        albumGrid.querySelectorAll('.album-card').forEach(card => {
            const match = !keyword || card.dataset.name.includes(keyword);
            card.classList.toggle('is-hidden', !match);
            if (match) visible++;
        });
        const hasAlbums = !!(cachedAlbums && cachedAlbums.length);
        albumEmpty.classList.toggle('hidden', !hasAlbums || visible !== 0);
        clearSearch.classList.toggle('hidden', albumSearch.value.length === 0);
        clearSearch.disabled = downloading;
        emptyClear.disabled = downloading;
    }

    function clearSelectionVisuals() {
        if (downloading) return;
        doneBadge.classList.add('hidden');
        doneVerify.classList.add('hidden');
        cancelBadge.classList.add('hidden');
        if (dlState.mode !== 'idle') {
            dlState.mode = 'idle';
            progressSection.classList.add('hidden');
            progressBar.style.width = '0%';
            progressBar.classList.remove('done');
            progressPct.textContent = '0%';
        }
    }

    function updateSelectionUI() {
        if (cachedAlbums === null) {
            selectionCount.textContent = '';
            footerCount.textContent = '';
            selectAllBtn.disabled = true;
            clearAllBtn.disabled = true;
            albumSearch.disabled = true;
            clearSearch.disabled = true;
            emptyClear.disabled = true;
            btnDownload.disabled = true;
            btnDownload.textContent = I18N.t('btn_loading');
            btnVerify.disabled = false;
            albumGrid.classList.remove('locked');
            return;
        }

        const albums = cachedAlbums || [];
        const stats = selectedStats();
        const selectedCount = stats.list.length;
        const hasAlbums = albums.length > 0;
        const allSelected = hasAlbums && selectedCount === albums.length;
        const noneSelected = selectedCount === 0;

        selectionCount.innerHTML = hasAlbums
            ? I18N.tn('selection_count_html', stats.files, { selected: selectedCount, total: albums.length, files: stats.files.toLocaleString(), size: formatSize(stats.size) })
            : I18N.t('no_photos');
        footerCount.textContent = noneSelected ? I18N.t('footer_empty') : I18N.tn('footer_selected', selectedCount, { count: selectedCount });

        selectAllBtn.disabled = downloading || !hasAlbums || allSelected;
        clearAllBtn.disabled = downloading || !hasAlbums || noneSelected;
        albumSearch.disabled = downloading;
        clearSearch.disabled = downloading;
        const transferComplete = dlState.mode === 'complete' && !downloading;
        btnVerify.disabled = downloading;
        btnVerify.classList.toggle('hidden', transferComplete);
        btnDownload.classList.toggle('secondary', transferComplete);
        doneVerify.classList.toggle('hidden', !transferComplete);
        albumGrid.classList.toggle('locked', downloading);

        albumGrid.querySelectorAll('.album-card').forEach(card => {
            const selected = selectedAlbumPaths.has(card.dataset.path);
            card.classList.toggle('selected', selected);
            card.setAttribute('aria-checked', String(selected));
            card.disabled = downloading;
            const album = albums.find(a => a.path === card.dataset.path);
            const meta = card.querySelector('.album-meta');
            if (album && meta) {
                meta.textContent = I18N.tn('library_meta', album.file_count || 0, {
                    count: (album.file_count || 0).toLocaleString(),
                    size: formatSize(album.total_size || 0),
                });
            }
        });

        btnDownload.classList.toggle('downloading', downloading);
        if (downloading) {
            btnDownload.disabled = false;
            btnDownload.textContent = I18N.t('cancel_transfer');
        } else if (!hasAlbums) {
            btnDownload.disabled = true;
            btnDownload.textContent = I18N.t('nothing_to_download');
        } else if (noneSelected) {
            btnDownload.disabled = true;
            btnDownload.textContent = I18N.t('select_albums_first');
        } else {
            btnDownload.disabled = false;
            if (dlState.mode === 'complete') {
                btnDownload.textContent = I18N.t('redownload');
            } else if (allSelected) {
                btnDownload.textContent = I18N.tn('download_all_selected', selectedCount, { count: selectedCount, size: formatSize(stats.size) });
            } else {
                btnDownload.textContent = I18N.tn('download_selected', selectedCount, { count: selectedCount, size: formatSize(stats.size) });
            }
        }

        updateAlbumFilter();
    }

    function updateCard() {
        ensureSelection();
        renderAlbumGrid();
        updateSelectionUI();
    }

    albumGrid.addEventListener('click', event => {
        const card = event.target.closest('.album-card');
        if (!card || downloading) return;
        clearSelectionVisuals();
        if (selectedAlbumPaths.has(card.dataset.path)) selectedAlbumPaths.delete(card.dataset.path);
        else selectedAlbumPaths.add(card.dataset.path);
        updateSelectionUI();
    });

    selectAllBtn.addEventListener('click', () => {
        if (downloading || !cachedAlbums || cachedAlbums.length === 0) return;
        clearSelectionVisuals();
        selectedAlbumPaths = new Set(cachedAlbums.map(a => a.path));
        updateSelectionUI();
    });

    clearAllBtn.addEventListener('click', () => {
        if (downloading) return;
        clearSelectionVisuals();
        selectedAlbumPaths.clear();
        updateSelectionUI();
    });

    albumSearch.addEventListener('input', () => {
        albumQuery = albumSearch.value;
        updateAlbumFilter();
    });

    albumSearch.addEventListener('keydown', event => {
        if (event.key === 'Escape') {
            albumSearch.value = '';
            albumQuery = '';
            updateAlbumFilter();
        }
    });

    clearSearch.addEventListener('click', () => {
        albumSearch.value = '';
        albumQuery = '';
        updateAlbumFilter();
        albumSearch.focus();
    });

    emptyClear.addEventListener('click', () => {
        albumSearch.value = '';
        albumQuery = '';
        updateAlbumFilter();
        albumSearch.focus();
    });

    // --- Download ---
    let downloading = false;
    let selectedAlbumCount = 0;
    let dlGeneration = 0;
    let currentBatchId = null;
    let activePollOnce = null;
    let dlSpeedSamples = [];
    // For re-rendering dynamic blocks on locale change: the big-file block / current album
    // title (neither has data-i18n; without re-rendering, stale language remains).
    let activeBigFileBatch = null;
    let activeAlbumMeta = null;   // {key, params} — progressAlbum's current copy (albums_count/album_progress)
    // Progress-section state-machine snapshot: refreshProgressText re-renders
    // speed/eta/downloaded from this on locale change (these elements have no data-i18n,
    // so applyToDOM skips them; without a snapshot, copy freezes after a locale change in
    // the success/interrupted states).
    let dlState = { mode: 'idle', overall: 0, total: 0, speed: 0, remaining: Infinity };

    if (btnVerifyDone) {
        btnVerifyDone.addEventListener('click', () => {
            if (window.photosmoveVerify && typeof window.photosmoveVerify.open === 'function') {
                window.photosmoveVerify.open();
            } else {
                btnVerify.click();
            }
        });
    }

    btnDownload.addEventListener('click', () => {
        // One button, multiple states: click while transferring = cancel, otherwise = start download.
        if (downloading) { cancelDownload(); return; }
        const paths = selectedAlbums().map(a => a.path);
        if (paths.length === 0) return;
        // HEIC bad-review guard: Free mode keeps HEIC as-is, which Windows can't open by
        // default. Prompt once before the first download.
        let heicWarned = false;
        try { heicWarned = localStorage.getItem('photosmove_heic_warned') === '1'; } catch (e) {}
        if (!heicWarned) {
            const msg = I18N.t('heic_warn');
            if (!confirm(msg)) return;
            try { localStorage.setItem('photosmove_heic_warned', '1'); } catch (e) {}
        }
        startDownload(paths);
    });

    async function startDownload(albumPaths) {
        if (downloading) return;
        downloading = true;
        const myGen = ++dlGeneration;

        btnDownload.disabled = false;
        btnDownload.textContent = I18N.t('cancel_transfer');
        btnDownload.classList.add('downloading');
        progressSection.classList.remove('hidden');
        window.scrollTo(0, 0);
        document.documentElement.scrollTop = 0;
        if (document.body) document.body.scrollTop = 0;
        doneBadge.classList.add('hidden');
        doneVerify.classList.add('hidden');
        cancelBadge.classList.add('hidden');
        progressBar.style.width = '0%';
        progressBar.classList.remove('done');
        progressPct.textContent = '0%';
        selectedAlbumCount = albumPaths.length;
        doneVerify.classList.add('hidden');
        verifiedBadge.classList.add('hidden');
        activeAlbumMeta = { key: 'transferring_albums', params: { count: selectedAlbumCount }, pluralCount: selectedAlbumCount };
        activeBigFileBatch = null;
        dlState = { mode: 'init', overall: 0, total: 0, speed: 0, remaining: Infinity };
        updateSelectionUI();
        refreshProgressText();
        progressAlbum.textContent = I18N.tn('transferring_albums', selectedAlbumCount, { count: selectedAlbumCount });

        appendLog(albumPaths.length === 1 ? 'transfer_start_one' : 'transfer_start', { count: albumPaths.length }, 'primary');

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
                dlState.mode = 'idle';
                progressSection.classList.add('hidden');
                progressBar.style.width = '0%';
                progressBar.classList.remove('done');
                appendLog('no_files_to_download', null, 'warning');
                updateSelectionUI();
                return;
            }

            const totalSize = batches.reduce((s, b) => s + b.total_size, 0);
            let totalDownloaded = 0;
            let downloadedBatches = 0;
            const t0 = Date.now();
            dlState = { mode: 'preparing', overall: 0, total: totalSize, speed: 0, remaining: Infinity };
            refreshProgressText();
            activeAlbumMeta = { key: 'transferring_albums', params: { count: selectedAlbumCount }, pluralCount: selectedAlbumCount };
            progressAlbum.textContent = I18N.tn('transferring_albums', selectedAlbumCount, { count: selectedAlbumCount });

            for (let i = 0; i < batches.length; i++) {
                if (myGen !== dlGeneration) return;

                const batch = batches[i];
                appendLog('batch_progress', { i: i + 1, n: batches.length, name: batch.album_name, size: formatSize(batch.total_size) });

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

            if (downloadedBatches === batches.length) {
                const elapsed = ((Date.now() - t0) / 1000).toFixed(1);
                const avgSpeed = totalSize / parseFloat(elapsed);
                const filesInDone = batches.reduce((s, b) => s + (b.file_count || 0), 0);
                appendLog(filesInDone === 1 ? 'all_done_one' : 'all_done', { files: filesInDone, size: formatSize(totalDownloaded), elapsed: elapsed, speed: formatSize(avgSpeed) }, 'success');
                finishDownload(true);
            } else if (downloadedBatches === 0) {
                showCancelled();
            } else {
                appendLog('download_incomplete', null, 'error');
                finishDownload(false);
            }
        } catch (err) {
            if (myGen !== dlGeneration) return;
            appendLog('download_failed', { msg: err.message }, 'error');
            btnDownload.classList.remove('downloading');
            finishDownload(false);
        }
    }

    function downloadOneBatch(batch, batchIdx, batchTotal, startBytes, totalSize, globalT0) {
        return new Promise((resolve) => {
            dlSpeedSamples = [];
            if (batchTotal === 1) {
                activeAlbumMeta = { key: 'transferring_albums', params: { count: selectedAlbumCount }, pluralCount: selectedAlbumCount };
            } else {
                activeAlbumMeta = { key: 'album_progress', params: { i: batchIdx + 1, n: batchTotal, name: batch.album_name } };
            }
            progressAlbum.textContent = activeAlbumMeta.pluralCount != null
                ? I18N.tn(activeAlbumMeta.key, activeAlbumMeta.pluralCount, activeAlbumMeta.params)
                : I18N.t(activeAlbumMeta.key, activeAlbumMeta.params);

            const isBigFileBatch = !!batch.big_file;
            if (isBigFileBatch) renderBigFileBatch(batch);

            const downloadUrl = `${serverUrl}/api/batch/${batch.id}?token=${encodeURIComponent(authToken)}`;
            const gen = dlGeneration;
            const batchStartTime = Date.now();

            // Poll progress (once per second, replacing the SSE long connection — avoids
            // progress stalls from TCP send-buffer backlog).
            currentBatchId = String(batch.id);
            const pollUrlBase = `${serverUrl}/api/progress-poll?token=${encodeURIComponent(authToken)}&batch=${encodeURIComponent(batch.id)}`;
            let completed = false;
            let lastMsg = Date.now();
            let lastSent = -1;
            let pollInterrupted = false;

            const applyProgress = (data) => {
                if (completed) return;
                const sent = data.sent || 0;
                const total = (batchTotal === 1 && data.total > 0)
                    ? data.total
                    : (totalSize > 0 ? totalSize : (data.total || 0));
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
                const remaining = speed > 0 ? Math.max(0, (total - overall)) / speed : Infinity;
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
                    const pollUrl = `${pollUrlBase}&_=${Date.now()}`;
                    const res = await fetch(pollUrl, { headers: { 'Authorization': `Bearer ${authToken}` }, cache: 'no-store' });
                    if (!res.ok) return;
                    const data = await res.json();
                    if (completed) return;
                    lastMsg = Date.now();
                    if (pollInterrupted) {
                        pollInterrupted = false;
                        appendLog('poll_resumed', { name: batch.album_name }, 'success');
                    }
                    applyProgress(data);
                    if (data.cancelled) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        appendLog('batch_cancelled', { name: batch.album_name }, 'warning');
                        resolve(false);
                        return;
                    }
                    if (data.done) {
                        clearInterval(pollTimer);
                        currentBatchId = null;
                        completed = true;
                        clearBigFileBatch();
                        const batchElapsed = ((Date.now() - batchStartTime) / 1000).toFixed(1);
                        appendLog('batch_done', { name: batch.album_name, size: formatSize(batch.total_size), elapsed: batchElapsed }, 'success');
                        resolve(true);
                        return;
                    }
                } catch (e) {
                    console.warn('[poll] fetch failed:', e.message);
                }
            };

            activePollOnce = pollOnce;
            const pollTimer = setInterval(pollOnce, 1000);
            const watchdog = setInterval(() => {
                if (completed) { clearInterval(watchdog); return; }
                if (gen !== dlGeneration) { clearInterval(watchdog); clearInterval(pollTimer); resolve(false); return; }
                if (Date.now() - lastMsg > 30000 && !pollInterrupted) {
                    pollInterrupted = true;
                    appendLog('poll_timeout', { name: batch.album_name }, 'warning');
                    appendLog('poll_check_downloader', null, 'info');
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
                        appendLog('batch_bigfile_cancelled', { name: batch.album_name }, 'warning');
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
                setTimeout(pollOnce, 250);
            })();
        });
    }

    function resumeProgressPoll() {
        if (!document.hidden && downloading && activePollOnce) activePollOnce();
    }

    document.addEventListener('visibilitychange', () => {
        if (!document.hidden) resumeProgressPoll();
    });
    window.addEventListener('focus', resumeProgressPoll);

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
                        appendLog('cancel_not_sent', null, 'warning');
                    }
                });
        }

        progressSection.classList.add('hidden');
        doneBadge.classList.add('hidden');
        showCancelled();
        appendLog('cancelled_notice', null, 'info');
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
            activeAlbumMeta = { key: 'progress_all_done', params: {} };
            progressAlbum.textContent = I18N.t('progress_all_done');
            refreshProgressText(); // speed=Done, eta empty, downloaded keeps current overall/total
            doneBadge.textContent = I18N.tn('done_albums_badge', selectedAlbumCount, { count: selectedAlbumCount });
            doneBadge.classList.remove('hidden');
            doneVerify.classList.remove('hidden');
        } else {
            doneBadge.classList.add('hidden');
            cancelBadge.dataset.reason = 'incomplete';
            cancelBadge.textContent = I18N.t('download_incomplete');
            cancelBadge.classList.remove('hidden');
            progressSection.classList.add('hidden');
            dlState.mode = 'idle';
            activeAlbumMeta = null;
        }
        updateSelectionUI();
    }

    function showCancelled() {
        downloading = false;
        dlState.mode = 'idle';
        doneBadge.classList.add('hidden');
        cancelBadge.dataset.reason = 'cancelled';
        cancelBadge.textContent = I18N.t('cancel_selection_badge');
        cancelBadge.classList.remove('hidden');
        progressSection.classList.add('hidden');
        btnDownload.classList.remove('downloading');
        activeAlbumMeta = null;
        updateSelectionUI();
    }

    // --- Console ---
    consoleToggle.addEventListener('click', () => {
        consoleBox.classList.toggle('show');
        consoleToggle.classList.toggle('open');
    });

    // Log entries are stored as {key, params, type, ts} and re-rendered from the current locale
    // on language switch, so the Transfer log never lingers in a stale language (mixed-language
    // log lines after switching to English). ts is kept as epoch so the timestamp follows the
    // locale too.
    const logStore = [];
    const LOG_CLASS = {
        info: 'log-info',
        success: 'log-success',
        warning: 'log-warning',
        error: 'log-error',
        primary: 'log-primary',
    };

    function logLocale() {
        return I18N.getLocale() === 'zh' ? 'zh-CN' : 'en-US';
    }

    function renderLogEntry(entry) {
        const p = document.createElement('p');
        p.className = LOG_CLASS[entry.type] || 'log-info';
        const ts = new Date(entry.ts).toLocaleTimeString(logLocale(), { hour12: false });
        p.textContent = ts + ' ' + I18N.t(entry.key, entry.params);
        return p;
    }

    function appendLog(key, params, type) {
        // Allow appendLog(key, type) where params is omitted.
        if (typeof params === 'string') { type = params; params = null; }
        type = type || 'info';
        const entry = { key: key, params: params || {}, type: type, ts: Date.now() };
        logStore.push(entry);
        if (logStore.length > 500) logStore.shift();
        const container = document.getElementById('live-logs');
        container.appendChild(renderLogEntry(entry));
        while (container.children.length > 500) {
            container.removeChild(container.firstChild);
        }
        consoleBox.scrollTop = consoleBox.scrollHeight;
        return entry;
    }

    function renderAllLogs() {
        const container = document.getElementById('live-logs');
        container.innerHTML = '';
        for (const entry of logStore) container.appendChild(renderLogEntry(entry));
        consoleBox.scrollTop = consoleBox.scrollHeight;
    }

    // --- Helpers ---
    function formatSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
        return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
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
            const host = document.getElementById('selection-head');
            if (host) host.parentNode.insertBefore(block, host.nextSibling);
        }
        return block;
    }

    function renderBigFileBatch(batch) {
        activeBigFileBatch = batch;
        const block = ensureBigFileBlock();
        const cardEl = document.querySelector('.main-card');
        if (cardEl) cardEl.classList.add('big-file');

        const sizeStr = formatSize(batch.total_size);
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

            const sizeStr = formatSize(batch.total_size);
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
            const res = await fetchWithTimeout(`${serverUrl}/api/albums`, {
                headers: { 'Authorization': `Bearer ${authToken}` },
            }, 10000);
            if (res.ok) {
                const data = await res.json();
                cachedAlbums = data.albums;
                showDashboard();
            } else if (res.status === 401) {
                clearToken();
            }
        } catch (e) {
            // Keep the token on a transient Safari/network failure. The user can reload
            // and auto-restore will retry instead of forcing the PIN again.
        }
    })();

    // Global error fallback: any uncaught exception is shown on #connect-error, avoiding
    // a "white screen with no error".
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
