package fx

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ecbHistZipURL is the ECB's full daily history, the same series
// embedded in eurofxref-hist.csv. See docs/adr/0002-fx-runtime-refresh.md.
const ecbHistZipURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.zip"

// offlineEnvVar, when set to a non-empty value, disables live refresh
// entirely: Rate() then behaves exactly as it did before runtime
// refresh existed — a stale lookup is always an error naming the
// manual-refresh steps in internal/fx/doc.go.
const offlineEnvVar = "TAXMAN_FX_OFFLINE"

// fetch is overridden by tests so they never make a real network call.
var fetch func(ctx context.Context) (string, error) = fetchLive

// refreshCooldown limits how often a stale lookup is allowed to
// trigger a real fetch — a batch of disposals on the same stale date
// would otherwise fire one HTTP request per transaction.
const refreshCooldown = time.Minute

var (
	refreshMu   sync.Mutex
	lastAttempt time.Time
)

// tryLiveRefresh makes one best-effort attempt to replace the active
// dataset with a freshly downloaded ECB history. It reports whether
// the active dataset changed. Failures (offline, ECB unreachable, a
// malformed download) are logged to stderr and otherwise swallowed —
// the caller falls back to its existing stale-data error.
func tryLiveRefresh() bool {
	if os.Getenv(offlineEnvVar) != "" {
		return false
	}

	refreshMu.Lock()
	defer refreshMu.Unlock()
	if time.Since(lastAttempt) < refreshCooldown {
		return false
	}
	lastAttempt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	csvText, err := fetch(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fx: live refresh of ECB reference rates failed: %v\n", err)
		return false
	}

	nd, err := parseHistCSV(csvText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fx: downloaded ECB reference rates were unusable: %v\n", err)
		return false
	}

	mu.RLock()
	current := data
	mu.RUnlock()
	if current != nil && !nd.lastDate.After(current.lastDate) {
		// Downloaded data is no newer than what's already active
		// (e.g. the ECB hasn't published today's rate yet).
		return false
	}

	nd.source = fmt.Sprintf("ECB eurofxref-hist (live refresh) through %s", nd.lastDate.Format(dateLayout))
	mu.Lock()
	data = nd
	mu.Unlock()

	fmt.Fprintf(os.Stderr, "fx: refreshed ECB reference rates through %s\n", nd.lastDate.Format(dateLayout))
	persistCache(csvText)
	return true
}

// fetchLive downloads the ECB's full history zip and returns the CSV
// it contains.
func fetchLive(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ecbHistZipURL, nil)
	if err != nil {
		return "", fmt.Errorf("fx: building request for %s: %w", ecbHistZipURL, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fx: fetching %s: %w", ecbHistZipURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fx: fetching %s: unexpected status %s", ecbHistZipURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // the zip is ~640KB; guard against a runaway response
	if err != nil {
		return "", fmt.Errorf("fx: reading %s: %w", ecbHistZipURL, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("fx: unzipping %s: %w", ecbHistZipURL, err)
	}
	for _, f := range zr.File {
		if f.Name != "eurofxref-hist.csv" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("fx: opening eurofxref-hist.csv in downloaded zip: %w", err)
		}
		defer func() { _ = rc.Close() }()
		csvBytes, err := io.ReadAll(rc)
		if err != nil {
			return "", fmt.Errorf("fx: reading eurofxref-hist.csv from downloaded zip: %w", err)
		}
		return string(csvBytes), nil
	}
	return "", fmt.Errorf("fx: eurofxref-hist.csv not found in downloaded zip")
}

// userCacheDir is os.UserCacheDir, indirected so tests can point it at
// a throwaway directory instead of the real per-user cache.
var userCacheDir = os.UserCacheDir

// cachePath is where a live-refreshed CSV is persisted so the next
// process start picks it up without another network round trip. A
// failure to determine or use it is never fatal — caching is
// best-effort.
func cachePath() (string, error) {
	dir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "taxman", "eurofxref-hist.csv"), nil
}

func persistCache(csvText string) {
	path, err := cachePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(csvText), 0o644)
}

// loadCachedRefresh reads back a previously persisted live refresh, if
// any. A missing or unusable cache file is not an error — the embedded
// data stands on its own.
func loadCachedRefresh() (*dataset, error) {
	path, err := cachePath()
	if err != nil {
		return nil, nil //nolint:nilerr // no cache dir is not a failure
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil //nolint:nilerr // no cache file yet is not a failure
	}
	d, err := parseHistCSV(string(raw))
	if err != nil {
		return nil, nil //nolint:nilerr // a corrupt cache file is not a failure; embedded data still works
	}
	d.source = fmt.Sprintf("ECB eurofxref-hist (cached live refresh) through %s", d.lastDate.Format(dateLayout))
	return d, nil
}
