package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/valuations"
)

// seedFundLot inserts a single fund purchase old enough to have passed
// an 8-year deemed-disposal anniversary, classified EXIT_TAX_FUND.
func seedFundLot(t *testing.T, conn *sql.DB) {
	t.Helper()
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2016-03-15"), Instrument: "IE_ETF",
		Quantity: mustDecimal(t, "100"), Price: decimalTen(t), Currency: "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if err := classify.NewStore(conn).Set("IE_ETF", classify.ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func TestHandler_Dashboard_ReachedAnniversaryWithNoValue_IsFlaggedNotGuessed(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()

	// The schedule names the anniversary and asks for a value...
	if !strings.Contains(body, "2024-03-15") {
		t.Error("expected the 2024-03-15 anniversary to be listed on the dashboard")
	}
	if !strings.Contains(body, "needs a market value") {
		t.Error("expected the reached anniversary to be flagged as needing a market value")
	}
	// ...and the holding itself reports blocked rather than showing a
	// tax figure derived from a guessed value.
	if !strings.Contains(body, "no market value is on record") {
		t.Error("expected the holding row to carry the engine's blocking error")
	}
}

func TestHandler_Valuation_POST_ThenHoldingComputes(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn)
	handler := NewServer(conn).Handler()

	form := url.Values{
		"instrument": {"IE_ETF"},
		"date":       {"2024-03-15"},
		"value":      {"18"},
		"currency":   {"EUR"},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, postForm("/valuations", form))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	stored, ok, err := valuations.NewStore(conn).Get("IE_ETF", mustDate(t, "2024-03-15"))
	if err != nil || !ok {
		t.Fatalf("Get after POST = (ok %v, err %v), want stored", ok, err)
	}
	if !stored.ValuePerUnit.Equal(mustDecimal(t, "18")) {
		t.Errorf("stored value = %s, want 18", stored.ValuePerUnit)
	}

	// With the value on record the deemed disposal computes:
	// 100 × (18 − 10) = 800 gain, at 41% = 328.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=2024", nil))
	body := rec.Body.String()
	if strings.Contains(body, "no market value is on record") {
		t.Error("expected the holding to compute once the value was entered")
	}
	if !strings.Contains(body, "328") {
		t.Errorf("expected the €328 deemed-disposal charge in the dashboard body, got: %s", body)
	}
	if !strings.Contains(body, "deemed disposal(s)") {
		t.Error("expected the holding row to note the deemed disposal")
	}
}

func TestHandler_Valuation_POST_RejectsBadInput(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn)
	handler := NewServer(conn).Handler()

	cases := map[string]url.Values{
		"no instrument":  {"date": {"2024-03-15"}, "value": {"18"}},
		"bad date":       {"instrument": {"IE_ETF"}, "date": {"15/03/2024"}, "value": {"18"}},
		"bad value":      {"instrument": {"IE_ETF"}, "date": {"2024-03-15"}, "value": {"eighteen"}},
		"zero value":     {"instrument": {"IE_ETF"}, "date": {"2024-03-15"}, "value": {"0"}},
		"negative value": {"instrument": {"IE_ETF"}, "date": {"2024-03-15"}, "value": {"-18"}},
	}
	for name, form := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, postForm("/valuations", form))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, http.StatusBadRequest)
		}
	}

	// Nothing was written by any of them.
	all, err := valuations.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected no valuations stored after rejected submissions, got %d", len(all))
	}
}

func TestHandler_Valuation_POST_Twice_Corrects(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn)
	handler := NewServer(conn).Handler()

	for _, value := range []string{"18", "17.50"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, postForm("/valuations", url.Values{
			"instrument": {"IE_ETF"}, "date": {"2024-03-15"}, "value": {value},
		}))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST %s: status = %d", value, rec.Code)
		}
	}

	all, err := valuations.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected the second entry to correct the first, got %d valuations", len(all))
	}
	if !all[0].ValuePerUnit.Equal(mustDecimal(t, "17.50")) {
		t.Errorf("value = %s, want the corrected 17.50", all[0].ValuePerUnit)
	}
}

