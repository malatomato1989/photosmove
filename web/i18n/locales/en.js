// PhotosMove i18n — English translations
// Structure mirrors zh.js exactly (same ui/errors key set). spec Req6: no hardcoded copy.
window.PHOTOSMOVE_I18N = window.PHOTOSMOVE_I18N || {};
window.PHOTOSMOVE_I18N.en = {
    ui: {
        // --- connect page ---
        title: 'PhotosMove — Photo Migration',
        subtitle: 'Move phone photos to your computer',
        pin_label: 'Enter the code shown in the phone app',
        connect_btn: 'Connect',
        connecting: 'Connecting...',
        tip_1: 'Connect your phone and computer to the same WiFi',
        tip_2: 'Open PhotosMove on your phone to get the code',
        tip_3: 'Enter the code here to connect',
        privacy_link: 'Privacy Policy',

        // --- dashboard ---
        brand_tagline: 'Photo migration · private & local · lightweight · lossless',
        status_ready: 'Ready',
        card_title: 'Camera roll',
        card_desc_loading: 'Loading albums…',
        done_badge: '✓ Transfer complete',
        cancel_badge: 'Cancelled',
        btn_loading: 'Loading...',
        btn_verify: 'Verify downloaded ZIP',
        verify_cancel: 'Cancel',
        console_toggle: 'Transfer log',
        privacy_footer: '🔒 Privacy Policy · Photos move only between your phone and computer — never uploaded to any cloud',
        service_ready: 'Service ready',

        // --- card states ---
        no_photos: 'No photos found',
        no_camera_photos: 'No camera photos found',
        nothing_to_download: 'Nothing to download',
        card_files_html: '<strong>{count}</strong> files · <strong>{size}</strong>',
        download_all: 'Download all ({size})',

        // --- download lifecycle ---
        cancel_transfer: 'Cancel transfer',
        redownload: 'Download again',
        transfer_start: 'Transfer started · {count} albums',
        no_files_to_download: 'No files to download',
        albums_count: '{count} albums',
        batch_progress: 'Batch {i}/{n}: {name} ({size})',
        album_progress: 'Album {i}/{n}: {name}',
        all_done: '✓ All done · {files} files · {size} · {elapsed}s · {speed}/s',
        download_failed: 'Download failed: {msg}',
        scan_failed_short: 'Scan failed',
        load_failed: 'Failed to load: {msg}',
        fetch_albums_failed: 'Failed to load albums',
        fetch_batches_failed: 'Failed to load batches',

        // --- progress ---
        progress_init: 'Sent 0 B / 0 B',
        progress_downloaded: 'Sent {done} / {total}',
        progress_preparing: 'Preparing...',
        progress_paused: 'Paused',
        progress_complete: '✓ Done',
        progress_interrupted: 'Connection interrupted',
        progress_see_downloader: 'check downloads',
        remaining: '{time} left',

        // --- poll status ---
        poll_resumed: '✓ {name} connection restored, progress continues',
        batch_cancelled: '{name} download cancelled',
        batch_done: '✓ {name} · {size} · {elapsed}s',
        batch_bigfile_cancelled: '{name} download cancelled at the large-file prompt',
        poll_timeout: '⚠ {name} no status update for 30s — the browser download may still be running',
        poll_check_downloader: '👉 Check the browser download manager for the actual progress',
        cancel_not_sent: 'Cancel request may not have been delivered — please check the phone app',
        cancelled_notice: 'Cancelled — the browser download manager may still show progress for a few seconds',

        // --- connect error panel ---
        err_default: 'Connection failed',
        err_target: 'Target: {url}',
        err_retry: 'Try again',
        err_copy: 'Copy diagnostic info',
        err_copied: '✓ Copied',
        err_server_status: 'Server returned {status}',
        err_network_hint: '⚠ Cannot reach the phone service over the network.<br>Common causes:<br>· A browser or system proxy (VPN / Clash / V2Ray) is intercepting LAN requests → disable the proxy or add a LAN bypass rule<br>· Phone and computer are not on the same WiFi, or the WiFi (corporate / school) has client isolation enabled<br>· The PhotosMove app was frozen by the system or has exited',
        network_error: 'Network connection failed — please check that the phone service is running',

        // --- HEIC warn ---
        heic_warn: 'Note: HEIC photos are kept as-is (not converted). Windows cannot open .heic by default — install the "HEIF Image Extensions" from Microsoft, or use an online converter.\n\nContinue downloading?',

        // --- big file ---
        bigfile_chip: '★ Large file',
        bigfile_batch_size: 'Batch size: {size} · {name} (single file, cannot be split)',
        bigfile_estimate: 'Estimated time: WiFi ~ {wifi} / USB ~ {usb}',
        bigfile_warning: '⚠️ If interrupted, the entire batch must be re-sent — keep the phone awake',
        bigfile_modal_title: '⚠️ Confirm large-file download',
        bigfile_modal_size: 'Batch size',
        bigfile_modal_estimate: 'Estimated time',
        bigfile_modal_warn: '⚠️ If interrupted, the entire batch must be re-sent — keep the phone awake.<br>The "Resume download" button in the browser does not work; if interrupted, click [Download] again.',
        bigfile_modal_cancel: 'Cancel',
        bigfile_modal_start: 'Start download',

        // --- verify (verification tool, verify.js full flow) ---
        verify_cancelled: 'Cancelled',
        verify_stage1: 'Stage 1/2 size pre-check: {i} / {n}',
        verify_stage2: 'Stage 2/2 byte-level check: {i} / {n} files',
        verify_result_title: 'Verification result ({source})',
        verify_ok: '✔ Byte-level intact: {count} files',
        verify_corrupted: '✗ Corrupted: {count} files (size matches but SHA-256 differs)',
        verify_size_mismatch: '⚠️ Size mismatch: {count} files',
        verify_missing: '✗ Missing: {count} files',
        verify_size_only: '· Size-only check: {count} files (manifest has no sha256)',
        verify_total: 'Total size: {size} · {count} files',
        verify_sha_mismatch_detail: 'SHA-256 mismatch (expected {exp}… / got {act}…)',
        verify_size_detail: 'Expected {exp} / got {act}',
        verify_missing_short: 'missing',
        verify_more: '... {count} more not shown (see manifest.json for the full list)',
        verify_parsing_cd: 'Parsing ZIP central directory: {name} ({size})...',
        verify_no_manifest: '⚠️ manifest.json not found in the ZIP',
        verify_no_manifest_hint: 'Make sure this is a ZIP downloaded from PhotosMove',
        verify_manifest_parse_failed: '⚠️ Failed to parse manifest.json',
        verify_manifest_invalid: '⚠️ manifest.json is not in a valid format',
        verify_source_zip: 'ZIP file',
        verify_zip_parse_failed: '⚠️ Failed to parse the ZIP',
        verify_err_no_eocd: 'EOCD not found (not a ZIP file, or the ZIP is damaged?)',
        verify_err_zip64_eocd: 'ZIP64 EOCD signature mismatch',
        verify_err_zip64_locator: 'ZIP64 flag set but locator not found',
        verify_err_cd_oob: 'CD entry out of bounds (entry {i})',
        verify_err_cd_sig: 'CD signature mismatch (entry {i})',
        verify_err_local_header: 'Local header signature mismatch: {path}',

        // --- time format ---
        time_calculating: 'Calculating...',
        time_seconds: '{n}s',
        time_minutes: '{m}m {s}s',
        time_hours: '{h}h {m}m',

        // --- fatal error ---
        fatal_title: '⚠ PhotosMove frontend crashed ({source})',
        fatal_error: 'Error: {msg}',
        fatal_reload: 'Reload page',

        // --- language switcher ---
        lang_label: '中文',
        lang_en_label: 'English'
    },
    errors: {
        E_UNAUTHORIZED: 'Unauthorized',
        E_METHOD_NOT_ALLOWED: 'Method not allowed',
        E_RATE_LIMITED: 'Too many attempts — please wait a minute',
        E_BAD_REQUEST: 'Invalid request',
        E_PIN_INVALID: 'Incorrect PIN',
        E_DOWNLOAD_IN_PROGRESS: 'A download is already in progress — wait for it to finish or cancel it first',
        E_SCAN_FAILED: 'Scan failed: {detail}',
        E_BATCH_NOT_FOUND: 'Batch not found',
        E_DOWNLOAD_CANCELLED: 'Download cancelled',
        E_INVALID_ID: 'Invalid id',
        E_NOT_FOUND: 'Not found',
        E_NO_THUMBNAIL: 'No thumbnail',
        E_NO_VIDEO_THUMBNAIL: 'No video thumbnail',
        E_MISSING_BATCH_PARAM: 'Missing batch parameter',
        E_MISSING_BATCH_ID: 'Missing batch_id'
    }
};
