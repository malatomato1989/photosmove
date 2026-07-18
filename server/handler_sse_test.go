package main

import (
	"testing"
	"time"
)

// TestShouldPushSSE_DualTrigger verifies the dual-trigger condition
// (>=500ms AND >=10MB) holds for normal progress, and the force-push
// backstop fires for slow stretches.
func TestShouldPushSSE_DualTrigger(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	// First push: always fire.
	should, term := shouldPushSSE(base, time.Time{}, time.Time{}, 0, 5*1024*1024, false)
	if !should || term {
		t.Fatalf("first push should fire (should=%v term=%v)", should, term)
	}

	lastPush := base
	lastForce := base
	lastSent := int64(5 * 1024 * 1024)

	// 300ms + 8MB later: neither arm fires.
	should, _ = shouldPushSSE(base.Add(300*time.Millisecond), lastPush, lastForce, lastSent, lastSent+8*1024*1024, false)
	if should {
		t.Fatal("300ms+8MB must NOT fire — both arms below threshold")
	}

	// 600ms + 8MB: time arm met but bytes arm below threshold → no push.
	should, _ = shouldPushSSE(base.Add(600*time.Millisecond), lastPush, lastForce, lastSent, lastSent+8*1024*1024, false)
	if should {
		t.Fatal("600ms+8MB must NOT fire — bytes arm still below 10MB")
	}

	// 600ms + 12MB: both arms fire.
	should, _ = shouldPushSSE(base.Add(600*time.Millisecond), lastPush, lastForce, lastSent, lastSent+12*1024*1024, false)
	if !should {
		t.Fatal("600ms+12MB should fire (both arms met)")
	}

	// Update state after a real push.
	lastPush = base.Add(600 * time.Millisecond)
	lastForce = lastPush
	lastSent += 12 * 1024 * 1024

	// 1.1s later, no progress: force-push backstop fires.
	should, _ = shouldPushSSE(base.Add(1700*time.Millisecond), lastPush, lastForce, lastSent, lastSent, false)
	if !should {
		t.Fatal("1.1s without 10MB should fire backstop")
	}

	// Terminal: always fire.
	should, isTerm := shouldPushSSE(base.Add(2*time.Second), lastPush, lastForce, lastSent, lastSent, true)
	if !should || !isTerm {
		t.Fatalf("terminal must fire; got should=%v term=%v", should, isTerm)
	}
}

// TestSSE_PushCount_30GB simulates a 30GB transfer at 20MB/s and asserts
// the total push count lands in the 3000-6000 budget (T-big-3 § 3.4.2).
//
// Model:
//   - ticker ticks every 100ms (real handleProgress behaviour post-T-big-3)
//   - between ticks, 2MB of bytes arrive (20MB/s × 100ms = 2MB)
//   - dual-trigger (>=500ms AND >=10MB) fires every 5 ticks = 500ms wallclock
//   - 30GB / 20MB/s = ~1500s total → ~3000 push events
//
// 3000 sits at the lower edge of the 3000-6000 envelope (the upper bound is
// generous to absorb faster USB/80MBps scenarios where dual-trigger fires
// every ~500ms × 375s ≈ 750 pushes; the 6000 ceiling is set conservatively
// to leave headroom for jittery real-world WiFi).
func TestSSE_PushCount_30GB(t *testing.T) {
	const total = int64(30 * 1024 * 1024 * 1024) // 30 GiB
	const bytesPerTick int64 = 2 * 1024 * 1024   // 2 MiB per 100ms tick (20MB/s)
	const tick = 100 * time.Millisecond

	var pushes int
	var sent int64
	now := time.Unix(1_700_000_000, 0)
	var lastPush, lastForce time.Time
	var lastSent int64

	// Prime: send the first event so lastPush/lastForce/lastSent are
	// initialised.
	firstShould, _ := shouldPushSSE(now, lastPush, lastForce, lastSent, sent, false)
	if firstShould {
		pushes++
		lastPush = now
		lastForce = now
		lastSent = sent
	}

	for sent < total {
		// Advance one tick of wallclock AND one tick of bytes.
		now = now.Add(tick)
		sent += bytesPerTick
		if sent > total {
			sent = total
		}

		// Terminal only when we've actually finished.
		terminal := sent >= total
		should, term := shouldPushSSE(now, lastPush, lastForce, lastSent, sent, terminal)
		if should {
			pushes++
			lastPush = now
			lastForce = now
			lastSent = sent
			if term {
				break
			}
		}
	}

	if pushes < 3000 || pushes > 6000 {
		t.Fatalf("30GB push count = %d, want 3000-6000", pushes)
	}
}

// TestShouldPushSSE_SmallFileBudget verifies an 800MB small-file batch
// stays within the 80-160 push budget at 20MB/s (T-big-3 § 3.4.2 small-file
// scenario, same throughput model as the 30GB test above).
//
// Model:
//   - 20MB/s × 100ms = 2MB/tick
//   - dual-trigger (500ms AND 10MB) fires every 5 ticks
//   - 800MB / 20MB/s = 40s = 400 ticks → ~80 pushes
func TestShouldPushSSE_SmallFileBudget(t *testing.T) {
	const total = int64(800 * 1024 * 1024) // 800 MiB
	const bytesPerTick int64 = 2 * 1024 * 1024
	const tick = 100 * time.Millisecond

	now := time.Unix(1_700_000_000, 0)
	var lastPush, lastForce time.Time
	var lastSent int64
	sent := int64(0)
	pushes := 0

	firstShould, _ := shouldPushSSE(now, lastPush, lastForce, lastSent, sent, false)
	if firstShould {
		pushes++
		lastPush = now
		lastForce = now
		lastSent = sent
	}

	for sent < total {
		now = now.Add(tick)
		sent += bytesPerTick
		if sent > total {
			sent = total
		}
		terminal := sent >= total
		should, term := shouldPushSSE(now, lastPush, lastForce, lastSent, sent, terminal)
		if should {
			pushes++
			lastPush = now
			lastForce = now
			lastSent = sent
			if term {
				break
			}
		}
	}

	if pushes < 80 || pushes > 160 {
		t.Fatalf("800MB push count = %d, want 80-160", pushes)
	}
}
