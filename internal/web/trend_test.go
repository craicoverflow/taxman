package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/prices"
)

// Every figure below is fabricated; the scenarios reproduce structure
// (a mid-series buy, a full sale, a currency) only.

func trendTx(t *testing.T, typ ledger.Type, date, qty, price string) ledger.Transaction {
	t.Helper()
	return ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: typ, Date: mustDate(t, date),
		Instrument: "X", Quantity: mustDecimal(t, qty), Price: mustDecimal(t, price), Currency: "EUR",
	}
}

func closes(t *testing.T, pairs ...string) []eurClose {
	t.Helper()
	var out []eurClose
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, eurClose{Date: mustDate(t, pairs[i]), Price: mustDecimal(t, pairs[i+1])})
	}
	return out
}

// strs renders a series for comparison, "-" for a gap.
func strs(ns []*json.Number) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		if n == nil {
			parts[i] = "-"
		} else {
			parts[i] = n.String()
		}
	}
	return strings.Join(parts, " ")
}

func TestBuildTrend_PerHoldingAndTimeWeightedOverall(t *testing.T) {
	// A: 10 bought 01-02, price 10 → 11 → 12 (+10%, +20%).
	// B: 5 bought 01-03, price 20 → 18 on 01-04 (−10%).
	// Overall: 01-03 is A alone (+10%); on 01-04 A's +€10 and B's −€10
	// cancel, so the chained index stays at +10% — B's purchase itself
	// is new money, not a gain.
	got := buildTrend([]trendHolding{
		{Label: "A", Symbol: "AAA", Txs: []ledger.Transaction{trendTx(t, ledger.TypeBuy, "2024-01-02", "10", "10")},
			Closes: closes(t, "2024-01-02", "10", "2024-01-03", "11", "2024-01-04", "12")},
		{Label: "B", Symbol: "BBB", Txs: []ledger.Transaction{trendTx(t, ledger.TypeBuy, "2024-01-03", "5", "20")},
			Closes: closes(t, "2024-01-02", "20", "2024-01-03", "20", "2024-01-04", "18")},
	}, mustDate(t, "2024-01-10"))

	if d := strings.Join(got.Dates, " "); d != "2024-01-02 2024-01-03 2024-01-04" {
		t.Fatalf("dates = %s", d)
	}
	checks := []struct{ name, got, want string }{
		{"A pct", strs(got.Holdings[0].Pct), "0.00 10.00 20.00"},
		{"B pct", strs(got.Holdings[1].Pct), "- 0.00 -10.00"},
		{"overall pct", strs(got.Overall.Pct), "0.00 10.00 10.00"},
		{"overall value", strs(got.Overall.Value), "100.00 210.00 210.00"},
		{"overall cost", strs(got.Overall.Cost), "100.00 200.00 200.00"},
		{"B value", strs(got.Holdings[1].Value), "- 100.00 90.00"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if len(got.Notes) != 0 {
		t.Errorf("notes = %v, want none", got.Notes)
	}
}

func TestBuildTrend_FullySoldHoldingEndsAtSale(t *testing.T) {
	got := buildTrend([]trendHolding{{
		Label: "A",
		Txs: []ledger.Transaction{
			trendTx(t, ledger.TypeBuy, "2024-01-02", "10", "10"),
			trendTx(t, ledger.TypeSell, "2024-01-03", "10", "11"),
		},
		Closes: closes(t, "2024-01-02", "10", "2024-01-03", "11", "2024-01-04", "15"),
	}}, mustDate(t, "2024-01-10"))

	if s := strs(got.Holdings[0].Pct); s != "0.00 10.00 -" {
		t.Errorf("pct = %q: the line should stop on the sale day", s)
	}
	// The sale day's move (held overnight) counts; after that nothing is held.
	if s := strs(got.Overall.Pct); s != "0.00 10.00 -" {
		t.Errorf("overall pct = %q", s)
	}
}

func TestBuildTrend_PartialSaleDoesNotMovePriceLine(t *testing.T) {
	got := buildTrend([]trendHolding{{
		Label: "A",
		Txs: []ledger.Transaction{
			trendTx(t, ledger.TypeBuy, "2024-01-02", "10", "10"),
			trendTx(t, ledger.TypeSell, "2024-01-03", "4", "10"),
		},
		Closes: closes(t, "2024-01-02", "10", "2024-01-03", "10", "2024-01-04", "12"),
	}}, mustDate(t, "2024-01-10"))

	if s := strs(got.Overall.Pct); s != "0.00 0.00 20.00" {
		t.Errorf("overall pct = %q, want the market move only", s)
	}
	if s := strs(got.Overall.Value); s != "100.00 60.00 72.00" {
		t.Errorf("overall value = %q", s)
	}
	if s := strs(got.Overall.Cost); s != "100.00 60.00 60.00" {
		t.Errorf("overall cost = %q (FIFO open lots)", s)
	}
}

func TestBuildTrend_HistoryStartsAfterPurchase_Noted(t *testing.T) {
	got := buildTrend([]trendHolding{{
		Label:  "Old",
		Txs:    []ledger.Transaction{trendTx(t, ledger.TypeBuy, "2010-01-04", "1", "5")},
		Closes: closes(t, "2016-01-04", "8", "2016-01-05", "10"),
	}}, mustDate(t, "2016-02-01"))

	if s := strs(got.Holdings[0].Pct); s != "0.00 25.00" {
		t.Errorf("pct = %q, want it based at the first available close", s)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "price history starts 2016-01-04") {
		t.Errorf("notes = %v", got.Notes)
	}
}

func TestBuildTrend_NoHoldings_EmptyNotNil(t *testing.T) {
	b, err := json.Marshal(buildTrend(nil, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"dates":[]`) || !strings.Contains(string(b), `"holdings":[]`) {
		t.Errorf("json = %s, want empty arrays the page can iterate", b)
	}
}

// fakeHistorian serves canned series by symbol.
type fakeHistorian struct {
	series map[string]prices.History
	calls  []string
}

func (f *fakeHistorian) History(_ context.Context, symbol string) (prices.History, error) {
	f.calls = append(f.calls, symbol)
	h, ok := f.series[symbol]
	if !ok {
		return prices.History{}, fmt.Errorf("no history for %q", symbol)
	}
	return h, nil
}

func TestHandler_PortfolioTrend_JSON(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn) // AAPL (US0378331005) and VANGUARD (IE00B3RBWM25)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL")

	fh := &fakeHistorian{series: map[string]prices.History{
		"AAPL": {Symbol: "AAPL", Currency: "USD", Closes: []prices.Close{
			{Date: mustDate(t, "2023-01-02"), Price: decimal.NewFromInt(100)},
			{Date: mustDate(t, "2023-01-03"), Price: decimal.NewFromInt(110)},
		}},
	}}
	s := NewServer(conn)
	s.quoter = stubQuoter{}
	s.historian = fh
	s.rate = func(cur string, _ time.Time) (decimal.Decimal, error) {
		if cur != "USD" {
			return decimal.Zero, fmt.Errorf("unexpected currency %s", cur)
		}
		return decimal.RequireFromString("0.5"), nil
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio/trend.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got struct {
		Dates    []string `json:"dates"`
		Holdings []struct {
			Label string            `json:"label"`
			Pct   []json.RawMessage `json:"pct"`
			Value []json.RawMessage `json:"value"`
		} `json:"holdings"`
		Notes   []string       `json:"notes"`
		Missing []trendMissing `json:"missing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, rec.Body.String())
	}
	if strings.Join(got.Dates, ",") != "2023-01-02,2023-01-03" {
		t.Errorf("dates = %v", got.Dates)
	}
	if len(got.Holdings) != 1 || got.Holdings[0].Label != "APPLE INC" {
		t.Fatalf("holdings = %+v", got.Holdings)
	}
	// 10 held (the 2024 sale is after these dates) × $110 × 0.5 = €550.
	if v := string(got.Holdings[0].Value[1]); v != "550.00" {
		t.Errorf("value on 2023-01-03 = %s, want 550.00 (converted to EUR)", v)
	}
	if p := string(got.Holdings[0].Pct[1]); p != "10.00" {
		t.Errorf("pct on 2023-01-03 = %s, want 10.00", p)
	}
	if len(got.Missing) != 1 || got.Missing[0].Instrument != "IE00B3RBWM25" || got.Missing[0].Reason != "no ticker mapped" {
		t.Errorf("missing = %+v, want the unmapped holding listed for a mapping form", got.Missing)
	}
	if strings.Join(fh.calls, ",") != "AAPL" {
		t.Errorf("history requested for %v; only mapped symbols may be looked up", fh.calls)
	}
}

func TestHandler_PortfolioTrend_HistoryFailure_IsANoteNotAnError(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL")

	s := NewServer(conn)
	s.historian = &fakeHistorian{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio/trend.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var got trendSeries
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var apple *trendMissing
	for i := range got.Missing {
		if got.Missing[i].Instrument == "US0378331005" {
			apple = &got.Missing[i]
		}
	}
	if apple == nil || apple.Symbol != "AAPL" || !strings.Contains(apple.Reason, "no price history") {
		t.Errorf("missing = %+v, want AAPL listed with its symbol so it can be remapped", got.Missing)
	}
}

func TestHandler_PortfolioTrend_RejectsPOST(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/portfolio/trend.json", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandler_Portfolio_RendersTrendCard(t *testing.T) {
	conn := openMigratedTestDB(t)
	body := portfolioBody(t, conn, "/portfolio")
	for _, want := range []string{`id="portfolio-trend"`, `id="trend-missing"`, `fetch("/portfolio/trend.json"`, "chart.umd.min.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("portfolio page missing %q", want)
		}
	}
}
