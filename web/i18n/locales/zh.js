// PhotosMove i18n — 中文翻译表
// 结构: { ui: {...}, errors: {...} }
//   ui     — 前端 UI/动态文案 key (data-i18n / data-i18n-html / t())
//   errors — 服务端 error code (E_XXX) 的本地化文案, 前端按当前 locale 查
// 加新语言 = 新增一份同结构文件 + i18n.js SUPPORTED 登记, 零逻辑改动 (spec Req4)
window.PHOTOSMOVE_I18N = window.PHOTOSMOVE_I18N || {};
window.PHOTOSMOVE_I18N.zh = {
    ui: {
        // --- connect page ---
        title: 'PhotosMove — 相册迁移',
        subtitle: '相册迁移 · 手机照片搬到电脑',
        pin_label: '手机端 APP 显示的验证码',
        connect_btn: '连接手机',
        connecting: '连接中...',
        tip_1: '手机和电脑连同一 WiFi',
        tip_2: '打开 PhotosMove APP 获取验证码',
        tip_3: '在此输入验证码连接',
        privacy_link: '隐私政策',

        // --- dashboard ---
        brand_tagline: '相册迁移 · 本地隐私 · 超级轻量 · 无损传输',
        status_ready: '已就绪',
        card_title: '相机拍摄',
        card_desc_loading: '正在获取相册信息',
        done_badge: '✓ 传输完成',
        cancel_badge: '已取消',
        btn_loading: '加载中...',
        btn_verify: '校验已下载zip文件',
        verify_cancel: '取消',
        console_toggle: '传输日志',
        privacy_footer: '🔒 隐私政策 · 照片仅在手机与电脑之间本地传输，绝不上传任何云端',
        service_ready: '服务已就绪',

        // --- card states ---
        no_photos: '未发现照片',
        no_camera_photos: '未发现相机照片',
        nothing_to_download: '没有可下载的内容',
        card_files_html: '共 <strong>{count}</strong> 个文件 · <strong>{size}</strong>',
        download_all: '下载全部 ({size})',

        // --- download lifecycle ---
        cancel_transfer: '取消传输',
        redownload: '重新下载',
        transfer_start: '传输启动 · {count} 个相册',
        no_files_to_download: '没有需要下载的文件',
        albums_count: '共 {count} 个相册',
        batch_progress: '批次 {i}/{n}: {name} ({size})',
        album_progress: '相册 {i}/{n}: {name}',
        all_done: '✓ 全部完成 · {files} 个文件 · {size} · {elapsed}s · {speed}/s',
        download_failed: '下载失败: {msg}',
        scan_failed_short: '扫描失败',
        load_failed: '加载失败: {msg}',
        fetch_albums_failed: '获取相册失败',
        fetch_batches_failed: '获取批次失败',

        // --- progress ---
        progress_init: '已传 0 B / 0 B',
        progress_downloaded: '已传 {done} / {total}',
        progress_preparing: '准备中...',
        progress_paused: '已暂停',
        progress_complete: '✓ 完成',
        progress_interrupted: '轮询中断',
        progress_see_downloader: '见下载器',
        remaining: '剩余 {time}',

        // --- poll status ---
        poll_resumed: '✓ {name} 轮询恢复, 进度继续',
        batch_cancelled: '{name} 下载已取消',
        batch_done: '✓ {name} · {size} · {elapsed}s',
        batch_bigfile_cancelled: '{name} 下载已取消 (大文件确认)',
        poll_timeout: '⚠ {name} 轮询 30s 无响应, 浏览器下载可能仍在进行',
        poll_check_downloader: '👉 查看浏览器下载管理器确认实际进度',
        cancel_not_sent: '取消请求可能未送达, 请检查手机端状态',
        cancelled_notice: '已取消, 浏览器下载管理器可能仍显示几秒进度后停止',

        // --- connect error panel ---
        err_default: '连接失败',
        err_target: '目标: {url}',
        err_retry: '再试一次',
        err_copy: '复制诊断信息',
        err_copied: '✓ 已复制',
        err_server_status: '服务器返回 {status}',
        err_network_hint: '⚠ 网络层无法到达手机服务。<br>常见原因:<br>· 浏览器/系统代理 (含 VPN/Clash/V2Ray) 劫持了局域网请求 → 关闭代理或加入直连白名单<br>· 电脑和手机不在同一 WiFi / 公司或学校 WiFi 开启了客户端隔离<br>· PhotosMove APP 被系统冻结或已退出',
        network_error: '网络连接失败，请检查手机服务是否运行',

        // --- HEIC warn ---
        heic_warn: '提示：HEIC 照片将原样保留（不转换）。Windows 默认无法打开 .heic，需安装"HEIF 图像扩展"或用在线工具转换。\n\n是否继续下载？',

        // --- big file ---
        bigfile_chip: '★ 大文件',
        bigfile_batch_size: '批次大小: {size} · {name} (单文件, 不可拆分)',
        bigfile_estimate: '预估传输: WiFi ~ {wifi} / USB ~ {usb}',
        bigfile_warning: '⚠️ 中断需重传整批, 建议保持手机唤醒',
        bigfile_modal_title: '⚠️ 大文件下载确认',
        bigfile_modal_size: '批次大小',
        bigfile_modal_estimate: '预估传输',
        bigfile_modal_warn: '⚠️ 中断需重传整批, 建议保持手机唤醒.<br>浏览器"恢复下载"按钮无效, 中断请重新点 [下载].',
        bigfile_modal_cancel: '取消',
        bigfile_modal_start: '开始下载',

        // --- verify (校验工具, verify.js 全流程文案) ---
        verify_cancelled: '已取消',
        verify_stage1: '阶段 1/2 大小预筛: {i} / {n}',
        verify_stage2: '阶段 2/2 字节级校验: {i} / {n} 个文件',
        verify_result_title: '校验结果 ({source})',
        verify_ok: '✔ 字节级完整: {count} 个文件',
        verify_corrupted: '✗ 字节损坏: {count} 个文件 (大小一致但 SHA-256 不符)',
        verify_size_mismatch: '⚠️ 大小不符: {count} 个文件',
        verify_missing: '✗ 缺失: {count} 个文件',
        verify_size_only: '· 仅大小校验: {count} 个文件 (manifest 无 sha256)',
        verify_total: '总大小: {size} · 共 {count} 个文件',
        verify_sha_mismatch_detail: 'SHA-256 不符 (期望 {exp}… / 实际 {act}…)',
        verify_size_detail: '期望 {exp} / 实际 {act}',
        verify_missing_short: '缺失',
        verify_more: '... 还有 {count} 个未显示 (查看 manifest.json 完整清单)',
        verify_parsing_cd: '解析 ZIP 中央目录: {name} ({size})...',
        verify_no_manifest: '⚠️ ZIP 内未找到 manifest.json',
        verify_no_manifest_hint: '请确认是 PhotosMove 下载的 ZIP',
        verify_manifest_parse_failed: '⚠️ manifest.json 解析失败',
        verify_manifest_invalid: '⚠️ manifest.json 格式无效',
        verify_source_zip: 'ZIP 文件',
        verify_zip_parse_failed: '⚠️ ZIP 解析失败',
        verify_err_no_eocd: '未找到 EOCD (非 ZIP 文件或 ZIP 损坏?)',
        verify_err_zip64_eocd: 'ZIP64 EOCD 签名不匹配',
        verify_err_zip64_locator: 'ZIP64 标记但找不到 locator',
        verify_err_cd_oob: 'CD entry 越界 (entry {i})',
        verify_err_cd_sig: 'CD 签名不匹配 (entry {i})',
        verify_err_local_header: 'Local header 签名不匹配: {path}',

        // --- time format ---
        time_calculating: '计算中...',
        time_seconds: '{n}秒',
        time_minutes: '{m}分{s}秒',
        time_hours: '{h}时{m}分',

        // --- fatal error ---
        fatal_title: '⚠ PhotosMove 前端崩溃 ({source})',
        fatal_error: '错误: {msg}',
        fatal_reload: '刷新页面',

        // --- language switcher ---
        lang_label: '中文',
        lang_en_label: 'English'
    },
    errors: {
        E_UNAUTHORIZED: '未授权',
        E_METHOD_NOT_ALLOWED: '方法不允许',
        E_RATE_LIMITED: '尝试次数过多，请等待一分钟',
        E_BAD_REQUEST: '请求格式错误',
        E_PIN_INVALID: 'PIN 码错误',
        E_DOWNLOAD_IN_PROGRESS: '下载进行中，请等待完成或取消后再试',
        E_SCAN_FAILED: '扫描失败: {detail}',
        E_BATCH_NOT_FOUND: '批次不存在',
        E_DOWNLOAD_CANCELLED: '下载已取消',
        E_INVALID_ID: '无效 ID',
        E_NOT_FOUND: '未找到',
        E_NO_THUMBNAIL: '无缩略图',
        E_NO_VIDEO_THUMBNAIL: '无视频缩略图',
        E_MISSING_BATCH_PARAM: '缺少 batch 参数',
        E_MISSING_BATCH_ID: '缺少 batch_id'
    }
};
