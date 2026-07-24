package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"photosmove/storage"
)

// writeTestFile writes data to a fresh tmpdir + path and returns the full path.
func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	return full
}

// TestWriteBatchZipEmitManifestAppendsTailEntry verifies that EmitManifest
// produces a trailing manifest.json entry whose SHA-256 matches the actual
// extracted file bytes.
func TestWriteBatchZipEmitManifestAppendsTailEntry(t *testing.T) {
	content1 := bytes.Repeat([]byte{'A'}, 1024)
	content2 := bytes.Repeat([]byte{'B'}, 2048)
	p1 := writeTestFile(t, "a.jpg", content1)
	p2 := writeTestFile(t, "b.jpg", content2)
	batch := &Batch{
		ID: "test-batch",
		Files: []FileEntry{
			{Path: "a.jpg", FullPath: p1, Size: int64(len(content1))},
			{Path: "b.jpg", FullPath: p2, Size: int64(len(content2))},
		},
	}

	opts := ZipWriteOptions{EmitManifest: true, SessionID: "sess-xyz", BatchID: batch.ID}
	zipSize := calculateZipSize(batch.Files, opts)

	var buf bytes.Buffer
	if err := writeBatchZip(&buf, batch, 0, zipSize, opts, false, context.Background(), nil); err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	entries := map[string]*zip.File{}
	var manifestFile *zip.File
	for _, f := range zr.File {
		entries[f.Name] = f
		if f.Name == "manifest.json" {
			manifestFile = f
		}
	}
	if manifestFile == nil {
		t.Fatalf("manifest.json entry missing; entries=%v", entries)
	}
	rc, err := manifestFile.Open()
	if err != nil {
		t.Fatalf("manifest Open: %v", err)
	}
	raw, _ := io.ReadAll(rc)
	rc.Close()
	trimmed := bytes.TrimRight(raw, " ")

	var payload storage.ManifestPayload
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		t.Fatalf("manifest Unmarshal: %v", err)
	}
	if payload.Schema != storage.ManifestSchemaVersion {
		t.Errorf("schema=%d want %d", payload.Schema, storage.ManifestSchemaVersion)
	}
	if payload.Session != "sess-xyz" {
		t.Errorf("session=%q want sess-xyz", payload.Session)
	}
	if len(payload.Files) != 2 {
		t.Fatalf("files len=%d want 2", len(payload.Files))
	}

	for _, me := range payload.Files {
		zf, ok := entries[me.Path]
		if !ok {
			t.Errorf("manifest path %q not in ZIP", me.Path)
			continue
		}
		frc, err := zf.Open()
		if err != nil {
			t.Fatalf("open %s: %v", me.Path, err)
		}
		got, _ := io.ReadAll(frc)
		frc.Close()
		h := sha256.Sum256(got)
		wantSHA := hex.EncodeToString(h[:])
		if me.Sha256 != wantSHA {
			t.Errorf("entry %s sha mismatch: manifest=%s actual=%s", me.Path, me.Sha256, wantSHA)
		}
	}
}

// TestCalculateZipSizeIncludesManifestReserve ensures the size returned with
// EmitManifest is base + manifestReserved + entry overhead so the browser
// progress bar stays accurate (iron rule 2).
func TestCalculateZipSizeIncludesManifestReserve(t *testing.T) {
	content1 := bytes.Repeat([]byte{'X'}, 512)
	p1 := writeTestFile(t, "a.jpg", content1)
	files := []FileEntry{{Path: "a.jpg", FullPath: p1, Size: int64(len(content1))}}

	base := calculateZipSize(files, ZipWriteOptions{})
	withManifest := calculateZipSize(files, ZipWriteOptions{EmitManifest: true})
	delta := withManifest - base
	want := storage.ManifestReservedSize(len(files)) + 256
	if delta != want {
		t.Errorf("delta=%d want %d (manifestReserved=%d + 256 overhead)",
			delta, want, storage.ManifestReservedSize(len(files)))
	}
}

