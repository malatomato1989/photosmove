# PhotosMove

**English** | [中文](README.zh-CN.md)

> Move **photos** and **videos** from Android to your PC over local Wi-Fi — byte-perfect, zero damage, preserving the original folder structure. **No cloud, no PC software to install, no account.**

PhotosMove is a **one-click** photo & video migration tool: move 100,000+ files / 100GB+ byte-for-byte from your phone to your computer in a single tap. Whether you call it migration, backup, export, or just "get my photos off my phone onto the computer" — if you want it lossless and off the cloud, PhotosMove does it.

## Demo

📺 [Watch demo video](https://github.com/malatomato1989/photosmove/blob/main/store/promo-video.mp4)

## Why PhotosMove?

Most "photo transfer" apps compress, transcode, or need the cloud + an installed client. PhotosMove is different:

- 🔒 **Byte-perfect** — original bytes preserved (JPG/HEIC/HEIF/RAW/DNG/MP4/MOV/Live Photo). EXIF, GPS, timestamps intact. No recompression, no transcoding.
- 🖱️ **One click** — pick albums, download. No driver, no client app, no account on the PC side (just a browser).
- 📁 **Original folder structure kept** — files land in the same DCIM/Pictures layout as on the phone.
- 🚀 **100GB+ in one go** — single streaming ZIP download, resumable per album, ZIP64 for files > 4GB.
- ✅ **Built-in integrity check** — SHA-256 byte-level verify after download (runs locally, nothing uploaded).
- 🏠 **100% local** — transfer never leaves your Wi-Fi. No cloud, no telemetry, no servers.
- 🔓 **Open source (GPL-3.0)** — fully auditable.

## How it works

Phone runs an HTTP server (Go + Android foreground service) → PC browser connects over LAN → enter the PIN shown on the phone → pick albums → download a single streaming ZIP via browser native download → HTTP polling tracks progress.

## Screenshots

**On the phone** — start the server with one tap:

<p align="center">
  <img src="store/screenshots/04-app-running-en.png?v=3" alt="App running on phone" height="520">
</p>

**In the browser** — connect and enter the PIN:

<p align="center">
  <img src="store/screenshots/02-web-pin-en.png?v=3" alt="PIN auth" height="430">
</p>

**Download** — one streaming ZIP via browser native download:

<p align="center">
  <img src="store/screenshots/04-web-download-en.png?v=3" alt="Downloading" height="430">
</p>

**Verify** — SHA-256 integrity check, runs locally in the browser:

<p align="center">
  <img src="store/screenshots/05-web-verify-en.png?v=3" alt="Integrity check" height="430">
</p>

## Compared to alternatives

| | PhotosMove | Immich | Google Photos | Syncthing | PhotoSync |
|---|---|---|---|---|---|
| Focus | one-click migration | self-hosted gallery | cloud gallery | general sync | transfer app |
| PC side | browser (no install) | deploy server | cloud account | install client | install app |
| Byte-perfect original | ✅ | ✅ | ⚠️ compresses | ✅ | ⚠️ optional |
| Keeps folder structure | ✅ | ❌ | ❌ | ✅ | ❌ |
| Stays local (no cloud) | ✅ | ✅ | ❌ | ✅ | ⚠️ |
| Bulk 100GB+ one download | ✅ | ✅ | ❌ | ⚠️ | ❌ |
| Open source | ✅ | ✅ | ❌ | ✅ | ❌ |

## Features

- Single streaming ZIP — all selected albums, one `<a>.click()`, 100% browser-compatible
- ZIP64 — single files > 4GB (DJI / GoPro / iPhone ProRes)
- Local verify — `verify.js`: size pre-filter + SHA-256 (WASM, runs in browser)
- 4-digit PIN auth + Bearer token; IP lockout on repeated wrong PIN
- Foreground service + wake lock so large transfers survive screen-off

## Localization

PhotosMove follows your system language (Chinese or English) out of the box, with a manual switcher if you want to override it.

- **Android**: follows the system locale. Manually switchable from the top of the main screen. On Android 13+ you can also set it from system **Settings → App language**. On Android 8–12, switch only via the in-app picker (system per-app language isn't supported below Android 13).
- **Web (PC browser)**: follows the browser's `navigator.language`. Manually switchable from the 🌐 globe icon at the top-right of the connect page and the dashboard.

**Adding a language = one translation file, no code changes:**
- Android: add `android/app/src/main/res/values-xx/strings.xml` and register the code in `LocaleHelper.LANGUAGES`.
- Web: add `web/i18n/locales/xx.js` (mirroring `zh.js`/`en.js`) and add the code to `SUPPORTED` in `web/i18n/i18n.js`.

Server error responses are language-neutral codes (`{"code":"E_XXX"}`); the front end translates them per the current UI language.

## Build

Requirements: JDK 17, Android SDK (build-tools 35.0.0, platform android-35), Go 1.23+.

```bash
cd android
bash build.sh          # produces app/build/outputs/bundle/release/app-release.aab
bash build.sh -i       # install debug to a connected device
```

## FAQ

- **Is PhotosMove a cloud service?** No. All transfer happens on your local Wi-Fi. No cloud, no account.
- **Is this a backup or sync tool?** It's a **one-click migration** tool today — get your photos & videos from phone to computer, lossless. Incremental sync is on the roadmap. It is not a continuous cloud backup.
- **Does it compress or re-encode my photos/videos?** No. Files are transferred byte-for-byte; EXIF/GPS/format are preserved.
- **Can I transfer 100GB+ in one go?** Yes — streaming ZIP + ZIP64, validated on large libraries.
- **iOS support?** Not yet — Android only for now.

## Privacy

No cloud, no telemetry, no account. See [Privacy Policy](docs/privacy.md).

## Keywords

photo transfer, video transfer, transfer photos to computer, Android photo backup, photo sync, migrate photos, export photos, move photos to PC, byte-perfect, lossless, HEIC, EXIF preserve, no cloud, Immich alternative, Google Photos alternative, open source photo transfer

## License

GPL-3.0 © photosmove
