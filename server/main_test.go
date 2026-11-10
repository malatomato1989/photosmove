package main

import (
	"net"
	"testing"
)

func occupyPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestListenWithFallback_PrefersGivenPort(t *testing.T) {
	preferred := freePort(t)
	ln, port, err := listenWithFallback(preferred, []int{0})
	if err != nil {
		t.Fatalf("bind preferred port %d: %v", preferred, err)
	}
	defer ln.Close()
	if port != preferred {
		t.Fatalf("port = %d, want preferred %d", port, preferred)
	}
}

func TestListenWithFallback_SkipsBusyPreferredPort(t *testing.T) {
	busy, busyPort := occupyPort(t)
	defer busy.Close()

	ln, port, err := listenWithFallback(busyPort, []int{0})
	if err != nil {
		t.Fatalf("expected fallback to an ephemeral port, got error: %v", err)
	}
	defer ln.Close()

	if port == busyPort {
		t.Fatalf("bound the busy port %d instead of falling back", busyPort)
	}
	if port <= 0 {
		t.Fatalf("port = %d, want a valid ephemeral port", port)
	}
}

func TestListenWithFallback_SkipsBusyFallback(t *testing.T) {
	busy, busyPort := occupyPort(t)
	defer busy.Close()

	ln, port, err := listenWithFallback(0, []int{busyPort, 0})
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	defer ln.Close()

	if port == busyPort {
		t.Fatalf("bound the busy fallback port %d", busyPort)
	}
}

func TestListenWithFallback_UsesEasyPortPool(t *testing.T) {
	ln, port, err := listenWithFallback(0, easyPorts)
	if err != nil {
		// All nine ports genuinely occupied on this host (rare); production would fall
		// through to an ephemeral port, which is covered separately.
		t.Skipf("all easy ports busy: %v", err)
	}
	defer ln.Close()

	for _, candidate := range easyPorts {
		if port == candidate {
			return
		}
	}
	t.Fatalf("port %d is not in the easy port pool %v", port, easyPorts)
}

func TestListenWithFallback_AllBusy(t *testing.T) {
	busy, busyPort := occupyPort(t)
	defer busy.Close()

	ln, port, err := listenWithFallback(0, []int{busyPort})
	if err == nil {
		if ln != nil {
			ln.Close()
		}
		t.Fatalf("expected error when every candidate is busy, got port %d", port)
	}
}
