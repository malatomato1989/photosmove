package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// makeTestJPEG generates an in-memory JPEG of approximately the requested size
// by encoding a colored RGBA image. Used to build realistic test fixtures
// without committing binary assets.
func makeTestJPEG(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// writeJPEGFiles creates n JPEG files in dir and returns FileEntry slice +
// total byte size. Files are ~100KB each so 10 files ~= 1MB.
func writeJPEGFiles(t *testing.T, dir string, n int) ([]FileEntry, int64) {
	t.Helper()
	var files []FileEntry
	var total int64
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, "IMG_"+pad3(i)+".jpg")
		// 512x512 RGB JPEG at q=85 is ~30-50KB. Use 768x1024 for ~100KB.
		jpegBytes := makeTestJPEG(t, 768, 1024, color.RGBA{
			R: uint8(i * 25),
			G: uint8(255 - i*20),
			B: 128,
			A: 255,
		})
		if err := os.WriteFile(name, jpegBytes, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		files = append(files, FileEntry{
			Path:     "Camera/IMG_" + pad3(i) + ".jpg",
			FullPath: name,
			Size:     fi.Size(),
			ModTime:  fi.ModTime().Unix(),
		})
		total += fi.Size()
	}
	return files, total
}

func pad3(i int) string {
	if i < 10 {
		return "00" + itoa(i)
	}
	if i < 100 {
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// blockingResponseWriter is an http.ResponseWriter whose Write blocks until
// either releaseCh is closed or the surrounding ctx is cancelled. This lets
// integration tests simulate a slow / paused HTTP client so that
// ctxWriter.Write observes ctx.Done and returns ctx.Err() — the exact path
// the browser-pause cancellation must take in production.
type blockingResponseWriter struct {
	header      http.Header
	headerOnce  sync.Once
	releaseCh   chan struct{}
	writeSeen   int64 // atomic: bytes seen before block
	mu          sync.Mutex
	writeCalled bool
	statusCode  int
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:    make(http.Header),
		releaseCh: make(chan struct{}),
	}
}

func (b *blockingResponseWriter) Header() http.Header {
	return b.header
}

func (b *blockingResponseWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.writeCalled = true
	b.mu.Unlock()
	// Block forever until released. The ctxWriter wrapper will cancel the
	// surrounding ctx via cancelBatch() and short-circuit through its own
	// goroutine select, returning ctx.Err() to the archiver.
	<-b.releaseCh
	return 0, io.ErrClosedPipe
}

func (b *blockingResponseWriter) WriteHeader(code int) {
	b.statusCode = code
}

// release unblocks all pending / future Write calls so the handler goroutine
// can drain and exit after ctx cancellation propagates.
func (b *blockingResponseWriter) release() {
	select {
	case <-b.releaseCh:
	default:
		close(b.releaseCh)
	}
}

// =====================================================================
// Test 1: POST /api/cancel interrupts an in-flight ZIP stream
// =====================================================================
//
// Scenario:
//   - 10 real JPEG files (~1MB total) staged on disk
//   - handleBatch invoked on a blockingResponseWriter (Write never returns
//     until releaseCh is closed, simulating a paused browser download)
//   - After 100ms, cancelBatch(batchID) is called from the main goroutine
//   - handleBatch MUST return within 5s (ctx cancellation propagates through
//     ctxWriter → archiver → writeBatchZip loop → handler exit)
//
// Pass criteria (any one):
//   - handleBatch returns within the 5s timeout, AND
//   - the progress entry shows cancelled=true OR the goroutine exit signals
//     ctx.Err() was observed (we check: handler returned, isBatchCanceled true)
//
// Failure mode if cancel is broken: handleBatch hangs on Write forever,
// goroutine never exits, test times out at 5s and fails with a clear error.
func TestCancelInterrupts_ZipStream(t *testing.T) {
	// Reset global cancel state from prior tests.
	clearCancelState()

	tmpDir := t.TempDir()
	files, totalSize := writeJPEGFiles(t, tmpDir, 10)
	t.Logf("staged %d JPEGs, totalSize=%d bytes", len(files), totalSize)

	const batchID = "cancel-test-batch-1"
	s := &server{
		pin:     "1234",
		token:   "test-token-123",
		batches: []Batch{
			{
				ID:        batchID,
				AlbumName: "Camera",
				Files:     files,
				TotalSize: totalSize,
			},
		},
		thumbs: make(map[int][]byte),
	}

	req := httptest.NewRequest("GET", "/api/batch/"+batchID+"?token=test-token-123", nil)
	// Use a fresh background ctx (not the test's ctx) so cancellation comes
	// ONLY from cancelBatch, not from t.Fatal cleanup. This isolates the
	// cancel mechanism under test.
	req = req.WithContext(context.Background())

	w := newBlockingResponseWriter()

	handlerDone := make(chan error, 1)
	startTime := time.Now()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				handlerDone <- fmt.Errorf("handler panic: %v", r)
			}
		}()
		s.handleBatch(w, req)
		handlerDone <- nil
	}()

	// Give the handler time to: enter handleBatch, register cancel,
	// start writing the first file (which blocks on Write).
	time.Sleep(100 * time.Millisecond)

	// Verify cancel is registered before we try to invoke it.
	cancelMapMu.Lock()
	_, registered := cancelMap[batchID]
	cancelMapMu.Unlock()
	if !registered {
		// Handler may not have reached registerCancel yet — give it more time.
		time.Sleep(200 * time.Millisecond)
		cancelMapMu.Lock()
		_, registered = cancelMap[batchID]
		cancelMapMu.Unlock()
		if !registered {
			t.Fatalf("cancel func not registered for batch %s after 300ms — handler stuck before registerCancel?", batchID)
		}
	}

	// Fire the cancel. This MUST unblock ctxWriter.Write via its internal
	// goroutine select on ctx.Done().
	cancelled := cancelBatch(batchID)
	if !cancelled {
		t.Fatalf("cancelBatch(%q) returned false — no cancel func registered", batchID)
	}
	t.Logf("cancelBatch returned true after %v", time.Since(startTime))

	// Wait for handler to exit (ctx.Err propagates → writeBatchZip returns →
	// handleBatch returns). Hard timeout to surface a stuck handler.
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("handleBatch goroutine panicked: %v", err)
		}
		t.Logf("handleBatch returned after %v (cancel propagated successfully)", time.Since(startTime))
	case <-time.After(5 * time.Second):
		// BUG indicator: cancel did NOT interrupt the Write loop. Release
		// the writer so the goroutine can exit and we can report cleanly.
		w.release()
		t.Fatalf("handleBatch did NOT return within 5s of cancelBatch — cancel mechanism is BROKEN (ctxWriter.Write not observing ctx.Done, or archiver loop not checking ctx between files)")
	}

	// Sanity: batch should be marked canceled (markBatchCanceled runs in
	// handleCancel, not in cancelBatch itself — so check isBatchCanceled is
	// false here, but progress.cancelled should be true if progress was set).
	if p := getBatchProgress(batchID); p != nil {
		_, _, _, _, progCancelled, _ := p.snapshot()
		if progCancelled {
			t.Logf("progress tracker shows cancelled=true (archiver observed ctx.Done)")
		} else {
			t.Logf("NOTE: progress tracker shows cancelled=false — handler exited before progress.cancel() ran, OR cancellation path bypassed progress tracking")
		}
	}

	// Unblock any lingering Write so we don't leak a goroutine past test end.
	w.release()
}

