# PhotosMove Privacy Policy

**English** | [中文](privacy.zh-CN.md)

PhotosMove is a photo & video migration tool that moves media from your phone to your computer over the local network, byte-for-byte. This policy explains how data flows.

## No cloud

PhotosMove does not connect to any cloud server. All transfer happens on your **phone ↔ computer** local network (same Wi-Fi).

- Data flow: phone → your computer (same Wi-Fi)
- Never passes through: PhotosMove servers, cloud storage, or any third party
- The app has no "upload", "sync", or "sign in" button — it cannot send data anywhere on its own

## No user data collection / telemetry

PhotosMove does not collect or report any user data:

- No account system, no registration, no login
- No analytics, no crash reporting, no usage statistics
- No ad SDKs

The code is fully open source and auditable.

## HEIC / EXIF preserved byte-for-byte

Media is transferred **byte-for-byte** — no conversion, no stripping, no recompression:

- Formats: JPG / HEIC / RAW / DNG / MP4 / MOV / Live Photo keep their original format
- Metadata: capture time, camera settings, GPS, and other EXIF info are fully preserved

PhotosMove does not read, analyze, or modify photo content — it only moves bytes from your phone to your computer.

## verify.js runs entirely locally

PhotosMove's web UI includes a `verify.js` byte-level integrity tool that **runs entirely in your PC browser**:

- Reads: the local ZIP file you select via the browser file picker
- Computes: SHA-256 in the browser via WASM (hash-wasm)
- Does not upload: no file, filename, or hash is ever sent to the phone or anywhere else

## PIN / Token are for pairing only

Each time PhotosMove starts, the app generates:

- **4-digit PIN**: for the initial pairing between phone and computer, to prevent random devices on the LAN from accessing
- **Bearer Token**: a session token issued after PIN verification, carried by subsequent requests

The PIN and Token travel only on the phone ↔ PC local network, never leave the LAN, and become invalid when the app closes. They are regenerated on the next start.

## Permissions

PhotosMove needs the following Android permissions, all used to read your media and complete the transfer, for no other purpose:

- Photos / videos / files access: read the albums you want to migrate
- Background running (foreground service + persistent notification): keep large transfers alive across screen-off
- Local network access: run the HTTP server on Wi-Fi for your computer to connect

PhotosMove does not request contacts, location, SMS, call log, or any permission unrelated to photo migration.

## Open source and auditable

PhotosMove is fully open source. Every statement in this policy can be verified in the source code. If you find any behavior inconsistent with this policy, please report it on GitHub Issues.
