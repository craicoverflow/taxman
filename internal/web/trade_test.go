package web

import (
	"database/sql"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/craicoverflow/taxman/internal/aliases"
	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/ledger"
)

func newTradeRequest(fields map[string]string) *http.Request {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/trade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// seedAAPLHolding imports one AAPL buy (10 units at 10 EUR, 2023-01-01)
// and classifies the holding as a CGT asset, so a manual trade has an
// existing holding to land on.
func seedAAPLHolding(t *testing.T, conn *sql.DB) {
	t.Helper()
	_, err := ledger.NewStore(conn).Insert(ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2023-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("seeding AAPL buy: %v", err)
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("classifying AAPL: %v", err)
	}
}

func countTransactions(t *testing.T, conn *sql.DB) int {
	t.Helper()
	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	return len(txs)
}

func TestHandler_Trade_POST_SellOnExistingHolding_StoresManualSellAndComputesCGT(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "sell", "instrument": "AAPL", "date": "2024-06-01",
		"quantity": "10", "price": "1000", "currency": "EUR",
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var manual []ledger.Transaction
	for _, tx := range txs {
		if tx.Platform == ledger.PlatformManual {
			manual = append(manual, tx)
		}
	}
	if len(manual) != 1 {
		t.Fatalf("manual transactions = %d, want 1; all: %+v", len(manual), txs)
	}
	if manual[0].Type != ledger.TypeSell || manual[0].Instrument != "AAPL" || manual[0].Currency != "EUR" {
		t.Errorf("stored trade = %+v, want a manual EUR sell of AAPL", manual[0])
	}

	// Same figures as twoYearCGTLedger's AAPL disposal: gain 9900,
	// taxable 8630, tax 2847.9 -> €2,847.00.
	if body := dashboardBody(t, conn, "/?year=2024"); !strings.Contains(body, "€2,847.00") {
		t.Errorf("expected the manual sell to produce €2,847.00 of CGT, got: %s", body)
	}
}

func TestHandler_Trade_POST_BuyThenSell_ComputesCGTAcrossBothLots(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	handler := NewServer(conn).Handler()

	for _, req := range []*http.Request{
		newTradeRequest(map[string]string{
			"kind": "buy", "instrument": "AAPL", "date": "2023-02-01",
			"quantity": "10", "price": "10", "currency": "EUR",
		}),
		newTradeRequest(map[string]string{
			"kind": "sell", "instrument": "AAPL", "date": "2024-06-01",
			"quantity": "20", "price": "1000", "currency": "EUR",
		}),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
		}
	}

	// Gain 20000 - 200 = 19800, taxable 18530, tax 6114.9 -> €6,114.00.
	if body := dashboardBody(t, conn, "/?year=2024"); !strings.Contains(body, "€6,114.00") {
		t.Errorf("expected €6,114.00 of CGT across both lots, got: %s", body)
	}
}

func TestHandler_Trade_POST_Oversell_IsRefusedAndWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "sell", "instrument": "AAPL", "date": "2024-06-01",
		"quantity": "10.5", "price": "1000", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("body = %q, want it to explain the shortfall", rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d after a refused oversell", got, before)
	}
}

func TestHandler_Trade_POST_SellDatedBeforeTheBuy_IsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn) // the only lot is acquired 2023-01-01
	before := countTransactions(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "sell", "instrument": "AAPL", "date": "2022-12-31",
		"quantity": "1", "price": "1000", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d", got, before)
	}
}

func TestHandler_Trade_POST_InvalidField_ReturnsErrorAndWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)
	handler := NewServer(conn).Handler()

	ok := map[string]string{
		"kind": "buy", "instrument": "AAPL", "date": "2024-06-01",
		"quantity": "1", "price": "10", "currency": "EUR",
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		maps.Copy(m, ok)
		m[k] = v
		return m
	}

	for name, fields := range map[string]map[string]string{
		"bad kind":           with("kind", "dividend"),
		"missing kind":       with("kind", ""),
		"missing instrument": with("instrument", ""),
		"bad date":           with("date", "nope"),
		"negative quantity":  with("quantity", "-1"),
		"zero price":         with("price", "0"),
		"missing currency":   with("currency", ""),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newTradeRequest(fields))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d after rejected requests", got, before)
	}
}

