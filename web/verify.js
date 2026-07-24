// verify.js — local byte-level verification tool (ZIP central directory parsing + hash-wasm SHA-256).
//
// Two-stage verification (Flow):
//   1. User clicks [🔍 Verify downloaded ZIP] → picks a local ZIP (browser file picker)
//   2. verify.js streams-parses the ZIP central directory (parseZipCentralDirectory), builds
//      fileMap: path → { size, offset }, and reads manifest.json separately
//   3. Stage 1 — size pre-filter (seconds): walk manifest.files; missing / size-mismatch
//      entries are classified immediately and skip SHA-256 (saves time)
//   4. Stage 2 — byte-level SHA-256 (for size-matching entries): read the local header
//      to compute dataStart, file.slice out the entry Blob (a reference, not loaded into
//      memory), send it to the hash-wasm Worker for streaming 1MB-chunk hashing, and
//      compare against manifest.files[].sha256
//   5. UI shows four result classes: ✔ byte-level intact / ✗ corrupted (sha mismatch) /
//                  ⚠️ size mismatch / ✗ missing
//
// Why hash-wasm (WASM) instead of SubtleCrypto: PC browsers reach the phone server over
// HTTP LAN (not HTTPS / not localhost), so crypto.subtle is unavailable (secure-context
// restriction). WASM works in any context, and the vendor bundle is built in.
//
// Large files without blowing memory: readZipEntry is only used for manifest.json (a few
// KB). Per-entry SHA-256 takes a separate path — getDataStart reads only the 30-byte local
// header, then file.slice yields a Blob (reference); the Worker hashes it in 1MB streaming
// chunks, peak memory ~1MB.
//
// Cancel: the user can click [Cancel] anytime → terminate the Worker (all pending promises
// reject); the next verification rebuilds it via ensureWorker().
//
// i18n: all user-visible copy goes through window.I18N.t (verify_* keys, see locales/zh|en.js).
// I18N is loaded by i18n.js before this file and is globally available.
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

    // i18n guard: if i18n.js fails to load, I18N is undefined; return here so later event
    // callbacks don't throw ReferenceError on I18N.t. Static data-i18n elements are still
    // covered by app.js's fallback logic.
    if (typeof I18N === 'undefined') return;

    let cancelled = false;
    // Snapshot of the current verification stage (on locale change, rerender uses it to
    // re-set verify-progress-text/verify-summary; neither has data-i18n so applyToDOM skips
    // them). summary = {key, params, strong} describes the current summary element's copy
    // ('normal'→<strong>, 'gray'→<strong style=color:#6b7280>, null→plain text); rebuilt
    // from this on locale change so parsing/stage/cancelled states don't keep stale copy.
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

    // Expose show/hide/rerender for app.js (onLocaleChange calls rerender on locale change
    // to re-render dynamic copy).
    function rerender() {
        if (!verifyState) return;
        renderSummary(verifyState.summary);
        if (verifyState.stage && progressText) {
            if (verifyState.phase === 'stage1') progressText.textContent = I18N.t('verify_stage1', verifyState.stage);
            else if (verifyState.phase === 'stage2') progressText.textContent = I18N.t('verify_stage2', verifyState.stage);
        }
        // done state uses the modal (separate DOM, not re-rendered here).
    }
    window.photosmoveVerify = { show, hide, rerender };

    // Clicking [Verify] opens the ZIP picker directly; the panel stays collapsed for now
    // (avoids an empty white box). The panel is only remove('hidden')'d inside onZipPicked
    // (after the user picks a ZIP).
    btnVerify.addEventListener('click', () => {
        reset();
        if (zipInput) zipInput.click();
    });

    if (cancelBtn) {
        cancelBtn.addEventListener('click', () => {
            cancelled = true;
            verifyState = { phase: 'cancelled', stage: null, summary: { key: 'verify_cancelled', strong: 'gray' } };
            // Terminate the Worker and reject all in-flight hashes (rebuilt on next run)
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

    // (Path A "pick an extracted folder" has been removed — ZIP selection is now the only
    // supported mode, simplifying the interaction.)

    // Verification entry point (shared): takes fileMap (path → {size, offset}) + ZIP File +
    // manifest. Two stages: (1) size pre-filter — missing/size-mismatch entries classified
    // immediately; (2) byte-level SHA-256 — hash-wasm check for each size-matching entry.
    // sourceLabel is used in the result modal title.
    async function runVerify(fileMap, file, manifest, sourceLabel) {
        const entries = manifest.files;
        let mismatched = 0, missing = 0;
        let byteOk = 0, corrupted = 0, sizeOnly = 0;
        const mismatchedFiles = [];
        const missingFiles = [];
        const corruptedFiles = [];
        const sizeMatched = []; // passed the size pre-filter, pending SHA-256 check

        // ===== Stage 1: size pre-filter (seconds) =====
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
                // Yield so the progress bar repaints.
                await new Promise(r => setTimeout(r, 0));
            }
        }

        if (cancelled) {
            verifyState = { phase: 'cancelled', stage: null, summary: { key: 'verify_cancelled', strong: 'gray' } };
            if (progressText) progressText.textContent = I18N.t('verify_cancelled');
            return;
        }

        // ===== Stage 2: byte-level SHA-256 (for size-matching entries) =====
        if (sizeMatched.length > 0) {
            ensureHasher();
            const total = sizeMatched.length;
            for (let i = 0; i < total; i++) {
                if (cancelled) break;
                const { entry, f } = sizeMatched[i];

                if (!entry.sha256) {
                    // manifest has no sha256 on record — size matches only; byte-level
                    // verification impossible.
                    sizeOnly++;
                } else {
                    let sha = null;
                    try {
                        const dataStart = await getDataStart(file, f);
                        // The Blob is a reference (not read into memory); the Worker hashes
                        // it internally in 1MB streaming chunks.
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

        // ===== Result modal (four classes) =====
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

        // Detail list (corrupted + mismatched + missing, capped at 25 per class).
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

    // ============== hash-wasm Worker management (byte-level SHA-256) ==============
    //
    // A single Worker processes entries serially: simple + avoids pulling multiple large
    // Blobs into memory concurrently. Each entry postMessages one Blob (a reference, not a
    // full copy); the Worker hashes it in 1MB streaming chunks. Promise + id correlation
    // (Map<id, resolve/reject>); on cancel, terminate the Worker and reject all pending,
    // then ensureHasher() rebuilds it on the next run.
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
        // terminate() does not fire onmessage → proactively reject all in-flight promises
        // to wake up their awaits.
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

    // getDataStart: read the entry's local file header (30 bytes) to compute where the
    // data begins. Shares local-header parsing logic with readZipEntry, but returns only
    // the dataStart offset without reading the entry contents into a Uint8Array (avoids
    // blowing memory on large files). The caller file.slice's a Blob reference and hands
    // it to the Worker for streaming hashing.
    //   local header layout:  [0..4)  signature 0x04034b50
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

    // ============== Direct ZIP-pick verification (streaming central-directory parsing,
    //                no content decompression) ==============
    //
    // PhotosMove ZIPs use Store mode (no compression), so verify only needs each entry's
    // uncompressed_size, not the decompressed content. Read the ZIP central directory (CD)
    // directly to extract filename + size; manifest.json is read separately via a partial
    // file.slice.
    //
    // Memory usage: trailing 64KB (to find the EOCD) + central-directory bytes (CD is
    // typically < 1MB per 1000 files) + manifest.json itself (a few KB). Total ~a few MB,
    // independent of the ZIP's total size. Compare with the old implementation
    // (fflate.unzipSync): a 1GB ZIP used 3GB of memory; a 9.84GB ZIP went straight to OOM.

    // Read a little-endian uint64 (ZIP64), returned as a Number. JS Number's max safe
    // integer is 2^53, so any single file < 8PB is representable — far beyond real needs.
    function readU64(view, offset, le) {
        return Number(view.getBigUint64(offset, le));
    }

    // Find the ZIP64 extension (tag 0x0001) in a CD entry's extra field and read the
    // 64-bit fields on demand. The ZIP64 field order is fixed: OriginalSize →
    // CompressedSize → LocalHeaderOffset → DiskNumber (a field appears in extra only when
    // the corresponding CD field == 0xFFFFFFFF). Parsing must advance q in this exact
    // order — do not skip a field just because wantCompressed=false, or the offset read
    // will be misaligned.
    function readZip64Extra(view, extraOff, extraLen, uncompIsZ64, compIsZ64, offsetIsZ64) {
        let uncompressed = null, compressed = null, offset = null;
        let p = extraOff;
        const end = extraOff + extraLen;
        while (p + 4 <= end) {
            const tag = view.getUint16(p, true);
            const sz = view.getUint16(p + 2, true);
            if (tag === 0x0001) {
                let q = p + 4;
                // Read all present fields in spec order, advancing q even for fields the
                // caller doesn't care about.
                if (uncompIsZ64) { uncompressed = readU64(view, q, true); q += 8; }
                if (compIsZ64) { compressed = readU64(view, q, true); q += 8; }
                if (offsetIsZ64) { offset = readU64(view, q, true); q += 8; }
                return { uncompressed, compressed, offset };
            }
            p += 4 + sz;
        }
        return { uncompressed, compressed, offset };
    }

    // Show an error and hide the progress bar (no lingering "Cancel" button + 0% bar).
    function showError(title, detail) {
        const html = '<div class="verify-modal-title err">' + escapeHtml(title) + '</div>' +
            (detail ? '<div class="verify-modal-meta">' + escapeHtml(detail) + '</div>' : '');
        showResultModal(html);
        if (panel) panel.classList.add('hidden');
        if (progressBox) progressBox.classList.add('hidden');
    }

    // Verification result modal: not rendered into the page (avoids taking dashboard
    // space). Closed by clicking the mask or pressing Esc.
    let _resultEscHandler = null;
    function showResultModal(innerHTML) {
        hideResultModal();
        const mask = document.createElement('div');
        mask.className = 'verify-modal-mask';
        mask.innerHTML = '<div class="verify-modal">' + innerHTML + '</div>';
        // Click on the mask (outside the modal) → close; click on modal content →
        // stopPropagation, stays open.
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

            // Read manifest.json's actual bytes (file.slice reads only this small block).
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

    // parseZipCentralDirectory streams-parses the ZIP central directory and returns
    // { fileMap, manifestEntry }.
    // fileMap: Map<path, { size, offset, compressed }> (offset points at the local header)
    // manifestEntry: the first entry whose path contains 'manifest.json'.
    // Streams backward from the end of the file scanning for the EOCD signature
    // (0x06054b50).
    // Why not a fixed trailing 64KB read? Because the server-side padToZipSize may append
    // zero-byte padding at the end of the ZIP (HEIC 3x estimation overshoot / actual size
    // shrinking after EXIF strip); if the padding exceeds 64KB, a fixed window would miss
    // the EOCD. Instead we scan streaming, reading 1MB chunks going backward and stopping
    // at the signature. Worst case scans the whole file, but in practice it's fast (the
    // EOCD is always near the end).
    async function findEocd(file) {
        const chunkSize = 1024 * 1024; // 1 MB
        const minEocdSize = 22; // EOCD is at least 22 bytes
        let fileOffset = file.size; // end position, moving backward
        const chunks = []; // cache read chunks; ZIP64 locator/EOCD parsing may need them
        while (fileOffset > 0) {
            const readSize = Math.min(chunkSize, fileOffset);
            const start = fileOffset - readSize;
            const buf = new Uint8Array(await file.slice(start, fileOffset).arrayBuffer());
            const view = new DataView(buf.buffer);
            // Scan backward within the current chunk
            for (let i = buf.length - minEocdSize; i >= 0; i--) {
                if (view.getUint32(i, true) === 0x06054b50) {
                    return { start, position: i, buf, view };
                }
            }
            chunks.unshift({ start, buf, view });
            fileOffset = start;
            // Safety bound: never scan more than file.size (whole-file worst case). In
            // practice the EOCD is always within the trailing 64KB (unless there is heavy
            // padding), so it's usually found in 1-2 chunks.
            if (chunks.length > 64) break; // 64 MB cap against pathological cases
        }
        return null;
    }

    async function parseZipCentralDirectory(file) {
        // 1. Stream-scan for the EOCD (End of Central Directory)
        const eocd = await findEocd(file);
        if (!eocd) throw new Error(I18N.t('verify_err_no_eocd'));
        const { start: eocdChunkStart, position: eocdOff, view: tailView } = eocd;
        // Absolute offset of the EOCD within the whole file
        const eocdAbsOffset = eocdChunkStart + eocdOff;

        let totalEntries = tailView.getUint16(eocdOff + 10, true);
        let cdSize = tailView.getUint32(eocdOff + 12, true);
        let cdOffset = tailView.getUint32(eocdOff + 16, true);

        // 2. Check for ZIP64 (any field == 0xFFFFFFFF means a ZIP64 extension is present)
        if (totalEntries === 0xFFFF || cdOffset === 0xFFFFFFFF || cdSize === 0xFFFFFFFF) {
            // The ZIP64 EOCD Locator sits 20 bytes before the EOCD, signature 0x07064b50.
            // It may straddle a chunk boundary, so simplify: read 20 bytes starting at
            // eocdAbsOffset - 20.
            const locatorBuf = new Uint8Array(await file.slice(eocdAbsOffset - 20, eocdAbsOffset).arrayBuffer());
            const locatorView = new DataView(locatorBuf.buffer);
            if (locatorView.getUint32(0, true) === 0x07064b50) {
                // locator + 4 (disk) + 8 (zip64 eocd offset, 64-bit)
                const z64EocdOffsetLow = locatorView.getUint32(8, true);
                const z64EocdOffsetHigh = locatorView.getUint32(12, true);
                const z64EocdOffset = z64EocdOffsetHigh * 0x100000000 + z64EocdOffsetLow;

                // The ZIP64 EOCD is at least 56 bytes, signature 0x06064b50
                const z64 = new Uint8Array(await file.slice(z64EocdOffset, z64EocdOffset + 56).arrayBuffer());
                const z64View = new DataView(z64.buffer);
                if (z64View.getUint32(0, true) !== 0x06064b50) throw new Error(I18N.t('verify_err_zip64_eocd'));
                totalEntries = readU64(z64View, 32, true);  // actual entries (CD on this disk)
                cdSize = readU64(z64View, 40, true);
                cdOffset = readU64(z64View, 48, true);
            } else {
                throw new Error(I18N.t('verify_err_zip64_locator'));
            }
        }

        // 3. Read the central directory and walk the entries
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

            // ZIP64 extension: 0xFFFFFFFF → actual value lives in extra
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

    // readZipEntry uses file.slice to read only a single entry's content bytes (without
    // decompressing anything else). The entry's offset points at the local file header
    // (signature 0x04034b50); the data starts after the fixed 30 bytes + filename + extra.
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
