package fx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubFetch swaps fetch for the duration of the test and resets the
// refresh cooldown so each test gets its own attempt, regardless of
// what ran before it. It also snapshots and restores the active
// dataset so a successful refresh in one test can't leak into another.
func stubFetch(t *testing.T, fn func(ctx context.Context) (string, error)) {
	t.Helper()
	loadOnce.Do(loadEmbedded) // ensure the embedded dataset is the baseline before we snapshot it

	orig := fetch
	fetch = fn
	t.Cleanup(func() { fetch = orig })

	// Never let a test touch the real per-user cache directory.
	dir := t.TempDir()
	origCacheDir := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = origCacheDir })

	refreshMu.Lock()
	lastAttempt = time.Time{}
	refreshMu.Unlock()

	mu.RLock()
	origData := data
	mu.RUnlock()
	t.Cleanup(func() {
		mu.Lock()
		data = origData
		mu.Unlock()
	})
}

func extendedCSV(t *testing.T, extraDate string) string {
	t.Helper()
	// eurofxref-hist.csv's real header plus one synthetic row far past
	// the embedded file's last date, so a refresh visibly moves
	// lastDate forward.
	return "Date,USD,\n" + extraDate + ",1.2000,\n2021-01-25,1.2152,\n"
}

func TestRate_StaleDate_LiveRefreshFillsTheGap(t *testing.T) {
	future := time.Now().AddDate(0, 0, 30).Format(dateLayout)
	stubFetch(t, func(ctx context.Context) (string, error) {
		return extendedCSV(t, future), nil
	})

	got, err := Rate("USD", mustDate(t, future))
	if err != nil {
		t.Fatalf("Rate after live refresh: %v", err)
	}
	if !got.Equal(eurPerUnit("1.2000")) {
		t.Errorf("Rate(USD, %s) = %s, want the refreshed rate", future, got)
	}
	if got := Source(); !strings.Contains(got, "live refresh") {
		t.Errorf("Source() = %q, want it to mention the live refresh", got)
	}
}

func TestRate_StaleDate_RefreshFails_StillErrors(t *testing.T) {
	stubFetch(t, func(ctx context.Context) (string, error) {
		return "", errors.New("network unreachable")
	})

	_, err := Rate("USD", mustDate(t, "2099-01-01"))
	if err == nil {
		t.Fatal("expected an error when live refresh fails and no data covers the date")
	}
}

func TestRate_Offline_SkipsLiveRefresh(t *testing.T) {
	called := false
	stubFetch(t, func(ctx context.Context) (string, error) {
		called = true
		return "", errors.New("must not be called")
	})
	t.Setenv(offlineEnvVar, "1")

	if _, err := Rate("USD", mustDate(t, "2099-01-01")); err == nil {
		t.Fatal("expected an error for a date past the last publication")
	}
	if called {
		t.Error("fetch was called despite TAXMAN_FX_OFFLINE being set")
	}
}

func TestRate_Refresh_DoesNotHammerOnRepeatedCalls(t *testing.T) {
	calls := 0
	stubFetch(t, func(ctx context.Context) (string, error) {
		calls++
		return "", errors.New("still unreachable")
	})

	for range 5 {
		_, _ = Rate("USD", mustDate(t, "2099-01-01"))
	}
	if calls != 1 {
		t.Errorf("fetch was called %d times, want 1 (cooldown should suppress the rest)", calls)
	}
}

func TestRate_Refresh_OlderOrEqualData_IsIgnored(t *testing.T) {
	mu.RLock()
	before := data
	mu.RUnlock()

	stubFetch(t, func(ctx context.Context) (string, error) {
		// Same-or-older last date as whatever is already active.
		return extendedCSV(t, before.lastDate.Format(dateLayout)), nil
	})

	_, _ = Rate("USD", mustDate(t, "2099-01-01"))

	mu.RLock()
	after := data
	mu.RUnlock()
	if after.source != before.source {
		t.Errorf("active dataset changed to %q despite the refresh not being newer", after.source)
	}
}
