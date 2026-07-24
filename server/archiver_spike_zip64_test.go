// S-1 Spike: ZIP64 on-device verification (locally runnable version)
//
// Source: openspec/changes/big-video-no-split/spikes/zip64-spike-draft_test.go
// Goal: verify whether Go archive/zip needs explicit ZIP64 enabling in three scenarios
//
// How to run:
//   go test -run TestSpikeZip64 -v -timeout 600s          # all (needs 11GB disk)
//   go test -run TestSpikeZip64 -v -short -timeout 60s    # file-count scenario only
//
// Three scenarios:
//   1. single file > 4GB     (DJI 4K 5GB)         - needs 5GB disk
//   2. accumulated > 4GB     (6 × 1GB videos)     - needs 6GB disk
//   3. file count > 65535    (70000 small photos) - needs ~100MB, slow

package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// zip32SizeSentinel is the ZIP32 size field sentinel value, triggering the
// extractor to read the ZIP64 extra field
const zip32SizeSentinel = 0xFFFFFFFF

// createHeaderZip64 is the core snippet of photosmove's modified writeFileToZip
// (the S-1 Spike verification target)
//
// Key: pre-fill UncompressedSize64; set the 32-bit size to the sentinel when
// over 4GB. Verify: whether Go automatically adds the ZIP64 extra field
// (Local Header + Central Directory).
func createHeaderZip64(zw *zip.Writer, name string, size int64) (io.Writer, error) {
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	hdr.SetMode(0644)
	hdr.UncompressedSize64 = uint64(size)
	if uint64(size) > zip32SizeSentinel {
		hdr.UncompressedSize = zip32SizeSentinel
	} else {
		hdr.UncompressedSize = uint32(size)
	}
	return zw.CreateHeader(hdr)
}

// TestSpikeZip64_SingleFileOver4GB scenario 1: single 5GB file
// Verifies both ZIP Local Header + Central Directory carry the ZIP64 extra field
func TestSpikeZip64_SingleFileOver4GB(t *testing.T) {
	if testing.Short() {
		t.Skip("needs 5GB temp disk space, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "big.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024 // 5GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip file: %v", err)
	}
	zw := zip.NewWriter(fw)

	w, err := createHeaderZip64(zw, "dji_4k_flight.mp4", fileSize)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}

	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Fatalf("Copy 5GB: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (ZIP64 decode failure means Go did not auto-enable it): %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Fatalf("expect 1 entry, got %d", len(zr.File))
	}
	entry := zr.File[0]
	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Errorf("UncompressedSize64 = %d, want %d (means the 64-bit size was not passed correctly)",
			entry.UncompressedSize64, fileSize)
	}

	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("entry Open: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != fileSize {
		t.Errorf("read back bytes = %d, want %d (data misalignment = ZIP32 overflow)", n, fileSize)
	}
	t.Logf("S-1 scenario 1 PASS: 5GB single file ZIP64 correctly enabled, read back %d bytes", n)
}

