// Package archiver holds batch ZIP planning and retry helpers that back the
// photosmove streaming pipeline. It complements the top-level archiver.go which
// owns the core writeBatchZip / writeFileToZip loop.
package archiver

import (
	"io"
	"os"
	"time"
)

// DefaultRetryDelays is the exponential backoff schedule used by RetryOpen.
// The schedule covers the two dominant transient failure modes on Android:
// flash-storage spin-up after screen lock (~100-500ms) and iCloud/Photos
// cache misses when a remote-only asset is touched for the first time (~2s).
//
// Validated by retry_test.go (exponential backoff timing).
var DefaultRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

// RetryOpen opens path with up to len(delays)+1 attempts. Between attempts it
// sleeps for the durations specified by delays. The function returns the
// reader from the first successful Open, or the last error if all attempts
// fail.
//
// Why open-stage retry (not io.Copy-stage):
// Once writeFileToZip calls CreateHeader + Write on a ZIP entry, those bytes
// have already been streamed to the HTTP ResponseWriter and cannot be taken
// back. A retry at io.Copy stage would write a half-file + the rest = a
// corrupt ZIP entry. Retrying at os.Open is the only safe place because no
// bytes have been emitted yet.
//
// If maxRetry is zero, only one attempt is made. If delays is shorter than
// maxRetry-1, missing slots default to the last entry of delays (or zero if
// delays is empty).
func RetryOpen(path string, maxRetry int, delays []time.Duration) (io.ReadCloser, error) {
	if maxRetry < 1 {
		maxRetry = 1
	}
	var lastErr error
	for attempt := 0; attempt < maxRetry; attempt++ {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		lastErr = err
		if attempt == maxRetry-1 {
			break
		}
		delay := delayFor(delays, attempt)
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}

func delayFor(delays []time.Duration, attempt int) time.Duration {
	if len(delays) == 0 {
		return 0
	}
	if attempt >= len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt]
}
