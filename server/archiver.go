package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"photosmove/archiver"
	"photosmove/storage"
	"strings"
	"time"
)

// safeZipName sanitizes a file path for use as a ZIP entry name.
// Rejects paths containing "..", then removes leading dots and slashes.
func safeZipName(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	if strings.Contains(p, "..") {
		return "_"
	}
	p = strings.TrimLeft(p, "./")
	if p == "" {
		p = "_"
	}
	return p
}

// flatZipName returns just the base filename for flat ZIP mode.
func flatZipName(p string) string {
	return filepath.Base(p)
}

// throttleWriter wraps an io.Writer with token-bucket rate limiting.
type throttleWriter struct {
	w       io.Writer
	burst   int64 // max bytes that can be sent instantly
	tokens  int64 // current token count
	last    time.Time
	rate    int64 // bytes per second
}

func newThrottleWriter(w io.Writer, bytesPerSec int64) *throttleWriter {
	return &throttleWriter{
		w:      w,
		burst:  bytesPerSec,
		tokens: bytesPerSec,
		last:   time.Now(),
		rate:   bytesPerSec,
	}
}

func (t *throttleWriter) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		// Refill tokens based on elapsed time.
		now := time.Now()
		elapsed := now.Sub(t.last)
		t.last = now
		t.tokens += int64(elapsed.Seconds() * float64(t.rate))
		if t.tokens > t.burst {
			t.tokens = t.burst
		}

		if t.tokens <= 0 {
			// Wait long enough to generate at least one chunk.
			wait := time.Second * time.Duration(len(p)-written) / time.Duration(t.rate)
			if wait < time.Millisecond {
				wait = time.Millisecond
			}
			time.Sleep(wait)
			continue
		}

		chunk := int64(len(p) - written)
		if chunk > t.tokens {
			chunk = t.tokens
		}
		n, err := t.w.Write(p[written : written+int(chunk)])
		written += n
		t.tokens -= int64(n)
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// countWriter counts bytes written without storing them.
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// ErrSkipFile marks an "open-stage" failure (RetryOpen exhausted, HEIC HTTP
// fetch failed, GPS-strip read failed) where no ZIP entry has been opened
// yet. writeBatchZip treats this as recoverable: logs the skip and continues
// the loop instead of poisoning the stream.
//
// Errors after zw.CreateHeader (io.Copy / fw.Write) MUST NOT wrap this
// sentinel — those bytes are already streamed and the archive is unrecoverable.
var ErrSkipFile = errSkipFile{}

type errSkipFile struct{}

func (errSkipFile) Error() string { return "archiver: skip file (open-stage failure)" }

// ctxWriter wraps an io.Writer and aborts on context cancellation.
// Uses a goroutine so that a blocked Write (e.g. browser paused download)
// can be interrupted by context cancellation.
// On write timeout, calls cancelFn so the handler's ctx.Err() path triggers
// TCP Hijack+RST, which unblocks the leaked goroutine.
type ctxWriter struct {
	w       io.Writer
	ctx     context.Context
	cancel  context.CancelFunc
}

type writeResult struct {
	n   int
	err error
}

func (c *ctxWriter) Write(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
	}
	ch := make(chan writeResult, 1)
	go func() {
		n, err := c.w.Write(p)
		ch <- writeResult{n, err}
	}()
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	case r := <-ch:
		return r.n, r.err
	case <-time.After(30 * time.Second):
		// Write timeout: TCP send buffer full (browser paused/stopped reading).
		// Trigger context cancellation so handler enters Hijack path,
		// which sends RST and unblocks the goroutine stuck in c.w.Write.
		// Note: this fires at 30s (before the 60s stall timeout in the
		// progress goroutine) because Write blocks immediately when the
		// buffer is full; the 60s stall only triggers when the buffer
		// still has room but sent bytes aren't advancing.
		if c.cancel != nil {
			c.cancel()
		}
		return 0, fmt.Errorf("write timeout: client not reading for 30s")
	}
}

// zeroReader provides an infinite stream of zero bytes.
type zeroReader struct{}

