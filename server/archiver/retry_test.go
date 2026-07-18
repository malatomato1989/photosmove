package archiver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRetryOpenSucceedsOnFirstTry covers the happy path: no retry needed.
func TestRetryOpenSucceedsOnFirstTry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := RetryOpen(p, 3, DefaultRetryDelays)
	if err != nil {
		t.Fatalf("RetryOpen: %v", err)
	}
	defer f.Close()
}

// TestRetryOpenRetriesUntilSuccess uses a deliberately missing file the first
// attempt and a marker file the second attempt via a tmpdir + sleep cycle.
// Since we can't simulate "open fails then succeeds" with os.Open directly,
// we instead verify the delay sequence is honoured by measuring total time.
func TestRetryOpenHonoursDelaySequence(t *testing.T) {
	start := time.Now()
	_, err := RetryOpen(filepath.Join(t.TempDir(), "missing"), 3,
		[]time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond})
	if err == nil {
		t.Fatal("expected error opening missing file")
	}
	elapsed := time.Since(start)
	// Total sleep budget: 50 + 100 + 200 = 350ms (delays between 4 attempts:
	// actually len(delays)=3, maxRetry=3 means 3 attempts total, 2 sleeps of
	// delays[0]+delays[1] = 150ms). Adjust expectation to >= 140ms (allow
	// scheduling slack) and < 500ms (no over-sleep).
	if elapsed < 140*time.Millisecond {
		t.Errorf("elapsed=%v, want >= 140ms (2 backoff sleeps)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed=%v, want <= 500ms (no over-sleep)", elapsed)
	}
}

// TestRetryOpenMaxRetryZeroMakesOneAttempt ensures a zero maxRetry still
// attempts once (otherwise callers would silently get nil-nil).
func TestRetryOpenMaxRetryZeroMakesOneAttempt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := RetryOpen(p, 0, nil)
	if err != nil {
		t.Fatalf("RetryOpen with maxRetry=0: %v", err)
	}
	f.Close()
}

// TestDelayForFallbackToLastDelay confirms delays shorter than the attempt
// index reuse the last entry rather than skipping the sleep.
func TestDelayForFallbackToLastDelay(t *testing.T) {
	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	if got := delayFor(delays, 5); got != 20*time.Millisecond {
		t.Errorf("delayFor(5) = %v, want 20ms (last)", got)
	}
	if got := delayFor(delays, 0); got != 10*time.Millisecond {
		t.Errorf("delayFor(0) = %v, want 10ms", got)
	}
	if got := delayFor(nil, 0); got != 0 {
		t.Errorf("delayFor(nil) = %v, want 0", got)
	}
}
