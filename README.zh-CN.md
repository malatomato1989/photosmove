# PhotosMove

[English](README.md) | **中文**

> 通过局域网 Wi-Fi 把 Android 手机里的**照片**和**视频**字节级无损搬到电脑，保留原始目录结构。**不联网、PC 端零安装、无需账号。**

PhotosMove 是一个**一键**照片和视频迁移工具：一次操作把 10 万张 / 100GB+ 的文件字节级原样从手机搬到电脑。不管你叫它迁移、备份、导出，还是"把手机照片传到电脑上"——只要你要无损、不联网，PhotosMove 都能搞定。

## 演示

📺 [观看演示视频](https://github.com/malatomato1989/photosmove/blob/main/store/promo-video.mp4)

## 为什么选 PhotosMove？

大多数"传照片"应用会压缩、转码，或依赖云 + 安装客户端。PhotosMove 不一样：

- 🔒 **字节级无损** —— 原始字节保留（JPG/HEIC/HEIF/RAW/DNG/MP4/MOV/Live Photo），EXIF、GPS、时间戳完整，不重压缩、不转码
- 🖱️ **一键操作** —— 选相册、下载，搞定。PC 端只需浏览器，无需驱动、无需客户端、无需账号
- 📁 **保留原始目录结构** —— 文件落到与手机一致的 DCIM/Pictures 布局
- 🚀 **100GB+ 一次搬完** —— 单个流式 ZIP 下载，按相册可单独重传，ZIP64 支持单文件 > 4GB
- ✅ **内置完整性校验** —— 下载后 SHA-256 字节级校验（浏览器本地运行，不上传）
- 🏠 **100% 本地** —— 传输不出局域网，不连云、不采集遥测、无服务端
- 🔓 **开源（GPL-3.0）** —— 完全可审计

## 工作原理

手机运行 HTTP 服务（Go + Android 前台服务）→ PC 浏览器通过局域网连接 → 输入手机显示的 PIN → 选择相册 → 浏览器原生下载单个流式 ZIP → HTTP 轮询实时追踪进度。

## 截图

**手机端** —— 一键启动服务：

<p align="center">
  <img src="store/screenshots/01-main-running-zh.png?v=2" alt="App 运行" height="520">
</p>

**浏览器端** —— 连接并输入 PIN：

<p align="center">
  <img src="store/screenshots/02-web-pin-zh.png?v=2" alt="PIN 验证" height="430">
</p>

**下载与校验** —— 流式 ZIP 下载，SHA-256 校验：

<p align="center">
  <img src="store/screenshots/04-web-download-zh.png?v=2" alt="下载中" height="430">
  &nbsp;
  <img src="store/screenshots/05-web-verify-zh.png?v=2" alt="校验结果" height="430">
</p>

## 与同类工具对比

| | PhotosMove | Immich | 谷歌相册 | Syncthing | PhotoSync |
|---|---|---|---|---|---|
| 定位 | 一键迁移 | 自托管相册管理 | 云相册 | 通用同步 | 传输 app |
| PC 端 | 浏览器（免安装） | 部署服务端 | 云账号 | 装客户端 | 装 app |
| 字节级保留原始 | ✅ | ✅ | ⚠️ 会压缩 | ✅ | ⚠️ 可选 |
| 保留目录结构 | ✅ | ❌ | ❌ | ✅ | ❌ |
| 纯本地（不上云） | ✅ | ✅ | ❌ | ✅ | ⚠️ |
| 100GB+ 一次下载 | ✅ | ✅ | ❌ | ⚠️ | ❌ |
| 开源 | ✅ | ✅ | ❌ | ✅ | ❌ |

## 功能

- 单 ZIP 流式 —— 所有选中相册打成 1 个 ZIP，1 次 `<a>.click()`，100% 浏览器兼容
- ZIP64 —— 支持单文件 > 4GB（DJI / GoPro / iPhone ProRes 大视频）
- 本地校验 —— `verify.js`，size 预筛 + SHA-256（WASM，浏览器本地运行）
- 4 位 PIN 鉴权 + Bearer Token；PIN 错误多次封锁 IP
- 前台服务 + 唤醒锁，大传输不被息屏打断

## 多语言

PhotosMove 开箱即用跟随系统语言（中文或英文），也可手动切换覆盖。

- **Android**：跟随系统语言。可在主界面顶部手动切换。Android 13+ 也可在系统 **设置 → 应用语言** 中切换；Android 8–12 只能在应用内切换（13 以下系统不支持应用级语言）。
- **Web（PC 浏览器）**：跟随浏览器 `navigator.language`。可在连接页和下载页右上角的 🌐 地球图标手动切换。

**新增语言 = 一个翻译文件，零代码改动：**
- Android：新增 `android/app/src/main/res/values-xx/strings.xml`，并在 `LocaleHelper.LANGUAGES` 登记语言代码。
- Web：新增 `web/i18n/locales/xx.js`（对照 `zh.js`/`en.js`），并在 `web/i18n/i18n.js` 的 `SUPPORTED` 登记语言代码。

服务端错误响应为语言无关的错误码（`{"code":"E_XXX"}`），前端按当前界面语言翻译。

## 构建

依赖：JDK 17、Android SDK（build-tools 35.0.0、platform android-35）、Go 1.23+。

```bash
cd android
bash build.sh          # 产出 app/build/outputs/bundle/release/app-release.aab
bash build.sh -i       # 安装 debug 到已连接设备
```

## 常见问题

- **PhotosMove 是云服务吗？** 不是。所有传输都在本地 Wi-Fi 内，不联网、无需账号。
- **这是备份或同步工具吗？** 当前是**一键迁移**工具——把手机照片和视频无损搬到电脑。增量同步在规划中。它不是持续的云端备份。
- **会压缩或重新编码我的照片/视频吗？** 不会。字节级原样传输，EXIF/GPS/格式完整保留。
- **能一次传 100GB+ 吗？** 可以。流式 ZIP + ZIP64，已在大库上验证。
- **支持 iOS 吗？** 暂不支持，目前仅 Android。

## 隐私

不连云端、不采集遥测、无需账号。详见[隐私政策](docs/privacy.zh-CN.md)。

## 关键词

照片迁移、视频迁移、手机照片传电脑、把照片传到电脑、照片导出、相册备份、照片同步、安卓照片传输、无损传输、字节级、HEIC 传输、EXIF 保留、不联网、Immich 替代、谷歌相册替代、开源照片迁移

## License

GPL-3.0 © photosmove