func (z zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// ZipWriteOptions controls how files are written into a ZIP archive.
//
// The Free path only relies on FlatMode + EmitManifest (+ SmartRename=false).
// The remaining fields (ConvertHeic/StripGps/StripExifCats/LivePhotoMode/
// SmartRename) are retained as legacy Pro hooks; Free always passes zero
// values ("preserve" / false / nil), and the corresponding archiver branches
// have been removed. Keeping the fields avoids touching external call sites
// en masse and eases future Pro reuse.
type ZipWriteOptions struct {
	ConvertHeic   bool
	FlatMode      bool
	SmartRename   bool
	StripGps      bool
	// StripExifCats extends StripGps to additional EXIF categories
	// (gps/time/device/shot/author). Multiple may be selected at once.
	// Empty (with StripGps=false) means no EXIF stripping.
	StripExifCats []string
	LivePhotoMode string // "preserve", "mp4", or "photo"

	// EmitManifest appends a manifest.json entry at the ZIP tail recording
	// SHA-256 + original_path + size for every packed file. Free verify.js
	// reads it to do byte-level integrity checks after download.
	EmitManifest bool

	// SessionID / BatchID are stamped into manifest.{session_id,batch_id} so
	// the client can correlate a downloaded ZIP back to its download session
	// for diagnostics. Ignored when EmitManifest is false.
	SessionID string
	BatchID  string
}

// BatchOpts is retained for API stability. With single-zip-trust-tcp, the
// planner no longer splits files — every selected file lands in one ZIP —
// so the fields are informational only.
type BatchOpts struct {
	AlbumName    string // ignored: single batch always uses AlbumName "photos"
	BatchCeiling int64  // ignored: single batch has no size ceiling
}

// Throughput constants power the front-end "this batch will take ~N minutes"
// warning. Conservative on purpose: better to over-estimate and surprise the
// user with a quick finish than the reverse.
const (
	planDWiFiBytesPerSec int64 = 20 * 1000 * 1000 // 20 MB/s
	planDUSBBytesPerSec  int64 = 80 * 1000 * 1000 // 80 MB/s
)

func estimateWiFiSeconds(size int64) int { return int(size / planDWiFiBytesPerSec) }
func estimateUSBSeconds(size int64) int  { return int(size / planDUSBBytesPerSec) }

// planSingleBatch packs every selected file into a single ZIP. The browser
// downloads it with one <a>.click() (single-zip-trust-tcp §1).
//
// The returned Batch has:
//
//   - ID = "photos_<unix_seconds>" — time.Now().Unix() guarantees uniqueness
//     across calls even when the same files are selected twice.
//   - AlbumName = "photos" — fixed download filename stem ("photos_*.zip").
//   - BigFile=true + BiggestFile when ANY file exceeds BigFileThreshold, so
//     the front-end can still render the "this will take a while" hint for
//     the largest single file in the ZIP.
//   - EstimatedWiFiSeconds / EstimatedUSBSeconds derived from the total size.
//
// Returns nil for empty input (callers handle nil → 0 batches).
func planSingleBatch(files []FileEntry, opts BatchOpts) *Batch {
	if len(files) == 0 {
		return nil
	}
	total := sumSize(files)

	// Biggest file drives the UI warning ("largest single file ~N GB").
	// BigFile is set whenever that file crosses BigFileThreshold — the
	// front-end uses this to flip the warning color.
	var biggest *FileEntry
	var biggestSize int64
	for i := range files {
		if files[i].Size > biggestSize {
			biggestSize = files[i].Size
			f := files[i]
			biggest = &f
		}
	}
	bigFile := biggest != nil && IsBigFile(biggest.Size)

	return &Batch{
		ID:                   fmt.Sprintf("photos_%d", time.Now().Unix()),
		AlbumName:            "photos",
		Files:                files,
		TotalSize:            total,
		BigFile:              bigFile,
		BiggestFile:          biggest,
		EstimatedWiFiSeconds: estimateWiFiSeconds(total),
		EstimatedUSBSeconds:  estimateUSBSeconds(total),
	}
}

// calculateZipSize computes the byte size of the ZIP archive by dry-running
// zip.Writer with zero data. For HEIC files with convertHeic enabled, uses a
// 5x size estimate (HEIC→JPEG typically expands ~1.5-2.5x; 5x gives generous
// margin to prevent actual > Content-Length → HTTP truncation → corrupted ZIP).
// Excess estimate is back-filled as trailing zeros by padToZipSize (verify.js
// uses streaming scans that can skip past the padding to find the EOCD).
//
// When opts.EmitManifest is set, the returned size also accounts for the
// manifest.json tail entry: reserved payload bytes (storage.ManifestReservedSize)
// plus the ZIP local-file-header overhead (~64 bytes conservative budget for
// the "manifest.json" filename + fixed 30-byte LFH + data descriptor). This
// keeps Content-Length exact (iron rule 2) so the browser shows a progress bar.
// calculateZipSize computes the exact final ZIP size with a math formula —
// O(num files), NO CRC32 over file contents. Byte constants mirror what
// archive/zip's Writer actually emits (struct.go + writer.go):
//   - local file header = 30 + name + 9 (extended-timestamp extra; Modified is
//     always non-zero because writeBatchZip sets Modified=time.Unix(ModTime))
//   - Store data = f.Size (Method=Store, no compression)
//   - data descriptor = 16 (Flags|=0x8 always set); 24 when the file itself
//     is zip64 (UncompressedSize64 > 4GB)
//   - central directory record = 46 + name + 9 (+28 zip64 extra when the file
//     is zip64 OR its local-header offset >= 4GB)
//   - EOCD = 22 (+76 zip64 EOCD+locator when records>=65535 or cd size/offset
//     >= 4GB)
//
// Verified byte-for-byte against calculateZipSizeLegacy by
// TestCalculateZipSizeMatchesLegacy. Replaces the old zip.Writer simulation
// which forced a CRC32 pass over all bytes (6-11s on a 10.5GB album).
func calculateZipSize(files []FileEntry, opts ZipWriteOptions) int64 {
	const (
		fileHeaderLen       = 30
		directoryHeaderLen  = 46
		directoryEndLen     = 22
		dataDescriptorLen   = 16
		dataDescriptor64Len = 24
		directory64EndLen   = 56
		directory64LocLen   = 20
		extTimestampExtra   = 9  // extended-timestamp extra field
		zip64ExtraInCD      = 28 // zip64 extra appended in central directory record
		manifestOverhead    = 256
		uint32max           = 1<<32 - 1
		uint16max           = 1<<16 - 1
	)

	seen := make(map[string]int)
	var localSum, cdSum, offset int64

	for _, f := range files {
		name := safeZipName(f.Path)
		if opts.FlatMode {
			name = dedupName(flatZipName(f.Path), seen)
		}
		nameLen := int64(len(name))
		isFileZip64 := uint64(f.Size) > uint32max

		// local file header (30 + name + 9) + Store data + data descriptor
		dd := int64(dataDescriptorLen)
		if isFileZip64 {
			dd = dataDescriptor64Len
		}
		entry := int64(fileHeaderLen) + nameLen + extTimestampExtra + f.Size + dd
		localSum += entry

		// central directory record (46 + name + 9 [+ 28 zip64 extra])
		extraCD := int64(extTimestampExtra)
		if isFileZip64 || uint64(offset) >= uint32max {
			extraCD += zip64ExtraInCD
		}
		cdSum += int64(directoryHeaderLen) + nameLen + extraCD

		offset += entry
	}

	total := localSum + cdSum + directoryEndLen
	if uint64(len(files)) >= uint16max || uint64(cdSum) >= uint32max || uint64(localSum) >= uint32max {
		total += directory64EndLen + directory64LocLen
	}

	if opts.EmitManifest {
		total += storage.ManifestReservedSize(len(files)) + manifestOverhead
	}
	return total
}

// calculateZipSizeLegacy estimates ZIP size by actually running a zip.Writer
// over fake (zero) data. Byte-exact but O(total bytes) because archive/zip
// forces a CRC32 pass over every written byte. Retained as the oracle for
// TestCalculateZipSizeMatchesLegacy; NOT used in production (the math-based
// calculateZipSize above is O(num files)).
func calculateZipSizeLegacy(files []FileEntry, opts ZipWriteOptions) int64 {
	cw := &countWriter{}
	zw := zip.NewWriter(cw)
	seen := make(map[string]int)
	for _, f := range files {
		// Free path: keep original filenames (SmartRename=false) or flatten
		// (FlatMode=true). No HEIC conversion / Live Photo splitting — files
		// are counted by their original bytes in the size estimate.
		name := safeZipName(f.Path)
		if opts.FlatMode {
			name = dedupName(flatZipName(f.Path), seen)
		}
		hdr := &zip.FileHeader{
			Name:               name,
			Method:             zip.Store,
			UncompressedSize64: uint64(f.Size),
			Modified:           time.Unix(f.ModTime, 0),
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			continue
		}
		if f.Size > 0 {
			io.CopyN(fw, zeroReader{}, f.Size)
		}
	}
	zw.Close()
	size := cw.n

	if opts.EmitManifest {
		// ZIP overhead of manifest.json = local file header (~65: 30 fixed + 13
		// filename + extra/data descriptor) + central directory record (~46 + 13
		// filename). For large archives (>4GB) that central record also carries a
		// zip64 extra (~28), totaling up to ~165. 256 is used as a safe upper
		// bound; the excess is zero-padded by padToZipSize (far smaller than the
		// EOCD 64KB back-scan window, so Windows/7-Zip extraction is unaffected).
		manifestPayload := storage.ManifestReservedSize(len(files))
		manifestEntryOverhead := int64(256)
		size += manifestPayload + manifestEntryOverhead
	}

	return size
}

func isHeicExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".heic" || ext == ".heif"
}

