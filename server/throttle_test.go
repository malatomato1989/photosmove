package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// errWriter errors after writing n bytes.
type errWriter struct {
	n   int
	wrote int
}

func (e *errWriter) Write(p []byte) (int, error) {
	remain := e.n - e.wrote
	if remain <= 0 {
		return 0, errors.New("write error")
	}
	chunk := len(p)
	if chunk > remain {
		chunk = remain
	}
	e.wrote += chunk
	return chunk, errors.New("write error")
}

// ============================================================
// newThrottleWriter
// ============================================================

func TestNewThrottleWriter_InitState(t *testing.T) {
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 5*1024*1024)
	if tw.rate != 5*1024*1024 {
		t.Errorf("rate = %d, want %d", tw.rate, 5*1024*1024)
	}
	if tw.burst != 5*1024*1024 {
		t.Errorf("burst = %d, want %d", tw.burst, 5*1024*1024)
	}
	if tw.tokens != 5*1024*1024 {
		t.Errorf("initial tokens = %d, want %d (should equal burst)", tw.tokens, 5*1024*1024)
	}
}

// ============================================================
// Small writes within burst
// ============================================================

func TestThrottleWriter_SmallWriteNoDelay(t *testing.T) {
	// A write well within burst should complete near-instantly.
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 1024*1024) // 1 MB/s burst

	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}

	start := time.Now()
	n, err := tw.Write(data)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("wrote %d bytes, want %d", n, len(data))
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("data mismatch")
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("small write took %v, should be near-instant", elapsed)
	}
}

// ============================================================
// Data integrity: sequential writes produce correct output
// ============================================================

func TestThrottleWriter_DataIntegrity(t *testing.T) {
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 10*1024*1024)

	chunks := [][]byte{
		[]byte("hello "),
		[]byte("world "),
		[]byte("foo"),
	}
	for _, chunk := range chunks {
		n, err := tw.Write(chunk)
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != len(chunk) {
			t.Errorf("wrote %d, want %d", n, len(chunk))
		}
	}

	got := buf.String()
	want := "hello world foo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ============================================================
// Empty write
// ============================================================

func TestThrottleWriter_EmptyWrite(t *testing.T) {
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 1024)

	n, err := tw.Write(nil)
	if err != nil {
		t.Fatalf("Write(nil) error: %v", err)
	}
	if n != 0 {
		t.Errorf("Write(nil) = %d, want 0", n)
	}
	if buf.Len() != 0 {
		t.Errorf("buffer should be empty, got %d bytes", buf.Len())
	}
}

// ============================================================
// Error propagation
// ============================================================

func TestThrottleWriter_ErrorPropagation(t *testing.T) {
	ew := &errWriter{n: 5}
	tw := newThrottleWriter(ew, 1024*1024)

	data := make([]byte, 100)
	n, err := tw.Write(data)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if n != 5 {
		t.Errorf("wrote %d bytes before error, want 5", n)
	}
}

// ============================================================
// Token bucket exhaustion and refill
// ============================================================

func TestThrottleWriter_TokenExhaustion(t *testing.T) {
	// Use a very low rate so tokens deplete fast.
	// Start with burst=100 tokens, rate=100 bytes/sec.
	// Write 200 bytes: first 100 from burst, then must wait ~1s for refill.
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 100) // 100 B/s, burst 100

	data := make([]byte, 200)
	for i := range data {
		data[i] = byte(i % 256)
	}

	start := time.Now()
	n, err := tw.Write(data)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 200 {
		t.Errorf("wrote %d bytes, want 200", n)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("data mismatch")
	}
	// Should have taken ~1s to refill 100 tokens at 100 B/s.
	// Allow generous tolerance for CI.
	if elapsed < 500*time.Millisecond {
		t.Errorf("200 bytes at 100 B/s with burst 100 took only %v, expected ~1s", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
}

// ============================================================
// Rate accuracy: verify effective throughput
// ============================================================

func TestThrottleWriter_RateAccuracy(t *testing.T) {
	// Write 500KB (exhausts burst) at 500KB/s, then write another 500KB
	// which must wait ~1s for token refill.
	var buf bytes.Buffer
	rate := int64(500 * 1024) // 500 KB/s, burst 500KB
	tw := newThrottleWriter(&buf, rate)

	// First 500KB — from burst, instant.
	data1 := make([]byte, 500*1024)
	start := time.Now()
	n, err := tw.Write(data1)
	if err != nil || n != len(data1) {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	t1 := time.Since(start)
	if t1 > 100*time.Millisecond {
		t.Errorf("burst write took %v, should be instant", t1)
	}

	// Second 500KB — needs ~1s token refill.
	data2 := make([]byte, 500*1024)
	start = time.Now()
	n, err = tw.Write(data2)
	if err != nil || n != len(data2) {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	t2 := time.Since(start)

	// Expected: ~1s (500KB at 500KB/s).
	if t2 < 500*time.Millisecond {
		t.Errorf("throttled write took only %v, expected ~1s", t2)
	}
	if t2 > 5*time.Second {
		t.Errorf("throttled write took too long: %v", t2)
	}

	total := buf.Len()
	if total != 1000*1024 {
		t.Errorf("total written = %d, want %d", total, 1000*1024)
	}
}

// ============================================================
// Burst cap: tokens should not exceed burst
// ============================================================

func TestThrottleWriter_BurstCap(t *testing.T) {
	var buf bytes.Buffer
	tw := newThrottleWriter(&buf, 1000) // 1000 B/s, burst 1000

	// Drain tokens.
	drain := make([]byte, 1000)
	tw.Write(drain)

	// Wait 2 seconds — should accumulate 2000 tokens but cap at 1000.
	time.Sleep(2 * time.Second)

	if tw.tokens > tw.burst {
		t.Errorf("tokens = %d, should not exceed burst %d", tw.tokens, tw.burst)
	}

	// A 1000-byte write should be instant (from refilled burst).
	data := make([]byte, 1000)
	start := time.Now()
	n, err := tw.Write(data)
	if err != nil || n != 1000 {
		t.Fatalf("post-sleep write: n=%d err=%v", n, err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Error("post-sleep write should be instant from refilled burst")
	}
}

// ============================================================
// io.Writer interface compliance
// ============================================================

func TestThrottleWriter_ImplementsIowriter(t *testing.T) {
	var _ io.Writer = (*throttleWriter)(nil)
}

// ============================================================
// Write chain integration: throttleWriter + ctxWriter
// ============================================================

func TestThrottleWriter_WithCtxWriter(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()

	// Simulate the handler chain: throttle → ctxWriter
	tw := newThrottleWriter(&buf, 1*1024*1024)
	cw := &ctxWriter{w: tw, ctx: ctx}

	data := []byte("test data through chain")
	n, err := cw.Write(data)
	if err != nil {
		t.Fatalf("chain write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("chain wrote %d, want %d", n, len(data))
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("chain data mismatch: got %q, want %q", buf.String(), string(data))
	}
}
