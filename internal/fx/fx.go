package fx

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

//go:embed eurofxref-hist.csv
var histCSV string

const dateLayout = "2006-01-02"

// maxLookback bounds how far before a requested date fx will reach for
// a reference rate. The ECB only publishes on TARGET business days, so
// a weekend or holiday date has to resolve to an earlier one — the
// longest closure is the Christmas/New-Year break (~4 days). A gap
// wider than this means there is no usable rate near that date: a
// currency that has since been retired (its column goes "N/A"), or a
// data file that is badly out of date. Either way that's an error,
// not a silent reach back to a months-old rate.
const maxLookback = 10 * 24 * time.Hour

// staleGrace bounds how far past the last published date a lookup may
// still resolve (to that last date). It covers a transaction dated a
// day or two before the data has been refreshed; it is not wide enough
// to mask a file that is genuinely stale.
const staleGrace = 7 * 24 * time.Hour

// dayRates holds one publication day's rates. A currency the ECB
// reported as "N/A" that day is simply absent from the map.
type dayRates struct {
	date  time.Time
	rates map[string]decimal.Decimal // ISO 4217 -> units of that currency per euro
}

// dataset is one parsed ECB reference-rate series plus a description
// of where it came from, for Source(). The active dataset starts as
// the embedded eurofxref-hist.csv and can be replaced at runtime by a
// live refresh (see refresh.go) once a lookup finds it stale.
type dataset struct {
	parsed    []dayRates // ascending by date
	known     map[string]bool
	firstDate time.Time
	lastDate  time.Time
	source    string
}

var (
	loadOnce sync.Once
	mu       sync.RWMutex // guards data; swapped wholesale by a live refresh
	data     *dataset
	parseErr error
)

func loadEmbedded() {
	d, err := parseHistCSV(histCSV)
	if err != nil {
		parseErr = err
		return
	}
	d.source = fmt.Sprintf("ECB eurofxref-hist (embedded) through %s", d.lastDate.Format(dateLayout))
	data = d

	// A prior process may already have fetched a fresher file than the
	// one embedded in this binary; pick it up without a network call.
	if cached, err := loadCachedRefresh(); err == nil && cached != nil && cached.lastDate.After(d.lastDate) {
		data = cached
	}
}

// parseHistCSV parses eurofxref-hist.csv's format: a header row of
// "Date" followed by one ISO 4217 column per currency, then one row
// per TARGET publication day, each cell "units of that currency per
// euro" or "N/A".
func parseHistCSV(csvText string) (*dataset, error) {
	r := csv.NewReader(strings.NewReader(csvText))
	r.FieldsPerRecord = -1 // trailing comma on every line -> ragged

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("fx: parsing eurofxref-hist.csv: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("fx: eurofxref-hist.csv has no data rows")
	}

	// Header: "Date" then one ISO code per column, with a trailing
	// empty field from the line's trailing comma.
	header := records[0]
	codeByCol := map[int]string{}
	known := map[string]bool{}
	for i := 1; i < len(header); i++ {
		code := strings.TrimSpace(header[i])
		if code == "" {
			continue
		}
		codeByCol[i] = code
		known[code] = true
	}

	out := make([]dayRates, 0, len(records)-1)
	for _, rec := range records[1:] {
		if len(rec) == 0 || strings.TrimSpace(rec[0]) == "" {
			continue
		}
		d, err := time.Parse(dateLayout, strings.TrimSpace(rec[0]))
		if err != nil {
			return nil, fmt.Errorf("fx: eurofxref-hist.csv: bad date %q: %w", rec[0], err)
		}
		day := dayRates{date: d, rates: make(map[string]decimal.Decimal)}
		for col, code := range codeByCol {
			if col >= len(rec) {
				continue
			}
			v := strings.TrimSpace(rec[col])
			if v == "" || v == "N/A" {
				continue
			}
			rate, err := decimal.NewFromString(v)
			if err != nil {
				return nil, fmt.Errorf("fx: eurofxref-hist.csv: bad rate %q for %s on %s: %w", v, code, rec[0], err)
			}
			if !rate.IsPositive() {
				continue
			}
			day.rates[code] = rate
		}
		out = append(out, day)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fx: eurofxref-hist.csv has no usable data rows")
	}

	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return &dataset{
		parsed:    out,
		known:     known,
		firstDate: out[0].date,
		lastDate:  out[len(out)-1].date,
	}, nil
}

