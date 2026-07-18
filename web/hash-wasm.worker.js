// hash-wasm.worker.js — background SHA-256 hasher for the local verification
// tool (web/verify.js). Runs in a Web Worker so the UI thread stays
// responsive while checksumming 30 GB+ of photos.
//
// Protocol (postMessage):
//   main → worker: { type: "hash", id, file }
//     file is a File / Blob (a slice of the ZIP entry's data region).
//     Worker reads it in 1 MB chunks, pipes through hash-wasm's
//     createSHA256() incremental updater, returns the hex digest.
//   worker → main: { type: "hashed", id, sha256, size, error? }
//
// Why a worker: SHA-256 over tens of GB takes minutes with hash-wasm.
// Blocking the main thread freezes the page (no progress bar, no cancel).
//
// Why hash-wasm (WASM) instead of SubtleCrypto: the PC browser reaches the
// phone server over HTTP on the LAN (not HTTPS, not localhost), so
// crypto.subtle is unavailable (secure-context only). WASM has no such
// restriction. Classic workers + importScripts work under HTTP LAN too.
self.importScripts('vendor/hash-wasm.umd.js');

let hasherPromise = null;

async function getHasher() {
    if (!hasherPromise) {
        hasherPromise = self.hashwasm.createSHA256();
    }
    return hasherPromise;
}

async function hashBlob(file) {
    const hasher = await getHasher();
    hasher.init();
    const chunkSize = 1 * 1024 * 1024; // 1 MB
    let offset = 0;
    while (offset < file.size) {
        const end = Math.min(offset + chunkSize, file.size);
        const slice = file.slice(offset, end);
        const buf = await slice.arrayBuffer();
        hasher.update(new Uint8Array(buf));
        offset = end;
    }
    return hasher.digest('hex');
}

self.onmessage = async (ev) => {
    const msg = ev.data;
    if (!msg || typeof msg !== 'object') return;
    if (msg.type === 'hash') {
        try {
            const sha256 = await hashBlob(msg.file);
            self.postMessage({
                type: 'hashed',
                id: msg.id,
                sha256,
                size: msg.file.size,
            });
        } catch (err) {
            self.postMessage({
                type: 'hashed',
                id: msg.id,
                error: String((err && err.message) || err),
            });
        }
    }
};
