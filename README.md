# PhotosMove

**English** | [中文](README.zh-CN.md)

> Move **photos** and **videos** from Android to your PC over local Wi-Fi — byte-perfect, zero damage, preserving the original folder structure. **No cloud, no PC software to install, no account.**

PhotosMove is a **one-click** photo & video migration tool: move 100,000+ files / 100GB+ byte-for-byte from your phone to your computer in a single tap. Whether you call it migration, backup, export, or just "get my photos off my phone onto the computer" — if you want it lossless and off the cloud, PhotosMove does it.

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

Phone runs an HTTP server (Go + Android foreground service) → PC browser connects over LAN → enter the PIN shown on the phone → pick albums → download a single streaming ZIP via browser native download → SSE tracks progress.

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