// Rate returns how many euro one unit of currency was worth on date,
// per the ECB euro foreign-exchange reference rates. currency is an
// ISO 4217 code; "" and "EUR" both return 1.
//
// The series starts from the eurofxref-hist.csv embedded in the
// binary. When date falls beyond what that data covers, Rate makes one
// best-effort live fetch of the current ECB history file before
// erroring (see refresh.go) — set TAXMAN_FX_OFFLINE=1 to disable this
// and get the old offline-only behaviour.
//
// A date on a weekend or TARGET holiday resolves to the most recent
// prior publication day (within maxLookback). Rate errors on: an
// unknown currency; a date before the series begins (1999); a date
// more than staleGrace past the last published day, live refresh
// included; or a currency with no published rate within maxLookback
// before date (a retired currency, or a stale data source).
func Rate(currency string, date time.Time) (decimal.Decimal, error) {
	loadOnce.Do(loadEmbedded)
	if parseErr != nil {
		return decimal.Zero, parseErr
	}

	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "EUR" {
		return decimal.NewFromInt(1), nil
	}

	mu.RLock()
	d := data
	mu.RUnlock()

	if !d.known[currency] {
		return decimal.Zero, fmt.Errorf("fx: unknown currency %q (not in the ECB reference set)", currency)
	}

	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if day.Before(d.firstDate) {
		return decimal.Zero, fmt.Errorf("fx: no ECB reference rate for %s on %s: the series begins %s",
			currency, day.Format(dateLayout), d.firstDate.Format(dateLayout))
	}
	if day.After(d.lastDate.Add(staleGrace)) {
		if refreshed := tryLiveRefresh(); refreshed {
			mu.RLock()
			d = data
			mu.RUnlock()
		}
	}
	if day.After(d.lastDate.Add(staleGrace)) {
		return decimal.Zero, fmt.Errorf("fx: %s ends at %s; no rate for %s on %s — refresh the data (set TAXMAN_FX_OFFLINE=1 to suppress live refresh attempts)",
			d.source, d.lastDate.Format(dateLayout), currency, day.Format(dateLayout))
	}

	// Clamp a within-grace future date back onto the last publication
	// day, then find the newest day on or before it.
	target := day
	if target.After(d.lastDate) {
		target = d.lastDate
	}
	after := sort.Search(len(d.parsed), func(i int) bool { return d.parsed[i].date.After(target) })
	limit := target.Add(-maxLookback)
	for i := after - 1; i >= 0 && !d.parsed[i].date.Before(limit); i-- {
		if ecb, ok := d.parsed[i].rates[currency]; ok {
			// eurofxref lists "1 EUR = ecb units of currency"; callers
			// want euro per unit.
			return decimal.NewFromInt(1).Div(ecb), nil
		}
	}

	return decimal.Zero, fmt.Errorf("fx: no ECB reference rate for %s within %d days before %s (retired currency, or the data source is out of date)",
		currency, int(maxLookback.Hours())/24, day.Format(dateLayout))
}

// Source is a short identifier for the active rate data, for audit
// records: which reference set produced a conversion, and how current
// it was.
func Source() string {
	loadOnce.Do(loadEmbedded)
	if parseErr != nil {
		return "ECB eurofxref-hist (unparseable)"
	}
	mu.RLock()
	defer mu.RUnlock()
	return data.source
}