func TestHandler_Valuation_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/valuations", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func postForm(path string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// anniversaryFormCount counts the value-entry forms rendered in the
// deemed-disposal schedule. Every such form posts to /valuations, and
// nothing else on the dashboard does.
func anniversaryFormCount(body string) int {
	return strings.Count(body, `action="/valuations"`)
}

func TestHandler_Dashboard_UpcomingAnniversary_IsListedWithoutAnEntryForm(t *testing.T) {
	conn := openMigratedTestDB(t)

	// Bought in 2022: the first anniversary falls in 2030, years away.
	// It belongs on the schedule so it can be seen coming, but a value
	// cannot be entered for a date that has not happened.
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2022-06-01"), Instrument: "IE_ETF",
		Quantity: mustDecimal(t, "50"), Price: mustDecimal(t, "20"), Currency: "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if err := classify.NewStore(conn).Set("IE_ETF", classify.ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "2030-06-01") {
		t.Error("expected the upcoming 2030 anniversary to be listed")
	}
	if !strings.Contains(body, "upcoming") {
		t.Error("expected the upcoming anniversary to be marked as such")
	}
	if n := anniversaryFormCount(body); n != 0 {
		t.Errorf("expected no value-entry form for an anniversary that has not fallen due, got %d", n)
	}
}

func TestHandler_Dashboard_EntryFormAppearsOnlyOnTheDueAnniversary(t *testing.T) {
	conn := openMigratedTestDB(t)

	// Two lots: 2016 (due 2024) and 2022 (due 2030). Viewing all
	// years, exactly one anniversary has fallen due, so exactly one
	// form should render — not one per row.
	store := ledger.NewStore(conn)
	for _, tx := range []ledger.Transaction{
		{
			Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
			Date: mustDate(t, "2016-03-15"), Instrument: "IE_ETF",
			Quantity: mustDecimal(t, "100"), Price: decimalTen(t), Currency: "EUR",
		},
		{
			Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
			Date: mustDate(t, "2022-06-01"), Instrument: "IE_ETF",
			Quantity: mustDecimal(t, "50"), Price: mustDecimal(t, "20"), Currency: "EUR",
		},
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("inserting %s: %v", tx.Date.Format("2006-01-02"), err)
		}
	}
	if err := classify.NewStore(conn).Set("IE_ETF", classify.ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "2030-06-01") {
		t.Error("expected the upcoming 2030 anniversary to still be listed")
	}
	if n := anniversaryFormCount(body); n != 1 {
		t.Errorf("expected exactly 1 value-entry form (the due 2024 anniversary), got %d", n)
	}
}

func TestHandler_Dashboard_ScheduleIsScopedToTheSelectedYear(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn) // bought 2016-03-15, anniversary 2024-03-15
	handler := NewServer(conn).Handler()

	// Viewing 2020: the 2024 anniversary has not been reached at that
	// point, so the year's figures neither need nor use its value. It
	// must not be demanded here — the schedule is built at the same
	// point in time the liability above it was.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=2020", nil))
	body := rec.Body.String()
	if strings.Contains(body, "needs a market value") {
		t.Error("viewing 2020: expected no market value to be demanded for an anniversary that falls in 2024")
	}
	if n := anniversaryFormCount(body); n != 0 {
		t.Errorf("viewing 2020: expected no value-entry form, got %d", n)
	}

	// Viewing 2024, the year it falls due: now it is asked for.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=2024", nil))
	body = rec.Body.String()
	if !strings.Contains(body, "needs a market value") {
		t.Error("viewing 2024: expected the due anniversary to ask for a market value")
	}
	if n := anniversaryFormCount(body); n != 1 {
		t.Errorf("viewing 2024: expected exactly 1 value-entry form, got %d", n)
	}
}