// TestCalculateZipSizeCeilingSmartRenameEXIF is a regression test for TWO bugs
// that pull calculateZipSize in opposite directions:
//
//  1. "finished then restarted" (Content-Length overflow): writeFileToZip renames by EXIF
//     capture time and dedups burst shots to _1/_2/..., while calculateZipSize
//     must NOT read EXIF (see bug 2). If calculateZipSize under-estimates the
//     dedup suffix length, the real ZIP overruns Content-Length near 100% → Go
//     aborts → browser re-downloads from 0.
//
//  2. "clicked download all but the browser did nothing" (blocked before response): a prior fix made calculateZipSize
//     call smartRenameEntry, which opens + reads 64KB per file. calculateZipSize
//     runs synchronously BEFORE handleBatch flushes the first byte, so 737 files
//     = 30-60s of pure disk I/O with the browser seeing nothing.
//
// Resolution: calculateZipSize uses ModTime (zero I/O) + a per-file dedupPad
// suffix (8 underscores) as slack for the EXIF dedup suffixes the real write
// path produces. This test copies ONE real-EXIF JPEG into N entries (identical
// EXIF second → N-way dedup in the write path) with DISTINCT ModTimes (no
// dedup on the estimate side), then asserts calculateZipSize >= the bytes
// writeBatchZip actually writes (iron rule 2: Content-Length must be a reliable
// upper bound) AND that the slack stays tight (no per-file blow-up).
func TestCalculateZipSizeCeilingSmartRenameEXIF(t *testing.T) {
	src := "../testdata/Download/30ae0ca2538d2309.jpg" // carries EXIF DateTimeOriginal
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("EXIF fixture unavailable: %v", err)
	}
	// EXIF APP1 sits at the file head and smartRenameEntry only reads 64KB, so a
	// 64KB slice keeps the capture timestamp parseable while keeping the test ZIP
	// small. Enough copies that the EXIF-collision dedup suffixes (_1.._79) far
	// exceed the manifest slack — a handful of files would be masked by it.
	if len(data) > 64*1024 {
		data = data[:64*1024]
	}

	dir := t.TempDir()
	var files []FileEntry
	for i := 0; i < 80; i++ {
		p := filepath.Join(dir, fmt.Sprintf("IMG_%04d.jpg", i))
		if werr := os.WriteFile(p, data, 0644); werr != nil {
			t.Fatal(werr)
		}
		files = append(files, FileEntry{
			Path:     fmt.Sprintf("IMG_%04d.jpg", i),
			FullPath: p,
			Size:     int64(len(data)),
			ModTime:  1000000 + int64(i)*60, // distinct seconds → no ModTime-side dedup
		})
	}

	opts := ZipWriteOptions{SmartRename: true, EmitManifest: true, BatchID: "b", SessionID: "s"}
	declared := calculateZipSize(files, opts)

	batch := &Batch{ID: "b", Files: append([]FileEntry(nil), files...)}
	var buf bytes.Buffer
	if err := writeBatchZip(&buf, batch, 0, declared, opts, false, context.Background(), nil); err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}
	actual := int64(buf.Len())

	if actual > declared {
		t.Fatalf("real ZIP %d > declared Content-Length %d (over by %d) — would truncate the HTTP response and force the browser to re-download from 0",
			actual, declared, actual-declared)
	}
	// Ceiling must stay tight: only the manifest entry + a few padding bytes,
	// never a per-file blow-up (would bloat trailing zero-padding past the EOCD
	// scan window and break Windows/7-Zip extraction).
	if slack := declared - actual; slack > int64(len(files))*64+512 {
		t.Errorf("over-estimate too loose: declared=%d actual=%d slack=%d", declared, actual, slack)
	}
}

// TestWriteBatchZipNoManifestByDefault confirms Free mode preserves
// pre-Pro behaviour: no manifest.json entry.
func TestWriteBatchZipNoManifestByDefault(t *testing.T) {
	content := bytes.Repeat([]byte{'Z'}, 256)
	p := writeTestFile(t, "x.jpg", content)
	batch := &Batch{
		ID:    "test-batch",
		Files: []FileEntry{{Path: "x.jpg", FullPath: p, Size: int64(len(content))}},
	}

	opts := ZipWriteOptions{} // EmitManifest=false
	zipSize := calculateZipSize(batch.Files, opts)

	var buf bytes.Buffer
	if err := writeBatchZip(&buf, batch, 0, zipSize, opts, false, context.Background(), nil); err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			t.Errorf("manifest.json present in Free mode; should be absent")
		}
	}
}

// TestWriteBatchZipSkipFileLogsAndContinues verifies that an open-stage
// failure (here: missing file) is logged + skipped rather than aborting the
// whole archive. single-zip-trust-tcp §1.4.1: failedList no longer exists,
// so the manifest only carries the successfully packed files.
func TestWriteBatchZipSkipFileLogsAndContinues(t *testing.T) {
	content := bytes.Repeat([]byte{'G'}, 1024)
	goodPath := writeTestFile(t, "good.jpg", content)
	missingPath := filepath.Join(filepath.Dir(goodPath), "missing.jpg")

	batch := &Batch{
		ID: "test-batch",
		Files: []FileEntry{
			{Path: "good.jpg", FullPath: goodPath, Size: int64(len(content))},
			{Path: "missing.jpg", FullPath: missingPath, Size: 0},
		},
	}
	opts := ZipWriteOptions{EmitManifest: true, BatchID: batch.ID}
	zipSize := calculateZipSize(batch.Files, opts)

	var buf bytes.Buffer
	if err := writeBatchZip(&buf, batch, 0, zipSize, opts, false, context.Background(), nil); err != nil {
		t.Fatalf("writeBatchZip should not return error for skip-able failure: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	var manifestFile *zip.File
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
		if f.Name == "manifest.json" {
			manifestFile = f
		}
	}
	if manifestFile == nil {
		t.Fatal("manifest.json missing")
	}
	rc, _ := manifestFile.Open()
	raw, _ := io.ReadAll(rc)
	rc.Close()

	var payload storage.ManifestPayload
	if err := json.Unmarshal(bytes.TrimRight(raw, " "), &payload); err != nil {
		t.Fatalf("manifest Unmarshal: %v", err)
	}

	// Manifest carries ONLY the successfully packed good.jpg — missing.jpg
	// was skipped silently (single-zip-trust-tcp §1.4.1).
	if len(payload.Files) != 1 {
		t.Fatalf("files len=%d want 1 (good.jpg only; missing.jpg skipped)", len(payload.Files))
	}
	if payload.Files[0].Path != "good.jpg" {
		t.Errorf("files[0].Path=%q want \"good.jpg\"", payload.Files[0].Path)
	}
	// ZIP body must NOT contain missing.jpg (it was skipped before opening).
	if _, present := entries["missing.jpg"]; present {
		t.Errorf("missing.jpg leaked into ZIP body — should have been skipped")
	}
}

// TestErrSkipFileIsComparable confirms errors.Is(err, ErrSkipFile) works for
// wrapped skip errors. This is the contract writeBatchZip relies on.
func TestErrSkipFileIsComparable(t *testing.T) {
	wrapped := fmt.Errorf("%w: retry open: permission denied", ErrSkipFile)
	if !errors.Is(wrapped, ErrSkipFile) {
		t.Errorf("errors.Is should match wrapped ErrSkipFile")
	}

	plain := errors.New("unrelated error")
	if errors.Is(plain, ErrSkipFile) {
		t.Errorf("errors.Is should NOT match unrelated error")
	}
}