// mediaHTTPClient is shared across all thumbnail / restricted file fetches.
var mediaHTTPClient = &http.Client{
	Timeout: 300 * time.Second,
}

// trackingWriter wraps an io.Writer, counting bytes and updating batch progress in real-time.
type trackingWriter struct {
	w        io.Writer
	n        int64
	progress *batchProgress
	curFile  string
}

func (tw *trackingWriter) Write(p []byte) (int, error) {
	n, err := tw.w.Write(p)
	tw.n += int64(n)
	tw.progress.update(tw.n, tw.curFile)
	return n, err
}

// copyWithCtx copies from src to dst in chunks, checking ctx between reads.
func copyWithCtx(dst io.Writer, src io.Reader, buf []byte, ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// writeBatchZip streams a ZIP archive of the batch's files to w.
// Updates progress via the global progress tracker in real-time (per Write call).
// Set trackProgress=false to skip progress registration.
// cancelFn is called on stall timeout (client not reading for 60s) to abort the ZIP.
func writeBatchZip(w io.Writer, batch *Batch, mediaPort int, zipSize int64, opts ZipWriteOptions, trackProgress bool, ctx context.Context, cancelFn context.CancelFunc) error {
	progress := &batchProgress{
		total: zipSize,
		files: len(batch.Files),
	}
	if trackProgress {
		setBatchProgress(batch.ID, progress)
	}

	// Periodically print progress to stdout for Android app to parse
	if trackProgress {
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			var lastSent int64 = -1
			stallCount := 0
			for {
				select {
				case <-ticker.C:
					sent, t, files, _, _, curFile := progress.snapshot()
					if sent == lastSent && lastSent >= 0 && sent > 0 {
						stallCount++
						if stallCount >= 2 {
							log.Printf("PAUSED:%s", batch.ID)
						}
						if stallCount >= 60 {
							log.Printf("STALL_TIMEOUT:%s (60s no progress, auto-cancel)", batch.ID)
							if cancelFn != nil {
								cancelFn()
							}
							return
						}
					} else {
						stallCount = 0
					}
					lastSent = sent
					pct := int64(0)
					if t > 0 {
						pct = sent * 100 / t
					}
					log.Printf("PROGRESS:%s %d %d %d %d %s", batch.ID, sent, t, pct, files, filepath.Base(curFile))
				case <-done:
					return
				}
			}
		}()
		defer close(done)
	}

	tw := &trackingWriter{w: w, progress: progress}
	zw := zip.NewWriter(tw)

	seen := make(map[string]int)
	// manifest accumulates per-file SHA-256 + original_path for the trailing
	// manifest.json entry. Allocation is conditional on EmitManifest so
	// callers that don't need verification pay zero overhead.
	var manifest []storage.ManifestEntry
	if opts.EmitManifest {
		manifest = make([]storage.ManifestEntry, 0, len(batch.Files))
	}
	for _, f := range batch.Files {
		select {
		case <-ctx.Done():
			zw.Close()
			if trackProgress {
				progress.cancel()
				log.Printf("CANCELLED:%s", batch.ID)
			}
			return ctx.Err()
		default:
		}
		tw.curFile = f.Path
		entry, err := writeFileToZip(zw, f, mediaPort, opts, seen, ctx)
		if err != nil {
			// ErrSkipFile = open-stage failure (no ZIP entry opened yet).
			// Single-zip-trust-tcp: we trust TCP integrity and do NOT
			// track failed files for retry. Just log + continue.
			if errors.Is(err, ErrSkipFile) {
				log.Printf("SKIP:%s %s %s", batch.ID, f.Path, err.Error())
				continue
			}
			// Unrecoverable: bytes already streamed, archive is poisoned.
			zw.Close()
			if trackProgress {
				progress.cancel()
				log.Printf("CANCELLED:%s", batch.ID)
			}
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		if opts.EmitManifest && entry.Path != "" {
			manifest = append(manifest, entry)
		}
	}

	// manifest.json tail entry. Padded with spaces to the pre-reserved byte
	// budget so Content-Length (declared by calculateZipSize) stays exact.
	// Free + Pro both emit (single-zip-trust-tcp §1.2.6: verify.js is a free
	// tool that reads the manifest).
	// Method=Store (consistent with other entries): verify.js readZipEntry only
	// supports Store; Deflate by default would hand the front-end a compressed
	// stream instead of JSON, causing verify to fail.
	if opts.EmitManifest {
		mh := &zip.FileHeader{
			Name:   "manifest.json",
			Method: zip.Store,
		}
		mw, merr := zw.CreateHeader(mh)
		if merr != nil {
			return fmt.Errorf("create manifest entry: %w", merr)
		}
		payload := storage.ManifestPayload{
			Schema:  storage.ManifestSchemaVersion,
			Session: opts.SessionID,
			BatchID: opts.BatchID,
			Files:   manifest,
		}
		reserved := storage.ManifestReservedSize(len(manifest))
		if _, werr := storage.WriteManifest(mw, payload, reserved); werr != nil {
			zw.Close()
			return fmt.Errorf("write manifest: %w", werr)
		}
	}

	tw.curFile = ""
	err := zw.Close()
	if trackProgress {
		if err != nil {
			progress.cancel()
			log.Printf("CANCELLED:%s", batch.ID)
		} else {
			progress.finish()
			sent, _, _, _, _, _ := progress.snapshot()
			log.Printf("DONE:%s %d", batch.ID, sent)
		}
	}
	return err
}

// dedupName ensures unique names in flat mode by appending _1, _2, etc.
// Keeps incrementing until an unused name is found to avoid collisions
// with pre-existing files like "photo_1.jpg".
func dedupName(base string, seen map[string]int) string {
	if _, ok := seen[base]; !ok {
		seen[base] = 0
		return base
	}
	seen[base]++
	ext := filepath.Ext(base)
	nameNoExt := strings.TrimSuffix(base, ext)
	for n := seen[base]; ; n++ {
		candidate := fmt.Sprintf("%s_%d%s", nameNoExt, n, ext)
		if _, exists := seen[candidate]; !exists {
			seen[candidate] = 0
			seen[base] = n
			return candidate
		}
	}
}

// writeFileToZip packs one file into zw. It returns a ManifestEntry capturing
// the path, size, and SHA-256 of the bytes written so callers (writeBatchZip)
// can append a manifest.json tail entry for Free verify.js integrity checks.
//
// Free path: keep original filenames (SmartRename=false) or flatten
// (FlatMode=true); original bytes are written as-is (no HEIC conversion /
// no EXIF stripping / no Live Photo splitting). SHA-256 is computed
// streaming via MultiWriter for verify.js byte-level checks.
func writeFileToZip(zw *zip.Writer, f FileEntry, mediaPort int, opts ZipWriteOptions, seen map[string]int, ctx context.Context) (storage.ManifestEntry, error) {
	zipName := safeZipName(f.Path)
	if opts.FlatMode {
		zipName = dedupName(flatZipName(f.Path), seen)
	}

	// RetryOpen MUST come before CreateHeader: once the ZIP entry is opened,
	// its bytes are streamed and cannot be taken back. Open-stage retry
	// covers ~99% of transient Android failures (flash spin-up, iCloud cache
	// miss) without polluting the archive (design decision §9.6).
	file, err := archiver.RetryOpen(f.FullPath, 3, archiver.DefaultRetryDelays)
	if err != nil {
		// Wrap with ErrSkipFile so writeBatchZip can log + skip this file
		// instead of poisoning the whole archive.
		return storage.ManifestEntry{}, fmt.Errorf("%w: retry open: %v", ErrSkipFile, err)
	}
	defer file.Close()

	hdr := &zip.FileHeader{
		Name:               zipName,
		Method:             zip.Store,
		UncompressedSize64: uint64(f.Size),
		Modified:           time.Unix(f.ModTime, 0),
	}
	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		return storage.ManifestEntry{}, err
	}

	// SHA-256 streamed via MultiWriter alongside the ZIP entry. copyWithCtx
	// preserves cancellation semantics (SSE PAUSED on stall).
	hasher := sha256.New()
	mw := io.MultiWriter(fw, hasher)
	buf := make([]byte, 256*1024)
	if err := copyWithCtx(mw, file, buf, ctx); err != nil {
		return storage.ManifestEntry{}, err
	}
	return storage.ManifestEntry{
		Path:         zipName,
		OriginalPath: f.FullPath,
		Converted:    false,
		Size:         f.Size,
		Sha256:       hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}
