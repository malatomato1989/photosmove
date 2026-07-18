package main

import (
	"strings"
	"testing"
	"time"
)

// makeSingleFile builds a FileEntry for planSingleBatch tests.
func makeSingleFile(name string, size int64, modtime time.Time) FileEntry {
	return FileEntry{
		Path:     name,
		FullPath: "/tmp/" + name,
		Size:     size,
		ModTime:  modtime.Unix(),
	}
}

// TestPlanSingleBatch_EmptyFiles_ReturnsNil confirms nil input never panics
// and yields a nil batch (callers must treat nil as 0 batches).
func TestPlanSingleBatch_EmptyFiles_ReturnsNil(t *testing.T) {
	if got := planSingleBatch(nil, BatchOpts{}); got != nil {
		t.Errorf("nil input: got %+v, want nil", got)
	}
	if got := planSingleBatch([]FileEntry{}, BatchOpts{}); got != nil {
		t.Errorf("empty input: got %+v, want nil", got)
	}
}

// TestPlanSingleBatch_OrdinaryFiles_ProducesOneBatch confirms a mixed slice
// collapses to a single photos_<ts> batch.
func TestPlanSingleBatch_OrdinaryFiles_ProducesOneBatch(t *testing.T) {
	files := []FileEntry{
		// 200 MB total so EstimatedUSBSeconds (= total/80MB) > 0.
		makeSingleFile("a.jpg", 100*1024*1024, time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)),
		makeSingleFile("b.mp4", 100*1024*1024, time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC)),
	}
	b := planSingleBatch(files, BatchOpts{AlbumName: "ignored"})
	if b == nil {
		t.Fatal("expected non-nil batch")
	}
	if !strings.HasPrefix(b.ID, "photos_") {
		t.Errorf("ID=%q, want prefix \"photos_\"", b.ID)
	}
	// Timestamp portion must be a unix-seconds integer.
	ts := strings.TrimPrefix(b.ID, "photos_")
	if _, err := time.Parse(time.UnixDate, ts); err == nil {
		t.Errorf("unexpected UnixDate match for %q", ts)
	}
	if b.AlbumName != "photos" {
		t.Errorf("AlbumName=%q want \"photos\" (fixed single-batch stem)", b.AlbumName)
	}
	if len(b.Files) != 2 {
		t.Errorf("files len=%d want 2", len(b.Files))
	}
	wantTotal := int64(200 * 1024 * 1024)
	if b.TotalSize != wantTotal {
		t.Errorf("TotalSize=%d want %d", b.TotalSize, wantTotal)
	}
	if b.EstimatedWiFiSeconds <= 0 || b.EstimatedUSBSeconds <= 0 {
		t.Errorf("estimates must be >0: wifi=%d usb=%d", b.EstimatedWiFiSeconds, b.EstimatedUSBSeconds)
	}
	// No file crosses BigFileThreshold so BigFile should be false.
	if b.BigFile {
		t.Errorf("BigFile=true for ordinary files, want false")
	}
}

// TestPlanSingleBatch_BigFileHighlightsBiggest confirms a >1GB file flips
// BigFile and BiggestFile points at the largest file.
func TestPlanSingleBatch_BigFileHighlightsBiggest(t *testing.T) {
	big := int64(2*1024*1024*1024 + 100) // 2GB+100B, well above the 1GB threshold
	files := []FileEntry{
		makeSingleFile("small.jpg", 50_000, time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)),
		makeSingleFile("huge.mp4", big, time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC)),
		makeSingleFile("medium.jpg", 5_000_000, time.Date(2024, 6, 3, 12, 0, 0, 0, time.UTC)),
	}
	b := planSingleBatch(files, BatchOpts{})
	if b == nil {
		t.Fatal("expected non-nil batch")
	}
	if !b.BigFile {
		t.Errorf("BigFile=false want true (largest file > 1GB)")
	}
	if b.BiggestFile == nil {
		t.Fatal("BiggestFile=nil want pointer to largest file")
	}
	if b.BiggestFile.Path != "huge.mp4" {
		t.Errorf("BiggestFile.Path=%q want \"huge.mp4\"", b.BiggestFile.Path)
	}
	if b.BiggestFile.Size != big {
		t.Errorf("BiggestFile.Size=%d want %d", b.BiggestFile.Size, big)
	}
	if b.TotalSize != 50_000+big+5_000_000 {
		t.Errorf("TotalSize=%d want %d", b.TotalSize, 50_000+big+5_000_000)
	}
}

// TestPlanSingleBatch_IDUniquenessAcrossCalls confirms two calls produce
// different IDs (time.Now().Unix() tick boundary is the only shared case
// but with a small sleep it cannot collide).
func TestPlanSingleBatch_IDUniquenessAcrossCalls(t *testing.T) {
	files := []FileEntry{
		makeSingleFile("a.jpg", 100, time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)),
	}
	first := planSingleBatch(files, BatchOpts{})
	time.Sleep(1100 * time.Millisecond) // cross a 1-second tick boundary
	second := planSingleBatch(files, BatchOpts{})
	if first == nil || second == nil {
		t.Fatal("expected non-nil batches")
	}
	if first.ID == second.ID {
		t.Errorf("two calls produced same ID=%q — timestamps must be unique", first.ID)
	}
}

// TestPlanSingleBatch_AlbumNameAlwaysPhotos confirms AlbumName is the fixed
// "photos" regardless of BatchOpts input.
func TestPlanSingleBatch_AlbumNameAlwaysPhotos(t *testing.T) {
	files := []FileEntry{
		makeSingleFile("a.jpg", 100, time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)),
	}
	cases := []BatchOpts{
		{},
		{AlbumName: "Camera"},
		{AlbumName: "Holiday", BatchCeiling: 1 << 30},
	}
	for i, opts := range cases {
		b := planSingleBatch(files, opts)
		if b == nil {
			t.Fatalf("case %d: nil batch", i)
		}
		if b.AlbumName != "photos" {
			t.Errorf("case %d: AlbumName=%q want \"photos\"", i, b.AlbumName)
		}
	}
}
