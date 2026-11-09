package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func postTo(path string, fields map[string]string) *http.Request {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// seedManualBuyAndSell leaves the imported AAPL buy (10), a manual buy
// of 5 (2023-06-01) and a manual sell of 12 (2024-01-01): 15 held, 12
// sold. It returns the manual rows' ids.
func seedManualBuyAndSell(t *testing.T, conn *sql.DB) (buyID, sellID int64) {
	t.Helper()
	seedAAPLHolding(t, conn)
	handler := NewServer(conn).Handler()
	for _, f := range []map[string]string{
		{"kind": "buy", "instrument": "AAPL", "date": "2023-06-01", "quantity": "5", "price": "10", "currency": "EUR"},
		{"kind": "sell", "instrument": "AAPL", "date": "2024-01-01", "quantity": "12", "price": "20", "currency": "EUR"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newTradeRequest(f))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("seeding trade: status %d: %s", rec.Code, rec.Body.String())
		}
	}
	trades, err := ledger.NewStore(conn).ManualTrades()
	if err != nil || len(trades) != 2 {
		t.Fatalf("ManualTrades: %d rows, err %v", len(trades), err)
	}
	for _, tr := range trades {
		if tr.Type == ledger.TypeBuy {
			buyID = tr.ID
		} else {
			sellID = tr.ID
		}
	}
	return buyID, sellID
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

func TestHandler_TradeUpdate_CorrectsAManualTrade(t *testing.T) {
	conn := openMigratedTestDB(t)
	_, sellID := seedManualBuyAndSell(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/update", map[string]string{
		"id": id(sellID), "kind": "sell", "date": "2024-02-01",
		"quantity": "8", "price": "25", "currency": "EUR",
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	trades, _ := ledger.NewStore(conn).ManualTrades()
	var sell *ledger.StoredTrade
	for i := range trades {
		if trades[i].ID == sellID {
			sell = &trades[i]
		}
	}
	if sell == nil || !sell.Quantity.Equal(decimal.NewFromInt(8)) || sell.Instrument != "AAPL" {
		t.Errorf("sell not corrected (instrument must stay AAPL): %+v", sell)
	}
}

func TestHandler_TradeUpdate_OversellIsRefusedAndNothingChanges(t *testing.T) {
	conn := openMigratedTestDB(t)
	_, sellID := seedManualBuyAndSell(t, conn) // 15 held

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/update", map[string]string{
		"id": id(sellID), "kind": "sell", "date": "2024-01-01",
		"quantity": "16", "price": "20", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exceeds") {
		t.Fatalf("status = %d, body %q; want 400 naming the shortfall", rec.Code, rec.Body.String())
	}
	trades, _ := ledger.NewStore(conn).ManualTrades()
	for _, tr := range trades {
		if tr.ID == sellID && !tr.Quantity.Equal(decimal.NewFromInt(12)) {
			t.Errorf("sell changed despite refusal: %+v", tr)
		}
	}
}

func TestHandler_TradeUpdate_EditingTheBuyBelowTheSellIsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	buyID, _ := seedManualBuyAndSell(t, conn) // sell 12 needs the manual buy of 5 (10+5-12)

	// Shrink the buy to 1: 10+1 = 11 < 12 sold.
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/update", map[string]string{
		"id": id(buyID), "kind": "buy", "date": "2023-06-01",
		"quantity": "1", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_TradeUpdate_RejectsBadInputAndNonManualRows(t *testing.T) {
	conn := openMigratedTestDB(t)
	_, sellID := seedManualBuyAndSell(t, conn)
	valid := map[string]string{"kind": "sell", "date": "2024-02-01", "quantity": "1", "price": "1", "currency": "EUR"}
	with := func(over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range valid {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	imported := int64(1) // the seeded Degiro AAPL buy

	cases := []struct {
		name   string
		fields map[string]string
		want   int
	}{
		{"imported row", with(map[string]string{"id": id(imported), "kind": "buy"}), http.StatusNotFound},
		{"missing row", with(map[string]string{"id": "9999"}), http.StatusNotFound},
		{"bad id", with(map[string]string{"id": "x"}), http.StatusBadRequest},
		{"bad quantity", with(map[string]string{"id": id(sellID), "quantity": "0"}), http.StatusBadRequest},
		{"bad kind", with(map[string]string{"id": id(sellID), "kind": "hold"}), http.StatusBadRequest},
		{"no currency", with(map[string]string{"id": id(sellID), "currency": ""}), http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/update", tt.fields))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
	// The imported row is untouched.
	all, _ := ledger.NewStore(conn).All()
	if all[0].Platform != ledger.PlatformDegiro || !all[0].Quantity.Equal(decimalTen(t)) {
		t.Errorf("imported row changed: %+v", all[0])
	}
}

func TestHandler_TradeUpdate_CollisionIs409(t *testing.T) {
	conn := openMigratedTestDB(t)
	buyID, _ := seedManualBuyAndSell(t, conn)
	handler := NewServer(conn).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTradeRequest(map[string]string{
		"kind": "buy", "instrument": "AAPL", "date": "2023-07-01", "quantity": "5", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}

	// Edit the first manual buy into a copy of the second.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, postTo("/trade/update", map[string]string{
		"id": id(buyID), "kind": "buy", "date": "2023-07-01", "quantity": "5", "price": "10", "currency": "EUR",
	}))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_TradeDelete_RemovesATradeNothingDependsOn(t *testing.T) {
	conn := openMigratedTestDB(t)
	_, sellID := seedManualBuyAndSell(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/delete", map[string]string{"id": id(sellID)}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if trades, _ := ledger.NewStore(conn).ManualTrades(); len(trades) != 1 {
		t.Errorf("want 1 manual trade left, got %d", len(trades))
	}
}

func TestHandler_TradeDelete_BuyThatALaterSellDependsOnIsRefused(t *testing.T) {
	conn := openMigratedTestDB(t)
	buyID, _ := seedManualBuyAndSell(t, conn)

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, postTo("/trade/delete", map[string]string{"id": id(buyID)}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exceeds") {
		t.Fatalf("status = %d, body %q; want 400 naming the shortfall", rec.Code, rec.Body.String())
	}
	if trades, _ := ledger.NewStore(conn).ManualTrades(); len(trades) != 2 {
		t.Errorf("nothing should be deleted, got %d manual trades", len(trades))
	}
}

func TestHandler_TradeDelete_RefusesImportedRowAndWrongMethod(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedManualBuyAndSell(t, conn)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, postTo("/trade/delete", map[string]string{"id": "1"})) // imported
	if rec.Code != http.StatusNotFound {
		t.Errorf("imported row: status = %d, want 404", rec.Code)
	}
	if n := countTransactions(t, conn); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}

	for _, path := range []string{"/trade/update", "/trade/delete"} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, want 405", path, rec.Code)
		}
	}
}

func TestHandler_Dashboard_ListsManualTradesWithEditAndDelete(t *testing.T) {
	conn := openMigratedTestDB(t)
	buyID, sellID := seedManualBuyAndSell(t, conn)

	body := dashboardBody(t, conn, "/")
	for _, want := range []string{
		"Manual trades logged (2)",
		`action="/trade/update"`,
		`action="/trade/delete"`,
		`name="id" value="` + id(buyID) + `"`,
		`name="id" value="` + id(sellID) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}
