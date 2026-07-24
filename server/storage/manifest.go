// Package storage provides persistence for photosmove Pro integrity verification
// and incremental sync subsystems.
//
// This file implements the ZIP-tail manifest system: each batch ZIP ends with
// a manifest.json entry recording SHA-256 + original_path + size for every
// packed file. The manifest is padded with spaces (RFC 8259 JSON-safe) to a
// pre-reserved byte count so the archive's Content-Length can be declared up
// front (iron rule 2).
package storage

import (
	"encoding/json"
	"io"
)

// ManifestSchemaVersion is the manifest format version. Bumped on any
// breaking change to ManifestEntry or ManifestPayload fields.
const ManifestSchemaVersion = 1

// ManifestEntry describes a single file inside a batch ZIP and is the unit
// the client verifies against (size + SHA-256).
type ManifestEntry struct {
	Path         string `json:"path"`          // path inside the ZIP (after rename / HEIC conversion)
	OriginalPath string `json:"original_path"` // absolute path on the phone (DCIM/Camera/...)
	Converted    bool   `json:"converted"`     // true if HEIC was converted to JPG
	Size         int64  `json:"size"`          // file size in bytes (post-conversion)
	Sha256       string `json:"sha256"`        // 64-char lowercase hex digest of bytes written to ZIP
}

// ManifestPayload is the JSON document serialized to manifest.json.
//
// single-zip-trust-tcp §1.4.3: the `.failed` field is removed. We trust TCP
// integrity and do not track per-file retry candidates.
type ManifestPayload struct {
	Schema  int              `json:"schema"`
	Session string           `json:"session_id"`
	BatchID string           `json:"batch_id"`
	Files   []ManifestEntry  `json:"files"`
}

// ManifestReservedSize returns the byte budget to pre-reserve in
// calculateZipSize for the manifest.json entry. The formula is intentionally
// conservative — there is no truncate-and-recover fallback because the
// streaming ResponseWriter cannot seek back once bytes are flushed.
//
// Real-world budget (measured against DCIM/Camera/IMG_XXXX.jpg-style paths):
//
//	ManifestEntry JSON encoded ≈ 70 bytes fixed overhead (keys + punctuation)
//	 + path (avg 40-120 bytes, escaped quotes/Chinese can balloon to 200+)
//	 + original_path (absolute DCIM path, 80-200 bytes)
//	 + sha256 (64 hex chars)
//	 + size (8-20 bytes) + converted (5)
//	 → worst-case ~450 bytes per entry
//
//	per-entry: 512 bytes (covers Chinese + escapes + 64-char sha + long paths)
//	fixed:     max(8192, filesCount*16) — envelope, schema/session/batch_id
func ManifestReservedSize(filesCount int) int64 {
	perEntry := int64(512)
	fixed := int64(8192)
	if slack := int64(filesCount) * 16; slack > fixed {
		fixed = slack
	}
	return int64(filesCount)*perEntry + fixed
}

// spaceReader produces an infinite stream of ASCII spaces (0x20). Used to pad
// the manifest entry to its reserved size: JSON parsers (Go / JS / Python)
// accept whitespace after the JSON text, but reject NUL bytes (RFC 8259
// forbids control characters U+0000-U+001F outside strings). The legacy
// zeroReader in archiver.go cannot be reused for this reason.
type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// WriteManifest serializes payload to w as JSON, then pads with spaces up to
// reserved bytes. Returns the total bytes written (payload + padding), which
// the caller uses to verify Content-Length accounting.
//
// If the serialized JSON exceeds `reserved` (formula drift), the function
// returns ErrManifestOverflow — there is no truncation fallback by design.
// The caller MUST fix the formula coefficient before shipping.
type manifestError string

func (e manifestError) Error() string { return string(e) }

const ErrManifestOverflow = manifestError("manifest payload exceeds reserved size; bump ManifestReservedSize coefficient")

func WriteManifest(w io.Writer, payload ManifestPayload, reserved int64) (int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	written, err := w.Write(data)
	if err != nil {
		return int64(written), err
	}
	total := int64(written)
	if total > reserved {
		return total, ErrManifestOverflow
	}
	pad := reserved - total
	if pad > 0 {
		n, err := io.CopyN(w, spaceReader{}, pad)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