// TestSpikeZip64_AccumulatedOver4GB scenario 2: accumulated 6 × 1GB = 6GB
// Verifies Central Directory ZIP64 is enabled (offset > uint32max)
func TestSpikeZip64_AccumulatedOver4GB(t *testing.T) {
	if testing.Short() {
		t.Skip("needs 6GB temp disk space, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "multi.zip")
	const fileCount = 6
	const eachSize int64 = 1024 * 1024 * 1024 // 1GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	for i := 0; i < fileCount; i++ {
		name := "video_" + string(rune('0'+i)) + ".mp4"
		w, err := createHeaderZip64(zw, name, eachSize)
		if err != nil {
			t.Fatalf("CreateHeader %d: %v", i, err)
		}
		if _, err := io.Copy(w, newSpikeZeroReader(eachSize)); err != nil {
			t.Fatalf("Copy %d: %v", i, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer zr.Close()
	if len(zr.File) != fileCount {
		t.Fatalf("expect %d entries, got %d", fileCount, len(zr.File))
	}
	t.Logf("S-1 scenario 2 PASS: 6GB accumulated ZIP64 central directory correctly enabled, %d entries", len(zr.File))
}

// TestSpikeZip64_FileCountOver65535 scenario 3: 70000 1KB files
// Verifies Central Directory ZIP64 is enabled (count > uint16max)
// Does not need a large disk, but creating 70000 entries is slow
func TestSpikeZip64_FileCountOver65535(t *testing.T) {
	if testing.Short() {
		t.Skip("creating 70000 entries is slow, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "many.zip")
	const fileCount = 70000

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	for i := 0; i < fileCount; i++ {
		// Generate non-duplicate filenames (avoid name collisions)
		name := "img_" + spikeItoa(i) + ".jpg"
		w, err := createHeaderZip64(zw, name, 1024)
		if err != nil {
			t.Fatalf("CreateHeader %d: %v", i, err)
		}
		if _, err := w.Write(make([]byte, 1024)); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v (if this fails, central directory ZIP64 was not enabled)", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (central directory ZIP64 decode failure): %v", err)
	}
	defer zr.Close()
	if len(zr.File) != fileCount {
		t.Errorf("entry count = %d, want %d", len(zr.File), fileCount)
	}
	t.Logf("S-1 scenario 3 PASS: %d files, ZIP64 central directory correctly enabled", len(zr.File))
}

// TestSpikeZip64_ControlGroup control group: use default Create(name) without
// pre-filling size. Verifies Go's default behavior without explicit ZIP64
// (should fail or misalign data)
func TestSpikeZip64_ControlGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("control group needs 5GB, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "control.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	// Control group: default Create, no pre-filled 64-bit size
	w, err := zw.Create("control.mp4")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write 5GB (Go computes size internally on Close, but streaming scenarios may fail)
	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Logf("control group failed as expected (Create mode does not support large files): %v", err)
		return
	}

	if err := zw.Close(); err != nil {
		t.Logf("control group Close failed (expected): %v", err)
		return
	}
	fw.Close()

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Logf("control group OpenReader failed (expected, ZIP32 overflow): %v", err)
		return
	}
	defer zr.Close()

	entry := zr.File[0]
	t.Logf("control group entry.UncompressedSize64 = %d (actual 5GB = %d), match=%v",
		entry.UncompressedSize64, fileSize, entry.UncompressedSize64 == uint64(fileSize))

	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Logf("control group misaligned as expected: Go default Create mode gives wrong size for a 5GB file, proving the createHeaderZip64 change is necessary")
	}
}

// TestSpikeZip64_NonSeekableWriter key scenario: simulate an HTTP ResponseWriter
// (non-seekable). photosmove's real scenario: zip.Writer sits on top of
// http.ResponseWriter, which cannot Seek.
// Verify: whether Go still auto-enables ZIP64 on a non-seekable writer.
//
// This test is the "real" verification of the S-1 Spike (the first 4 use
// os.File Seeker and go through the seek-and-update path)
func TestSpikeZip64_NonSeekableWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("non-seekable 5GB test, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "nonseekable.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024 // 5GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Wrap as a non-seekable writer to simulate http.ResponseWriter
	nw := newNonSeekableWriter(fw)

	zw := zip.NewWriter(nw)

	// No pre-filled size (control group: simulating photosmove's current state)
	w, err := zw.Create("non_seekable.mp4")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v (if this fails, non-seekable mode does not support large files)", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (non-seekable ZIP64 decode failure): %v", err)
	}
	defer zr.Close()

	entry := zr.File[0]
	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Errorf("UncompressedSize64 = %d, want %d", entry.UncompressedSize64, fileSize)
	}

	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("entry Open: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != fileSize {
		t.Errorf("read back = %d, want %d", n, fileSize)
	}
	t.Logf("S-1 non-seekable PASS: Go still auto-enables ZIP64 on a non-seekable writer, read back %d bytes", n)
	t.Logf("  → meaning photosmove does not need to explicitly pre-fill UncompressedSize64; Go handles data descriptor + ZIP64 automatically")
}

// nonSeekableWriter wraps an io.Writer, hiding the Seek method to simulate
// http.ResponseWriter
type nonSeekableWriter struct {
	w io.Writer
}

func newNonSeekableWriter(w io.Writer) *nonSeekableWriter {
	return &nonSeekableWriter{w: w}
}

func (n *nonSeekableWriter) Write(p []byte) (int, error) { return n.w.Write(p) }

// spikeZeroReader streams N zero bytes without consuming memory
type spikeZeroReader struct{ remaining int64 }

func newSpikeZeroReader(n int64) *spikeZeroReader { return &spikeZeroReader{remaining: n} }

func (z *spikeZeroReader) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	give := int64(len(p))
	if give > z.remaining {
		give = z.remaining
	}
	for i := int64(0); i < give; i++ {
		p[i] = 0
	}
	z.remaining -= give
	return int(give), nil
}

// spikeItoa is a simplified itoa, avoiding the strconv import (fewer deps for the demo)
func spikeItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