func TestHandler_Trade_POST_UnknownInstrument_IsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "buy", "instrument": "NOSUCH", "date": "2024-06-01",
		"quantity": "1", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d", got, before)
	}
}

func TestHandler_Trade_POST_InterestAccountIsNotAHolding(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	// A hand-entered interest credit makes "N26" a ledger instrument,
	// but a savings account is not a security.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequestFromSource("N26", "2024-05-01", "12.50"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("seeding interest: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	before := countTransactions(t, conn)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "buy", "instrument": "N26", "date": "2024-06-01",
		"quantity": "1", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d", got, before)
	}
}

func TestHandler_Trade_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trade", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Dashboard_ShowsTradeFormWithHoldingPicker(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)

	body := dashboardBody(t, conn, "/")
	for _, want := range []string{
		`action="/trade"`, `name="kind"`, `name="instrument"`, `name="date"`,
		`name="quantity"`, `name="price"`, `name="currency"`,
		`<option value="AAPL">`,
		"Fees not included",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the dashboard to include %q", want)
		}
	}
}

// --- New instrument with an explicit classification (SPEC.md §18) ---

func TestHandler_Trade_POST_NewInstrument_WritesTradeAndClassificationTogether(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	for _, key := range []string{"IE00B4L5Y983", "VWCE"} { // an ISIN and a ticker are both fine
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newTradeRequest(map[string]string{
			"kind": "buy", "new_instrument": " " + key + " ", "classification": "EXIT_TAX_FUND",
			"date": "2024-06-01", "quantity": "3", "price": "112.40", "currency": "EUR",
		}))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, want %d; body: %s", key, rec.Code, http.StatusSeeOther, rec.Body.String())
		}

		got, err := classify.NewStore(conn).Classify(key)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if got != classify.ExitTaxFund {
			t.Errorf("%s: classification = %q, want EXIT_TAX_FUND", key, got)
		}
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 2 || txs[0].Platform != ledger.PlatformManual || txs[0].Type != ledger.TypeBuy {
		t.Fatalf("transactions = %+v, want two manual buys", txs)
	}
	unclassified, err := classify.NewStore(conn).ListUnclassified()
	if err != nil {
		t.Fatalf("ListUnclassified: %v", err)
	}
	if len(unclassified) != 0 {
		t.Errorf("unclassified = %v, want none: a new instrument must arrive classified", unclassified)
	}
}

func TestHandler_Trade_POST_NewInstrument_WithoutValidClassification_IsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	for name, class := range map[string]string{
		"missing":      "",
		"unclassified": "UNCLASSIFIED",
		"unknown":      "ETF",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newTradeRequest(map[string]string{
			"kind": "buy", "new_instrument": "VWCE", "classification": class,
			"date": "2024-06-01", "quantity": "3", "price": "112.40", "currency": "EUR",
		}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", name, rec.Code, rec.Body.String())
		}
	}
	if got := countTransactions(t, conn); got != 0 {
		t.Errorf("transactions = %d, want 0", got)
	}
	if got, _ := classify.NewStore(conn).Classify("VWCE"); got != classify.Unclassified {
		t.Errorf("classification = %q, want none recorded", got)
	}
}

func TestHandler_Trade_POST_NewInstrument_FirstTradeCannotBeASell(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "sell", "new_instrument": "VWCE", "classification": "CGT_ASSET",
		"date": "2024-06-01", "quantity": "1", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != 0 {
		t.Errorf("transactions = %d, want 0", got)
	}
	if got, _ := classify.NewStore(conn).Classify("VWCE"); got != classify.Unclassified {
		t.Errorf("classification = %q: a refused trade must not leave one behind", got)
	}
}

