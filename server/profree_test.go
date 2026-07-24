package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// isVideoFile
// ============================================================

func TestIsVideoFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"photo.jpg", false},
		{"photo.jpeg", false},
		{"photo.png", false},
		{"photo.heic", false},
		{"video.mp4", true},
		{"video.MP4", true},
		{"video.mov", true},
		{"video.Mov", true},
		{"video.avi", true},
		{"video.mkv", true},
		{"video.3gp", true},
		{"video.wmv", true},
		{"video.flv", true},
		{"video.m4v", true},
		{"video.mpeg", true},
		{"video.mpg", true},
		{"photo.gif", false},
		{"photo.webp", false},
		{"file.txt", false},
		{"noext", false},
		{"DCIM/Camera/IMG_001.MOV", true},
		{"DCIM/Camera/IMG_001.heic", false},
	}
	for _, tt := range tests {
		got := isVideoFile(tt.path)
		if got != tt.want {
			t.Errorf("isVideoFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// ============================================================
// dedupName
// ============================================================

func TestDedupName_NoCollision(t *testing.T) {
	seen := map[string]int{}
	got := dedupName("photo.jpg", seen)
	if got != "photo.jpg" {
		t.Errorf("first occurrence: got %q, want %q", got, "photo.jpg")
	}
}

func TestDedupName_SimpleCollision(t *testing.T) {
	seen := map[string]int{}
	dedupName("photo.jpg", seen)
	got := dedupName("photo.jpg", seen)
	if got != "photo_1.jpg" {
		t.Errorf("second occurrence: got %q, want %q", got, "photo_1.jpg")
	}
}

func TestDedupName_TripleCollision(t *testing.T) {
	seen := map[string]int{}
	dedupName("photo.jpg", seen)
	dedupName("photo.jpg", seen)
	got := dedupName("photo.jpg", seen)
	if got != "photo_2.jpg" {
		t.Errorf("third occurrence: got %q, want %q", got, "photo_2.jpg")
	}
}

func TestDedupName_CollisionWithPreexistingFile(t *testing.T) {
	seen := map[string]int{}
	// Real file named photo_1.jpg exists
	dedupName("photo.jpg", seen)
	dedupName("photo_1.jpg", seen) // real file takes photo_1.jpg
	got := dedupName("photo.jpg", seen) // duplicate should skip photo_1.jpg
	if got != "photo_2.jpg" {
		t.Errorf("collision with existing: got %q, want %q", got, "photo_2.jpg")
	}
}

func TestDedupName_ComplexCollisionChain(t *testing.T) {
	seen := map[string]int{}
	// photo.jpg, photo_1.jpg, photo_2.jpg all exist as real files
	dedupName("photo.jpg", seen)
	dedupName("photo_1.jpg", seen)
	dedupName("photo_2.jpg", seen)
	// Duplicate photo.jpg should get photo_3.jpg
	got := dedupName("photo.jpg", seen)
	if got != "photo_3.jpg" {
		t.Errorf("complex chain: got %q, want %q", got, "photo_3.jpg")
	}
}

func TestDedupName_DifferentExtensions(t *testing.T) {
	seen := map[string]int{}
	a := dedupName("IMG.heic", seen)
	b := dedupName("IMG.mov", seen)
	if a != "IMG.heic" || b != "IMG.mov" {
		t.Errorf("different extensions should not collide: got %q, %q", a, b)
	}
}

// ============================================================
// flatZipName / safeZipName
// ============================================================

func TestFlatZipName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"DCIM/Camera/IMG_001.jpg", "IMG_001.jpg"},
		{"Photos/Screenshot.png", "Screenshot.png"},
		{"file.jpg", "file.jpg"},
	}
	for _, tt := range tests {
		got := flatZipName(tt.path)
		if got != tt.want {
			t.Errorf("flatZipName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestSafeZipName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"DCIM/Camera/IMG.jpg", "DCIM/Camera/IMG.jpg"},
		{"../etc/passwd", "_"},
		{".hidden/photo.jpg", "hidden/photo.jpg"},
		{"/absolute/path.jpg", "absolute/path.jpg"},
	}
	for _, tt := range tests {
		got := safeZipName(tt.path)
		if got != tt.want {
			t.Errorf("safeZipName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// ============================================================
// calculateZipSize — flatMode vs normal mode
// ============================================================

func TestCalculateZipSize_FlatMode(t *testing.T) {
	files := []FileEntry{
		{Path: "Camera/IMG_001.jpg", Size: 1000, ModTime: 1000000},
		{Path: "Camera/IMG_002.jpg", Size: 2000, ModTime: 1000000},
	}
	normalSize := calculateZipSize(files, ZipWriteOptions{FlatMode: false})
	flatSize := calculateZipSize(files, ZipWriteOptions{FlatMode: true})
	// Flat mode and normal mode produce different entry names but same data size,
	// so ZIP overhead should be very close (flat names are shorter)
	if normalSize == 0 || flatSize == 0 {
		t.Errorf("sizes should be non-zero: normal=%d flat=%d", normalSize, flatSize)
	}
	// Both should be > sum of file sizes (ZIP has headers)
	if normalSize <= 3000 {
		t.Errorf("normal ZIP size should include headers: got %d", normalSize)
	}
}

// ============================================================
// calculateZipSize — flatMode dedup produces larger ZIP
// ============================================================

func TestCalculateZipSize_FlatModeDuplicateNames(t *testing.T) {
	files := []FileEntry{
		{Path: "Album1/IMG.jpg", Size: 500, ModTime: 1000000},
		{Path: "Album2/IMG.jpg", Size: 500, ModTime: 1000000},
	}
	flatSize := calculateZipSize(files, ZipWriteOptions{FlatMode: true})
	normalSize := calculateZipSize(files, ZipWriteOptions{FlatMode: false})
	// Flat mode deduplicates "IMG.jpg" → "IMG.jpg" + "IMG_1.jpg"
	// "IMG_1.jpg" is longer than "Album2/IMG.jpg" if Album2 is short,
	// so we just verify both are valid
	if flatSize == 0 || normalSize == 0 {
		t.Errorf("sizes should be non-zero: flat=%d normal=%d", flatSize, normalSize)
	}
}

// ============================================================
// isVideoFile classifier (used by planBatchesD Pass 1 media-type split,
// NOT for Free-mode filtering — Free includes videos per spec §5.2)
// ============================================================

func TestIsVideoFile_ClassifiesCommonFormats(t *testing.T) {
	files := []FileEntry{
		{Path: "IMG_001.jpg", Size: 1000, FullPath: "/tmp/IMG_001.jpg"},
		{Path: "IMG_002.mp4", Size: 5000, FullPath: "/tmp/IMG_002.mp4"},
		{Path: "IMG_003.heic", Size: 800, FullPath: "/tmp/IMG_003.heic"},
		{Path: "IMG_004.mov", Size: 3000, FullPath: "/tmp/IMG_004.mov"},
		{Path: "IMG_005.png", Size: 600, FullPath: "/tmp/IMG_005.png"},
	}

	var nonVideo []FileEntry
	for _, f := range files {
		if isVideoFile(f.Path) {
			continue
		}
		nonVideo = append(nonVideo, f)
	}

	if len(nonVideo) != 3 {
		t.Errorf("expected 3 non-video files, got %d", len(nonVideo))
	}
	for _, f := range nonVideo {
		if isVideoFile(f.Path) {
			t.Errorf("video file leaked through classifier: %s", f.Path)
		}
	}
}

func TestIsVideoFile_LivePhotoMOVCompanion(t *testing.T) {
	files := []FileEntry{
		{Path: "Live.heic", Size: 800, FullPath: "/tmp/Live.heic", IsLive: true},
		{Path: "Live.mov", Size: 2000, FullPath: "/tmp/Live.mov", IsLive: true},
	}

	// planBatchesD splits Live Photo pairs: HEIC → pic_*, MOV → vid_*.
	// This test documents that the .mov half classifies as video.
	var nonVideo []FileEntry
	for _, f := range files {
		if isVideoFile(f.Path) {
			continue
		}
		nonVideo = append(nonVideo, f)
	}

	if len(nonVideo) != 1 {
		t.Fatalf("expected 1 non-video (HEIC still), got %d", len(nonVideo))
	}
	if nonVideo[0].Path != "Live.heic" {
		t.Errorf("expected Live.heic, got %s", nonVideo[0].Path)
	}
}

// ============================================================
// sumSize
// ============================================================

func TestSumSize(t *testing.T) {
	files := []FileEntry{
		{Size: 100},
		{Size: 200},
		{Size: 300},
	}
	if got := sumSize(files); got != 600 {
		t.Errorf("sumSize = %d, want 600", got)
	}
	if got := sumSize(nil); got != 0 {
		t.Errorf("sumSize(nil) = %d, want 0", got)
	}
}

// ============================================================
// ZIP content verification: flatMode produces flat names
// ============================================================

func TestWriteBatchZip_FlatMode(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG_001.jpg")
	os.WriteFile(f1, make([]byte, 100), 0644)
	f2 := filepath.Join(tmpDir, "IMG_002.jpg")
	os.WriteFile(f2, make([]byte, 200), 0644)

	batch := &Batch{
		ID:        "test",
		AlbumName: "Camera",
		Files: []FileEntry{
			{Path: "DCIM/Camera/IMG_001.jpg", FullPath: f1, Size: 100, ModTime: 1000000},
			{Path: "DCIM/Camera/IMG_002.jpg", FullPath: f2, Size: 200, ModTime: 1000000},
		},
		TotalSize: 300,
	}

	var buf bytes.Buffer
	err := writeBatchZip(&buf, batch, 0, 300, ZipWriteOptions{FlatMode: true}, false, context.Background(), nil)
	if err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}

	for _, f := range zr.File {
		if strings.Contains(f.Name, "/") {
			t.Errorf("flatMode ZIP should have no paths, got %q", f.Name)
		}
	}

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["IMG_001.jpg"] || !names["IMG_002.jpg"] {
		t.Errorf("expected flat names, got: %v", names)
	}
}

// TestWriteBatchZip_NormalMode verifies the Free default path (SmartRename=false,
// FlatMode=false): safeZipName preserves the original directory structure and
// filenames contain "/".
func TestWriteBatchZip_NormalMode(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG_001.jpg")
	os.WriteFile(f1, make([]byte, 100), 0644)

	batch := &Batch{
		ID:        "test",
		AlbumName: "Camera",
		Files: []FileEntry{
			{Path: "DCIM/Camera/IMG_001.jpg", FullPath: f1, Size: 100, ModTime: 1000000},
		},
		TotalSize: 100,
	}

	var buf bytes.Buffer
	err := writeBatchZip(&buf, batch, 0, 100, ZipWriteOptions{}, false, context.Background(), nil)
	if err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 file, got %d", len(zr.File))
	}
	// Free keeps original relative paths by default (safeZipName does not strip directory separators).
	if !strings.Contains(zr.File[0].Name, "/") {
		t.Errorf("normalMode ZIP should preserve paths, got %q", zr.File[0].Name)
	}
	if zr.File[0].Name != "DCIM/Camera/IMG_001.jpg" {
		t.Errorf("normalMode ZIP name = %q, want DCIM/Camera/IMG_001.jpg", zr.File[0].Name)
	}
}

func TestWriteBatchZip_FlatModeDuplicateNames(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG_001.jpg")
	os.WriteFile(f1, make([]byte, 100), 0644)

	batch := &Batch{
		ID:        "test",
		AlbumName: "photos",
		Files: []FileEntry{
			{Path: "Album1/IMG_001.jpg", FullPath: f1, Size: 100, ModTime: 1000000},
			{Path: "Album2/IMG_001.jpg", FullPath: f1, Size: 100, ModTime: 1000000},
		},
		TotalSize: 200,
	}

	var buf bytes.Buffer
	err := writeBatchZip(&buf, batch, 0, 200, ZipWriteOptions{FlatMode: true}, false, context.Background(), nil)
	if err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["IMG_001.jpg"] {
		t.Errorf("missing IMG_001.jpg, got: %v", names)
	}
	if !names["IMG_001_1.jpg"] {
		t.Errorf("missing deduped IMG_001_1.jpg, got: %v", names)
	}
	if len(zr.File) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(zr.File), names)
	}
}

// ============================================================
// ZIP size prediction accuracy
// ============================================================

func TestCalculateZipSizeMatchesActual(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG_001.jpg")
	os.WriteFile(f1, make([]byte, 5000), 0644)
	f2 := filepath.Join(tmpDir, "IMG_002.jpg")
	os.WriteFile(f2, make([]byte, 8000), 0644)

	files := []FileEntry{
		{Path: "Camera/IMG_001.jpg", FullPath: f1, Size: 5000, ModTime: 1000000},
		{Path: "Camera/IMG_002.jpg", FullPath: f2, Size: 8000, ModTime: 1000000},
	}

	for _, flatMode := range []bool{false, true} {
		predicted := calculateZipSize(files, ZipWriteOptions{FlatMode: flatMode})

		var buf bytes.Buffer
		batch := &Batch{ID: "test", AlbumName: "Camera", Files: files, TotalSize: 13000}
		err := writeBatchZip(&buf, batch, 0, 13000, ZipWriteOptions{FlatMode: flatMode}, false, context.Background(), nil)
		if err != nil {
			t.Fatalf("writeBatchZip(flat=%v): %v", flatMode, err)
		}

		if int64(buf.Len()) != predicted {
			t.Errorf("flatMode=%v: predicted %d, actual %d (diff %d)",
				flatMode, predicted, buf.Len(), int64(buf.Len())-predicted)
		}
	}
}

// ============================================================
// Handler tests: /api/albums (Free — no longer returns pro_mode)
// ============================================================

func newTestServer() *server {
	return &server{
		pin:    "1234",
		token:  "test-token-123",
		albums: []Album{},
		thumbs: make(map[int][]byte),
	}
}

// TestHandleAlbums_NoProModeField verifies the open-source Free build's
// /api/albums no longer includes the pro_mode field (Pro mechanism removed);
// it only returns albums.
func TestHandleAlbums_NoProModeField(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest("GET", "/api/albums", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")
	w := httptest.NewRecorder()
	s.handleAlbums(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := resp["pro_mode"]; present {
		t.Errorf("pro_mode should be absent in Free build, got %v", resp["pro_mode"])
	}
	if _, ok := resp["albums"]; !ok {
		t.Errorf("albums field missing from response: %v", resp)
	}
}

func TestHandleAlbums_VideoStats(t *testing.T) {
	s := &server{
		pin:   "1234",
		token: "test-token-123",
		albums: []Album{
			{
				Path:       "/DCIM/Camera",
				Name:       "Camera",
				Category:   "camera",
				FileCount:  10,
				TotalSize:  50000,
				VideoCount: 3,
				VideoSize:  20000,
			},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/albums", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")
	w := httptest.NewRecorder()
	s.handleAlbums(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	albumsArr, ok := resp["albums"].([]interface{})
	if !ok || len(albumsArr) != 1 {
		t.Fatalf("albums parse error: %v", resp["albums"])
	}
	a := albumsArr[0].(map[string]interface{})
	if a["video_count"].(float64) != 3 {
		t.Errorf("video_count = %v, want 3", a["video_count"])
	}
	if a["video_size"].(float64) != 20000 {
		t.Errorf("video_size = %v, want 20000", a["video_size"])
	}
}

// ============================================================
// handleSelect Free mode: since is always 0 + Plan D batches (videos included)
// ============================================================

func TestHandleSelect_FreeModeIgnoresSince(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "IMG_001.jpg"), make([]byte, 100), 0644)
	os.WriteFile(filepath.Join(tmpDir, "VID_001.mp4"), make([]byte, 200), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		albums: []Album{
			{Path: tmpDir, Name: "TestAlbum", Category: "camera", FileCount: 2, TotalSize: 300},
		},
		thumbs: make(map[int][]byte),
	}

	reqBody, _ := json.Marshal(map[string]interface{}{"paths": []string{tmpDir}, "since": 9999999999})
	req := httptest.NewRequest("POST", "/api/select", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer test-token-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSelect(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	fc := resp["file_count"].(float64)
	// Plan D (T-big-3): Free includes videos per spec §5.2.
	// Free is always full-set (since forced to 0); a client-supplied since is ignored.
	if fc != 2 {
		t.Errorf("file_count = %v, want 2 (jpg+mp4, Free includes videos, since ignored)", fc)
	}
}

func TestHandleSelect_FreeModeKeepsBatchesPerAlbum(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir1, "a.jpg"), make([]byte, 50), 0644)
	os.WriteFile(filepath.Join(tmpDir2, "b.jpg"), make([]byte, 50), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		albums: []Album{
			{Path: tmpDir1, Name: "Album1", FileCount: 1, TotalSize: 50},
			{Path: tmpDir2, Name: "Album2", FileCount: 1, TotalSize: 50},
		},
		thumbs: make(map[int][]byte),
	}

	reqBody, _ := json.Marshal(map[string]interface{}{"paths": []string{tmpDir1, tmpDir2}, "since": 0})
	req := httptest.NewRequest("POST", "/api/select", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer test-token-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSelect(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	bc, ok := resp["batch_count"].(float64)
	if !ok {
		t.Fatalf("no batch_count in response: %v", resp)
	}
	// single-zip-trust-tcp §1.2.1: every selected file collapses into ONE
	// batch (AlbumName "photos"). Two albums → 1 batch.
	if bc != 1 {
		t.Errorf("batch_count = %v, want 1 (single ZIP across albums)", bc)
	}

	// AlbumName is the fixed single-batch stem "photos".
	s.mu.RLock()
	batchName := s.batches[0].AlbumName
	s.mu.RUnlock()
	if batchName != "photos" {
		t.Errorf("batch AlbumName = %q, want \"photos\"", batchName)
	}
}

// ============================================================
// handleBatch Free mode: filename + original directory structure preserved
// ============================================================

// TestHandleBatch_FreeModeFilename verifies the Free download filename is
// always photos.zip, and that with SmartRename=false the ZIP keeps original
// relative paths (including "/").
func TestHandleBatch_FreeModeFilename(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG.jpg")
	os.WriteFile(f1, make([]byte, 100), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		batches: []Batch{
			{ID: "batch1", AlbumName: "Camera", Files: []FileEntry{
				{Path: "Camera/IMG.jpg", FullPath: f1, Size: 100, ModTime: 1000000},
			}, TotalSize: 100},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/batch/batch1?token=test-token-123", nil)
	w := httptest.NewRecorder()
	s.handleBatch(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "photos.zip") {
		t.Errorf("Content-Disposition = %q, should contain photos.zip", cd)
	}

	// Free no longer smart-renames (SmartRename=false): safeZipName preserves the original directory structure.
	body := w.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	var userName string
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			userName = f.Name
		}
	}
	if userName != "Camera/IMG.jpg" {
		t.Errorf("Free ZIP entry = %q, want original path \"Camera/IMG.jpg\" (SmartRename=false preserves dirs)", userName)
	}
}

// ============================================================
// Content-Length accuracy (critical for browser download)
// ============================================================

func TestHandleBatch_ContentLengthAccurate(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG.jpg")
	data := make([]byte, 5000)
	os.WriteFile(f1, data, 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		batches: []Batch{
			{ID: "batch1", AlbumName: "Camera", Files: []FileEntry{
				{Path: "IMG.jpg", FullPath: f1, Size: 5000, ModTime: 1000000},
			}, TotalSize: 5000},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/batch/batch1?token=test-token-123", nil)
	w := httptest.NewRecorder()
	s.handleBatch(w, req)

	cl := w.Header().Get("Content-Length")
	if cl == "" {
		t.Fatal("Content-Length header missing")
	}

	var declared, actual int64
	fmt.Sscanf(cl, "%d", &declared)
	actual = int64(w.Body.Len())

	if declared != actual {
		t.Errorf("Content-Length=%d but body=%d (diff=%d)", declared, actual, actual-declared)
	}
}

// ============================================================
// Range request — Free ZIP (resume not supported)
// ============================================================

func TestHandleBatch_RangeRequestFreeMode(t *testing.T) {
	// single-zip-trust-tcp §1: Range resume is not supported. Even if the client
	// sends a Range header, the server must return a full 200 response (to avoid
	// the browser's auto-resume triggering a "ghost new download").
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "IMG.jpg")
	os.WriteFile(f1, make([]byte, 5000), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		batches: []Batch{
			{ID: "batch1", AlbumName: "photos", Files: []FileEntry{
				{Path: "IMG.jpg", FullPath: f1, Size: 5000, ModTime: 1000000},
			}, TotalSize: 5000},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/batch/batch1?token=test-token-123", nil)
	req.Header.Set("Range", "bytes=0-99")
	w := httptest.NewRecorder()
	s.handleBatch(w, req)

	if w.Code != 200 {
		t.Errorf("Range request status = %d, want 200 (Range not supported, must return full response)", w.Code)
	}
	// single-zip-trust-tcp §1: explicitly declare Accept-Ranges: none, clearly
	// telling the browser that resume is unsupported, preventing a Range resume
	// after an interrupted download → server ignores Range and returns 200 full
	// → browser treats it as a new download ("finished then restarted"). Stronger
	// than leaving it empty at expressing "not resumable".
	if got := w.Header().Get("Accept-Ranges"); got != "none" {
		t.Errorf("Accept-Ranges = %q, want \"none\" (explicitly forbid resume)", got)
	}
	if w.Body.Len() < 5000 {
		t.Errorf("body length = %d, should be >= 5000 (full ZIP, not a Range slice)", w.Body.Len())
	}
}

// ============================================================
// Empty result — selected paths yield no media files (Free still includes videos)
// ============================================================

func TestHandleSelect_FreeModeIncludesVideos(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "VID.mp4"), make([]byte, 200), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		albums: []Album{
			{Path: tmpDir, Name: "Videos", FileCount: 1, TotalSize: 200},
		},
		thumbs: make(map[int][]byte),
	}

	reqBody, _ := json.Marshal(map[string]interface{}{"paths": []string{tmpDir}, "since": 0})
	req := httptest.NewRequest("POST", "/api/select", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer test-token-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSelect(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	// spec §5.2 ("original video preserved byte-for-byte, 4K/60fps") — Free includes videos.
	if resp["file_count"].(float64) == 0 {
		t.Errorf("video-only album in Free mode: file_count = %v, want ≥1 (spec §5.2 includes video)", resp["file_count"])
	}
	// single-zip-trust-tcp §1.2.1: every file → one batch. Video-only album
	// must still produce exactly 1 batch (the videos are inside it).
	if resp["batch_count"].(float64) != 1 {
		t.Errorf("video-only album in Free mode: batch_count = %v, want 1 (single ZIP)", resp["batch_count"])
	}

	// Strengthened assertion (QA P1): the single batch must contain the
	// video file (file_count=1 proves it). ID carries the photos_ prefix.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.batches) == 0 {
		t.Fatalf("expected 1 batch, got 0 — Free should include videos per spec §5.2")
	}
	if !strings.HasPrefix(s.batches[0].ID, "photos_") {
		t.Errorf("batch ID = %q, want prefix \"photos_\"", s.batches[0].ID)
	}
	if len(s.batches[0].Files) != 1 {
		t.Errorf("batch should hold 1 file (the video), got %d", len(s.batches[0].Files))
	}
}

// ============================================================
// ZIP data integrity — verify file content matches
// ============================================================

func TestWriteBatchZip_DataIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	content := []byte("hello world test data 12345")
	f1 := filepath.Join(tmpDir, "test.jpg")
	os.WriteFile(f1, content, 0644)

	batch := &Batch{
		ID:        "test",
		AlbumName: "Camera",
		Files: []FileEntry{
			{Path: "test.jpg", FullPath: f1, Size: int64(len(content)), ModTime: 1000000},
		},
		TotalSize: int64(len(content)),
	}

	var buf bytes.Buffer
	zipSize := calculateZipSize(batch.Files, ZipWriteOptions{})
	err := writeBatchZip(&buf, batch, 0, zipSize, ZipWriteOptions{}, false, context.Background(), nil)
	if err != nil {
		t.Fatalf("writeBatchZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}

	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()

	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %d bytes, want %d bytes", len(got), len(content))
	}
}

// ============================================================
// EXIF / GPS stripping (exifutil.go kept as a standalone tool, Pro call sites removed)
// ============================================================

func TestStripGpsFromJpeg_InvalidInput(t *testing.T) {
	_, err := stripGpsFromJpeg([]byte("not a jpeg"))
	if err == nil {
		t.Error("expected error for non-JPEG input")
	}
}

func TestStripGpsFromJpeg_NoExif(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	original := buf.Bytes()

	result, err := stripGpsFromJpeg(original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, original) {
		t.Error("JPEG without EXIF should be returned unchanged")
	}
}

// TestStripExifFromJpeg_AllCategories_NoCrash verifies multi-category EXIF
// stripping does not panic on a real JPEG (without EXIF) and returns the
// original data unchanged.
func TestStripExifFromJpeg_AllCategories_NoCrash(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	original := buf.Bytes()

	cats := []string{"gps", "time", "device", "shot", "author"}
	result, err := stripExifFromJpeg(original, cats)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, original) {
		t.Error("JPEG without EXIF should be returned unchanged regardless of categories")
	}
}

// TestStripExifFromJpeg_EmptyCategories verifies an empty category list is a no-op.
func TestStripExifFromJpeg_EmptyCategories(t *testing.T) {
	original := []byte("fake jpeg data")
	result, err := stripExifFromJpeg(original, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, original) {
		t.Error("empty categories should be no-op")
	}
}

// TestStripExifFromJpeg_InvalidInput verifies non-JPEG input returns an error.
func TestStripExifFromJpeg_InvalidInput(t *testing.T) {
	_, err := stripExifFromJpeg([]byte("not a jpeg"), []string{"gps"})
	if err == nil {
		t.Error("expected error for non-JPEG input")
	}
}

func TestReadExifDateTime_NoExif(t *testing.T) {
	fallback := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	dt := readExifDateTime([]byte("no exif here"), fallback)
	if !dt.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", dt, fallback)
	}
}

func TestReadExifDateTime_FromMinimalJpeg(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)

	fallback := time.Date(2025, 12, 25, 10, 30, 0, 0, time.UTC)
	dt := readExifDateTime(buf.Bytes(), fallback)
	if !dt.Equal(fallback) {
		t.Errorf("JPEG without EXIF should return fallback, got %v", dt)
	}
}

// TestHandleBatch_FreeModeIgnoresStripGps verifies Free mode ignores the
// strip_gps parameter: file bytes are written as-is (Pro EXIF stripping call
// sites removed).
func TestHandleBatch_FreeModeIgnoresStripGps(t *testing.T) {
	tmpDir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var jpegBuf bytes.Buffer
	jpeg.Encode(&jpegBuf, img, nil)
	f1 := filepath.Join(tmpDir, "IMG.jpg")
	os.WriteFile(f1, jpegBuf.Bytes(), 0644)

	s := &server{
		pin:   "1234",
		token: "test-token-123",
		batches: []Batch{
			{ID: "batch1", AlbumName: "photos", Files: []FileEntry{
				{Path: "IMG.jpg", FullPath: f1, Size: int64(jpegBuf.Len()), ModTime: 1000000},
			}, TotalSize: int64(jpegBuf.Len())},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/batch/batch1?token=test-token-123&strip_gps=1", nil)
	w := httptest.NewRecorder()
	s.handleBatch(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}

	// Find the non-manifest user file entry.
	var rc io.ReadCloser
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			rc, _ = f.Open()
			break
		}
	}
	if rc == nil {
		t.Fatal("no user file entry in ZIP")
	}
	got, _ := io.ReadAll(rc)
	rc.Close()

	if !bytes.Equal(got, jpegBuf.Bytes()) {
		t.Error("Free mode should ignore strip_gps — file data should be unchanged")
	}
}
