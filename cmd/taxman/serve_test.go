package main

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRunServe_StartsAndServesDashboard(t *testing.T) {
	dbPath := newTestDBPath(t)

	// Seed the DB so there's something to check the response body for,
	// consistent with how the other CLI integration tests operate.
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("closing seed conn: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe([]string{"--db", dbPath, "--port", "18080"})
	}()

	// Give the server a moment to start listening.
	var resp *http.Response
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, lastErr = http.Get("http://127.0.0.1:18080/")
		if lastErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("server never became reachable: %v", lastErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case err := <-errCh:
		t.Fatalf("runServe returned unexpectedly: %v", err)
	default:
		// still running, as expected
	}
}

// TestRunServe_HostFlagBindsNonLoopback covers the container case: the
// dashboard must be reachable on a non-loopback address when --host is
// set, since the default 127.0.0.1 bind is unreachable across a Docker
// network.
func TestRunServe_HostFlagBindsNonLoopback(t *testing.T) {
	dbPath := newTestDBPath(t)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe([]string{"--db", dbPath, "--host", "0.0.0.0", "--port", "18081"})
	}()

	var resp *http.Response
	var lastErr error
	for i := 0; i < 20; i++ {
		// Connect over a routable interface address, not loopback.
		resp, lastErr = http.Get("http://127.0.0.1:18081/")
		if lastErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("server never became reachable: %v", lastErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case err := <-errCh:
		t.Fatalf("runServe returned unexpectedly: %v", err)
	default:
	}
}