func TestHandler_Trade_POST_ClassificationOnKnownHolding_IsRefusedNotOverwritten(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn) // CGT_ASSET
	before := countTransactions(t, conn)

	for name, fields := range map[string]map[string]string{
		"picked from the list": {"instrument": "AAPL"},
		"typed as new":         {"new_instrument": "AAPL"},
	} {
		fields["kind"], fields["classification"] = "buy", "EXIT_TAX_FUND"
		fields["date"], fields["quantity"], fields["price"], fields["currency"] = "2024-06-01", "1", "10", "EUR"

		rec := httptest.NewRecorder()
		NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(fields))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", name, rec.Code, rec.Body.String())
		}
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d", got, before)
	}
	if got, _ := classify.NewStore(conn).Classify("AAPL"); got != classify.CGTAsset {
		t.Errorf("classification = %q, want it left as CGT_ASSET", got)
	}
}

func TestHandler_Trade_POST_NewInstrument_AliasKeyIsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	if err := aliases.NewStore(conn).Merge("US0378331005", "AAPL"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "buy", "new_instrument": "US0378331005", "classification": "CGT_ASSET",
		"date": "2024-06-01", "quantity": "1", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AAPL") {
		t.Errorf("body = %q, want it to point at the canonical key AAPL", rec.Body.String())
	}
}