// =====================================================================
// Test 2: cancelBatch + markBatchCanceled + isBatchCanceled state machine
// =====================================================================
func TestCancelBatch_MarksCanceled(t *testing.T) {
	clearCancelState()

	t.Run("cancelBatch_returns_false_for_unknown_batch", func(t *testing.T) {
		clearCancelState()
		if cancelBatch("nonexistent-batch") {
			t.Fatal("cancelBatch should return false for an unregistered batch ID")
		}
	})

	t.Run("cancelBatch_returns_true_and_invokes_cancelFunc", func(t *testing.T) {
		clearCancelState()
		const id = "marks-canceled-1"
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		registerCancel(id, cancel)

		if ctx.Err() != nil {
			t.Fatal("ctx should not be cancelled before cancelBatch")
		}
		if !cancelBatch(id) {
			t.Fatal("cancelBatch should return true for a registered batch")
		}
		// The registered CancelFunc MUST have been invoked.
		select {
		case <-ctx.Done():
			// good
		case <-time.After(100 * time.Millisecond):
			t.Fatal("ctx was not cancelled after cancelBatch returned true")
		}

		// cancelBatch must remove the entry (idempotency: second call returns false).
		if cancelBatch(id) {
			t.Fatal("cancelBatch should return false on second call — entry must be removed after first cancel")
		}
	})

	t.Run("markBatchCanceled_makes_isBatchCanceled_true", func(t *testing.T) {
		clearCancelState()
		const id = "marks-canceled-2"
		if isBatchCanceled(id) {
			t.Fatal("isBatchCanceled should be false before markBatchCanceled")
		}
		markBatchCanceled(id)
		if !isBatchCanceled(id) {
			t.Fatal("isBatchCanceled should be true after markBatchCanceled")
		}
	})

	t.Run("handleCancel_marks_canceled_when_batch_registered", func(t *testing.T) {
		clearCancelState()
		const id = "marks-canceled-3"
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		registerCancel(id, cancel)

		s := &server{pin: "1234", token: "tok", thumbs: make(map[int][]byte)}
		body := strings.NewReader(`{"batch_id":"` + id + `"}`)
		req := httptest.NewRequest("POST", "/api/cancel", body)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.handleCancel(rec, req)

		if rec.Code != 200 {
			t.Fatalf("handleCancel status=%d, want 200", rec.Code)
		}
		var resp struct {
			Cancelled bool `json:"cancelled"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.Cancelled {
			t.Fatal("handleCancel response cancelled=false, want true (batch was registered)")
		}
		if !isBatchCanceled(id) {
			t.Fatal("isBatchCanceled should be true after handleCancel on a registered batch")
		}
	})
}

// =====================================================================
// Test 3: handleCancel auth
// =====================================================================
func TestHandleCancel_RequiresAuth(t *testing.T) {
	clearCancelState()

	makeServer := func() *server {
		return &server{
			pin:    "1234",
			token:  "test-token-123",
			thumbs: make(map[int][]byte),
		}
	}

	t.Run("no_token_returns_401", func(t *testing.T) {
		s := makeServer()
		req := httptest.NewRequest("POST", "/api/cancel", strings.NewReader(`{"batch_id":"b1"}`))
		rec := httptest.NewRecorder()
		// Wrap with authRequired as the production mux does.
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})

	t.Run("wrong_token_returns_401", func(t *testing.T) {
		s := makeServer()
		req := httptest.NewRequest("POST", "/api/cancel", strings.NewReader(`{"batch_id":"b1"}`))
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
	})

	t.Run("correct_token_nonexistent_batch_returns_200_with_cancelled_false", func(t *testing.T) {
		clearCancelState()
		s := makeServer()
		// handleCancel is idempotent: cancelling an unknown batch returns
		// 200 OK with cancelled=false (no-op). Verified against current impl.
		req := httptest.NewRequest("POST", "/api/cancel", strings.NewReader(`{"batch_id":"does-not-exist"}`))
		req.Header.Set("Authorization", "Bearer test-token-123")
		rec := httptest.NewRecorder()
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200 (handleCancel is idempotent for unknown batches)", rec.Code)
		}
		var resp struct {
			Cancelled bool `json:"cancelled"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Cancelled {
			t.Fatalf("cancelled=true for unknown batch, want false. body=%s", rec.Body.String())
		}
	})

	t.Run("correct_token_registered_batch_returns_200_with_cancelled_true", func(t *testing.T) {
		clearCancelState()
		s := makeServer()
		const id = "auth-test-batch"
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		registerCancel(id, cancel)

		req := httptest.NewRequest("POST", "/api/cancel", strings.NewReader(`{"batch_id":"`+id+`"}`))
		req.Header.Set("Authorization", "Bearer test-token-123")
		rec := httptest.NewRecorder()
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200", rec.Code)
		}
		var resp struct {
			Cancelled bool `json:"cancelled"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.Cancelled {
			t.Fatalf("cancelled=false for registered batch, want true. body=%s", rec.Body.String())
		}
		if !isBatchCanceled(id) {
			t.Fatal("isBatchCanceled should be true after successful handleCancel")
		}
	})

	t.Run("wrong_method_returns_405", func(t *testing.T) {
		s := makeServer()
		req := httptest.NewRequest("GET", "/api/cancel", nil)
		req.Header.Set("Authorization", "Bearer test-token-123")
		rec := httptest.NewRecorder()
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status=%d, want 405", rec.Code)
		}
	})

	t.Run("missing_batch_id_returns_400", func(t *testing.T) {
		s := makeServer()
		req := httptest.NewRequest("POST", "/api/cancel", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer test-token-123")
		rec := httptest.NewRecorder()
		s.authRequired(s.handleCancel)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
}

// clearCancelState resets global cancel / progress / canceled maps so tests
// don't bleed state into each other. Tests run serially within a package so
// this is safe.
func clearCancelState() {
	cancelMapMu.Lock()
	cancelMap = make(map[string]context.CancelFunc)
	cancelMapMu.Unlock()

	canceledBatchesMu.Lock()
	canceledBatches = make(map[string]time.Time)
	canceledBatchesMu.Unlock()

	progressMapMu.Lock()
	progressMap = make(map[string]*batchProgress)
	progressMapMu.Unlock()
}