func TestHandler_Dashboard_TradeForm_OffersNewInstrumentAndClassification(t *testing.T) {
	conn := openMigratedTestDB(t)
	body := dashboardBody(t, conn, "/")
	for _, want := range []string{`name="new_instrument"`, `name="classification"`, `value="CGT_ASSET"`, `value="EXIT_TAX_FUND"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the dashboard (even with no holdings yet) to include %q", want)
		}
	}
}

// --- Batch grid (SPEC.md §18) ---

// tradeFields are the grid's columns. A browser submits every column of
// every row, so the parallel slices always stay aligned.
var tradeFields = []string{"kind", "instrument", "new_instrument", "classification", "date", "quantity", "price", "currency"}

// newTradeBatchRequest posts one grid row per map, filling absent
// columns with "" exactly as the browser does for untouched inputs.
func newTradeBatchRequest(rows ...map[string]string) *http.Request {
	form := url.Values{}
	for _, row := range rows {
		for _, f := range tradeFields {
			form.Add(f, row[f])
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/trade", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func tradeRow(kind, instrument, date, qty, price string) map[string]string {
	return map[string]string{
		"kind": kind, "instrument": instrument, "date": date,
		"quantity": qty, "price": price, "currency": "EUR",
	}
}

func TestHandler_Trade_POST_Batch_InsertsEveryRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	newETF := map[string]string{
		"kind": "buy", "new_instrument": "IE00B4L5Y983", "classification": "EXIT_TAX_FUND",
		"date": "2024-02-01", "quantity": "5", "price": "80", "currency": "EUR",
	}
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(
		tradeRow("buy", "AAPL", "2023-02-01", "10", "10"),
		newETF,
		tradeRow("sell", "AAPL", "2024-06-01", "20", "1000"),
	))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before+3 {
		t.Errorf("transactions = %d, want %d", got, before+3)
	}
	if got, _ := classify.NewStore(conn).Classify("IE00B4L5Y983"); got != classify.ExitTaxFund {
		t.Errorf("ETF classification = %q, want EXIT_TAX_FUND", got)
	}
	// The batch's buy and sell of AAPL net out as in the single-row case.
	if body := dashboardBody(t, conn, "/?year=2024"); !strings.Contains(body, "€6,114.00") {
		t.Errorf("expected €6,114.00 of CGT from the batch, got: %s", body)
	}
}

func TestHandler_Trade_POST_Batch_AnyInvalidRowWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	newETF := map[string]string{
		"kind": "buy", "new_instrument": "VWCE", "classification": "EXIT_TAX_FUND",
		"date": "2024-02-01", "quantity": "5", "price": "80", "currency": "EUR",
	}
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(
		tradeRow("buy", "AAPL", "2023-02-01", "10", "10"),
		newETF,
		tradeRow("buy", "AAPL", "not-a-date", "1", "10"), // row 3 is bad
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "row 3") {
		t.Errorf("body = %q, want it to name row 3", rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d: a bad row must reject the whole batch", got, before)
	}
	if got, _ := classify.NewStore(conn).Classify("VWCE"); got != classify.Unclassified {
		t.Errorf("classification = %q: a rejected batch must not record one", got)
	}
}

func TestHandler_Trade_POST_Batch_BlankRowsAreSkipped(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	blank := map[string]string{"kind": "buy"} // the select's default; nothing else touched
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(
		blank,
		tradeRow("buy", "AAPL", "2023-02-01", "1", "10"),
		blank,
	))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before+1 {
		t.Errorf("transactions = %d, want %d", got, before+1)
	}
}

func TestHandler_Trade_POST_Batch_NoFilledRowsIsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(map[string]string{"kind": "buy"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Trade_POST_Batch_HalfFilledRowIsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	before := countTransactions(t, conn)

	half := tradeRow("buy", "AAPL", "2024-01-01", "1", "") // no price
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(tradeRow("buy", "AAPL", "2023-02-01", "1", "10"), half))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != before {
		t.Errorf("transactions = %d, want %d", got, before)
	}
}

func TestHandler_Trade_POST_Batch_OversellIsCheckedInDateOrder(t *testing.T) {
	cases := []struct {
		name     string
		sellDate string
		wantCode int
	}{
		{"sell after the same-batch buy is accepted", "2024-03-01", http.StatusSeeOther},
		{"sell before the same-batch buy is refused", "2024-01-01", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := openMigratedTestDB(t)
			newETF := map[string]string{
				"kind": "buy", "new_instrument": "VWCE", "classification": "CGT_ASSET",
				"date": "2024-02-01", "quantity": "5", "price": "80", "currency": "EUR",
			}
			sell := map[string]string{
				"kind": "sell", "new_instrument": "VWCE", "date": tc.sellDate,
				"quantity": "5", "price": "90", "currency": "EUR",
			}
			// The sell is listed first on purpose: list order must not matter.
			rec := httptest.NewRecorder()
			NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(sell, newETF))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			wantTxs := 0
			if tc.wantCode == http.StatusSeeOther {
				wantTxs = 2
			}
			if got := countTransactions(t, conn); got != wantTxs {
				t.Errorf("transactions = %d, want %d", got, wantTxs)
			}
		})
	}
}

func TestHandler_Trade_POST_Batch_SecondRowOfANewInstrumentMayOmitItsClassification(t *testing.T) {
	conn := openMigratedTestDB(t)
	first := map[string]string{
		"kind": "buy", "new_instrument": "VWCE", "classification": "CGT_ASSET",
		"date": "2024-02-01", "quantity": "5", "price": "80", "currency": "EUR",
	}
	second := map[string]string{
		"kind": "buy", "new_instrument": "VWCE",
		"date": "2024-03-01", "quantity": "5", "price": "85", "currency": "EUR",
	}
	conflicting := map[string]string{
		"kind": "buy", "new_instrument": "VWCE", "classification": "EXIT_TAX_FUND",
		"date": "2024-03-01", "quantity": "5", "price": "85", "currency": "EUR",
	}

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(first, conflicting))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("conflicting classification: status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != 0 {
		t.Fatalf("transactions = %d, want 0 after the refused batch", got)
	}

	rec = httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, newTradeBatchRequest(first, second))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("omitted classification: status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := countTransactions(t, conn); got != 2 {
		t.Errorf("transactions = %d, want 2", got)
	}
}

func TestHandler_Dashboard_TradeForm_IsAGridWithAddRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedAAPLHolding(t, conn)
	body := dashboardBody(t, conn, "/")
	for _, want := range []string{`id="trade-batch"`, `addTradeRow()`, "Add row"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the dashboard to include %q", want)
		}
	}
	if n := strings.Count(body, `name="quantity"`); n < 1 {
		t.Errorf("expected at least one trade grid row, found %d quantity inputs", n)
	}
}
