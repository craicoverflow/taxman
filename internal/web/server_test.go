package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/audit"
	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/prices"
	"github.com/craicoverflow/taxman/internal/tickers"
)

const degiroFixture = "../../testdata/fixtures/degiro/sample.csv"

// newUploadRequest builds a multipart/form-data POST to /import
// carrying content under the "file" field (named filename) and,
// when platform is non-empty, a "platform" field alongside it.
func newUploadRequest(t *testing.T, content []byte, filename, platform string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing upload body: %v", err)
	}

	if platform != "" {
		if err := w.WriteField("platform", platform); err != nil {
			t.Fatalf("WriteField platform: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func openMigratedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	return conn
}

func TestHandler_Dashboard_ReturnsOK(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandler_Dashboard_ShowsHumanReadableProductName(t *testing.T) {
	conn := openMigratedTestDB(t)

	// Instrument is an ISIN, which nobody can eyeball and recognize —
	// the dashboard should also show the source export's own
	// human-readable name for it.
	tx := ledger.Transaction{
		Platform:    ledger.PlatformDegiro,
		Type:        ledger.TypeBuy,
		Date:        mustDate(t, "2024-01-01"),
		Instrument:  "CA00FIXTURE03",
		Description: "SAMPLE GROWTH CO",
		Quantity:    decimalTen(t),
		Price:       decimalTen(t),
		Currency:    "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(tx); err != nil {
		t.Fatalf("inserting transaction: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "CA00FIXTURE03") {
		t.Error("expected the ISIN to still appear in the dashboard body")
	}
	if !strings.Contains(body, "SAMPLE GROWTH CO") {
		t.Error("expected the human-readable product name to appear in the dashboard body")
	}
}

func TestHandler_Dashboard_ShowsUnclassifiedHoldingsFlagged(t *testing.T) {
	conn := openMigratedTestDB(t)

	tx := ledger.Transaction{
		Platform:   ledger.PlatformDegiro,
		Type:       ledger.TypeBuy,
		Date:       mustDate(t, "2024-01-01"),
		Instrument: "UNCLASSIFIED_HOLDING",
		Quantity:   decimalTen(t),
		Price:      decimalTen(t),
		Currency:   "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(tx); err != nil {
		t.Fatalf("inserting transaction: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "UNCLASSIFIED_HOLDING") {
		t.Error("expected the unclassified holding's instrument to appear in the dashboard body")
	}
	if !strings.Contains(strings.ToLower(body), "unclassified") {
		t.Error("expected the dashboard to visibly flag the holding as unclassified, not hide it")
	}
}

func TestHandler_Dashboard_ShowsAuditLinkForLiabilityFigures(t *testing.T) {
	conn := openMigratedTestDB(t)

	// A real audit record only exists once an actual disposal
	// transaction has been computed, so the dashboard's holdings list
	// (derived from real ledger transactions, not from audit records
	// directly) needs one here too.
	tx := ledger.Transaction{
		Platform:   ledger.PlatformDegiro,
		Type:       ledger.TypeSell,
		Date:       mustDate(t, "2024-06-01"),
		Instrument: "AAPL",
		Quantity:   decimalTen(t),
		Price:      decimalTen(t),
		Currency:   "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(tx); err != nil {
		t.Fatalf("inserting transaction: %v", err)
	}

	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}
	rec := audit.Record{
		Instrument:         "AAPL",
		Kind:               audit.KindCGT,
		LiabilityAmount:    decimalTen(t),
		SourceTransactions: []string{"fp-1"},
		LotIDs:             []string{"lot-1"},
		DisposalDate:       mustDate(t, "2024-06-01"),
		RuleEffectiveFrom:  mustDate(t, "1900-01-01"),
		RuleRate:           decimalTen(t),
	}
	if err := audit.NewStore(conn).Insert(rec); err != nil {
		t.Fatalf("Insert audit record: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	respRec := httptest.NewRecorder()
	handler.ServeHTTP(respRec, req)

	body := respRec.Body.String()
	if !strings.Contains(body, "AAPL") {
		t.Error("expected AAPL to appear in the dashboard body")
	}
	// Every rendered liability figure must link to or reference its
	// audit trail per SPEC.md §7's acceptance criteria — checking for
	// an href pointing at the per-instrument audit view is a stand-in
	// for "linked," not a strict UI contract.
	if !strings.Contains(body, "/audit/AAPL") {
		t.Error("expected a link to the audit trail for AAPL's liability figure")
	}
}

func TestHandler_AuditDetail_ReturnsRecordsForInstrument(t *testing.T) {
	conn := openMigratedTestDB(t)

	rec := audit.Record{
		Instrument:         "AAPL",
		Kind:               audit.KindCGT,
		LiabilityAmount:    decimalTen(t),
		SourceTransactions: []string{"fp-1", "fp-2"},
		LotIDs:             []string{"lot-1"},
		DisposalDate:       mustDate(t, "2024-06-01"),
		RuleEffectiveFrom:  mustDate(t, "1900-01-01"),
		RuleRate:           decimalTen(t),
	}
	if err := audit.NewStore(conn).Insert(rec); err != nil {
		t.Fatalf("Insert audit record: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/audit/AAPL", nil)
	respRec := httptest.NewRecorder()
	handler.ServeHTTP(respRec, req)

	if respRec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", respRec.Code, http.StatusOK)
	}
	body := respRec.Body.String()
	if !strings.Contains(body, "fp-1") || !strings.Contains(body, "fp-2") {
		t.Error("expected the audit detail page to list source transaction fingerprints")
	}
	if !strings.Contains(body, "lot-1") {
		t.Error("expected the audit detail page to list lot IDs")
	}
}

func TestHandler_AuditDetail_UnknownInstrument_ReturnsEmptyNotError(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/audit/NOPE", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (no records is not an error)", rec.Code, http.StatusOK)
	}
}

func TestHandler_Dashboard_ShowsUploadForm(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `action="/import"`) {
		t.Error("expected the dashboard to include an upload form posting to /import")
	}
	if !strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Error("expected the upload form to be multipart/form-data")
	}
	if !strings.Contains(body, `type="file"`) {
		t.Error("expected the upload form to include a file input")
	}
}

func TestHandler_Import_POST_AutoDetectsAndStoresTransactions(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	content, err := os.ReadFile(degiroFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	req := newUploadRequest(t, content, "sample.csv", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 transactions stored, got %d", count)
	}
}

func TestHandler_Import_POST_ExplicitPlatform_IsUsed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	content, err := os.ReadFile(degiroFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	req := newUploadRequest(t, content, "sample.csv", "degiro")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 transactions stored, got %d", count)
	}
}

func TestHandler_Import_POST_Rerun_IsIdempotent(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	content, err := os.ReadFile(degiroFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	for i := 0; i < 2; i++ {
		req := newUploadRequest(t, content, "sample.csv", "degiro")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("upload %d: status = %d, body: %s", i, rec.Code, rec.Body.String())
		}
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 transactions after re-uploading the identical file, got %d", count)
	}
}

// degiroFixtureWithOneNewRow simulates the real-world case that
// motivated this test: a fresh monthly export that repeats every row
// from a previous export (same trades) plus one genuinely new one.
const degiroFixtureWithOneNewRow = `Date,Time,Product,ISIN,Reference exchange,Venue,Quantity,Price,,Local value,,Value EUR,Exchange rate,AutoFX Fee,Transaction and/or third party fees EUR,Total EUR,Order ID
15-03-2024,10:32,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,10,85.32,EUR,-853.20,EUR,-853.20,,0.00,-1.00,-854.20,abc-123
20-04-2024,14:00,VANGUARD FTSE ALL-WORLD,IE00BK5BQT80,EAM,XAMS,3,110.50,EUR,-331.50,EUR,-331.50,,0.00,-1.00,-332.50,def-789
02-06-2024,09:15,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,-5,90.00,EUR,450.00,EUR,450.00,,0.00,-1.00,449.00,ghi-321
10-07-2024,11:00,VANGUARD FTSE ALL-WORLD,IE00BK5BQT80,EAM,XAMS,2,115.00,EUR,-230.00,EUR,-230.00,,0.00,-1.00,-231.00,new-999
`

func TestHandler_Import_POST_PartialOverlap_OnlyInsertsNewRows(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	firstContent, err := os.ReadFile(degiroFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	// First import: the original 3-row export.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newUploadRequest(t, firstContent, "march.csv", "degiro"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first upload: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	// Second import: a later export repeating all 3 original rows
	// plus one new one (the "crossover" case).
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newUploadRequest(t, []byte(degiroFixtureWithOneNewRow), "july.csv", "degiro"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second upload: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 transactions (3 original + 1 new) after the overlapping import, got %d", count)
	}
}

func TestHandler_Import_POST_UnrecognizedPlatform_DoesNotGuess(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := newUploadRequest(t, []byte("Foo,Bar,Baz\n1,2,3\n"), "mystery.csv", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "auto-detect") {
		t.Errorf("expected the error body to explain auto-detection failed, got: %s", rec.Body.String())
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected nothing stored for an unrecognized upload, got %d", count)
	}
}

func TestHandler_Import_POST_NoFile_ReturnsError(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_Import_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/import", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Import_POST_CorporateAction_WarnsAndStillImportsRest(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	// A Degiro Account-export upload with one corporate-action row
	// (skipped, not fatal) and one ordinary trade row.
	content := []byte("Date,Time,Value date,Product,ISIN,Description,FX,Change,,Balance,,Order Id\n" +
		"28-02-2024,07:16,27-02-2024,WIDGET CORP,IE0000000001,MERGER: Buy 9 Widget Corp@15.4993 EUR (IE0000000001),,EUR,0.00,EUR,-6.87,\n" +
		"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n")

	req := newUploadRequest(t, content, "account.csv", "degiro")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (warnings should render a summary, not redirect); body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "MERGER") {
		t.Error("expected the corporate-action warning to be shown on the result page")
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected the ordinary trade row to still be imported despite the warning, got %d transactions", count)
	}
}

func TestHandler_Dashboard_ShowsCGTPnLForHolding(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// Buy 10 @10, sell 10 @25: gain 150, under the 1270 exemption so
	// taxable gain/tax due are both 0 but the raw gain should still
	// show.
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	sell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-06-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: mustDecimal(t, "25"), Currency: "EUR",
	}
	if _, err := store.Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if _, err := store.Insert(sell); err != nil {
		t.Fatalf("inserting sell: %v", err)
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "150") {
		t.Errorf("expected the holding's total gain (150) to appear in the dashboard body, got: %s", body)
	}
}

func TestHandler_Dashboard_ShowsRunningLiabilityByTaxType(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// A CGT disposal comfortably over the annual exemption, so its
	// tax due is nonzero and should roll up into the CGT summary
	// total.
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	sell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-06-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: mustDecimal(t, "1000"), Currency: "EUR",
	}
	if _, err := store.Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if _, err := store.Insert(sell); err != nil {
		t.Fatalf("inserting sell: %v", err)
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	// Gain = 10*(1000-10) = 9900; taxable = 9900-1270 = 8630; tax due
	// = 8630*0.33 = 2847.9, rounded down to whole euro -> 2847, shown
	// in the summary table as a euro amount with a symbol, two
	// decimals, and thousands separators.
	if !strings.Contains(body, "€2,847.00") {
		t.Errorf("expected the CGT running liability total (€2,847.00) to appear in the dashboard body, got: %s", body)
	}
}

func TestHandler_Dashboard_ShowsDIRTSummaryForInterest(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	interest := ledger.Transaction{
		Platform: ledger.PlatformN26, Type: ledger.TypeInterest,
		Date: mustDate(t, "2024-06-01"), Instrument: "N26-SAVINGS",
		Quantity: decimal.NewFromInt(1), Price: mustDecimal(t, "100"), Currency: "EUR",
	}
	if _, err := store.Insert(interest); err != nil {
		t.Fatalf("inserting interest: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	// DIRT due = 100*0.33 = 33.
	if !strings.Contains(body, "33") {
		t.Errorf("expected DIRT due (33) to appear in the dashboard body, got: %s", body)
	}
}

func TestHandler_Dashboard_NonEURHolding_ConvertsViaFX(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// USD-denominated: the engine now restates each transaction in
	// euro at its transaction-date ECB rate, so this holding computes
	// a P&L rather than blocking on "not EUR".
	for _, tx := range []ledger.Transaction{
		{
			Platform: ledger.PlatformIBKR, Type: ledger.TypeBuy,
			Date: mustDate(t, "2021-01-25"), Instrument: "MSFT",
			Quantity: decimalTen(t), Price: decimalTen(t), Currency: "USD",
		},
		{
			Platform: ledger.PlatformIBKR, Type: ledger.TypeSell,
			Date: mustDate(t, "2024-01-02"), Instrument: "MSFT",
			Quantity: decimalTen(t), Price: mustDecimal(t, "20"), Currency: "USD",
		},
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("inserting %s: %v", tx.Type, err)
		}
	}
	if err := classify.NewStore(conn).Set("MSFT", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "MSFT") {
		t.Error("expected MSFT to appear in the dashboard body")
	}
	if strings.Contains(strings.ToLower(body), "not eur") || strings.Contains(strings.ToLower(body), "fx conversion is not") {
		t.Errorf("MSFT should now convert via FX, not show a non-EUR error, got: %s", body)
	}
}

func TestHandler_Dashboard_UnresolvableFXRate_ShowsErrorNotCrash(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// A currency with no ECB reference rate: the per-holding compute
	// error must be shown for this holding without failing the page.
	buy := ledger.Transaction{
		Platform: ledger.PlatformIBKR, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-02"), Instrument: "ZZZCO",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "ZZZ",
	}
	if _, err := store.Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if err := classify.NewStore(conn).Set("ZZZCO", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (a per-holding compute error must not fail the whole page)", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ZZZCO") {
		t.Error("expected ZZZCO to still appear in the dashboard body")
	}
	if !strings.Contains(strings.ToLower(body), "error") && !strings.Contains(strings.ToLower(body), "fx:") {
		t.Errorf("expected the FX error to be visible for ZZZCO, got: %s", body)
	}
}

func newClassifyRequest(instrument, classification string) *http.Request {
	form := url.Values{"instrument": {instrument}, "classification": {classification}}
	req := httptest.NewRequest(http.MethodPost, "/classify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandler_Classify_POST_SetsClassification(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newClassifyRequest("AAPL", "CGT_ASSET"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	got, err := classify.NewStore(conn).Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != classify.CGTAsset {
		t.Errorf("classification = %q, want %q", got, classify.CGTAsset)
	}
}

func TestHandler_Classify_POST_Reclassify_Overwrites(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	handler.ServeHTTP(httptest.NewRecorder(), newClassifyRequest("AAPL", "CGT_ASSET"))
	handler.ServeHTTP(httptest.NewRecorder(), newClassifyRequest("AAPL", "EXIT_TAX_FUND"))

	got, err := classify.NewStore(conn).Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != classify.ExitTaxFund {
		t.Errorf("classification = %q, want %q (later --set should overwrite)", got, classify.ExitTaxFund)
	}
}

func TestHandler_Classify_POST_InvalidClassification_ReturnsError(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newClassifyRequest("AAPL", "BOGUS"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	got, err := classify.NewStore(conn).Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != classify.Unclassified {
		t.Errorf("expected AAPL to remain unclassified after a rejected request, got %q", got)
	}
}

func TestHandler_Classify_POST_MissingInstrument_ReturnsError(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newClassifyRequest("", "CGT_ASSET"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_Classify_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/classify", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Dashboard_LoadsChartJSFromCDN(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "cdn.jsdelivr.net/npm/chart.js") {
		t.Errorf("expected the dashboard to load Chart.js from the CDN, got: %s", body)
	}
}

func TestHandler_Dashboard_EmbedsLiabilityByTaxTypeChartData(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// Same CGT scenario as TestHandler_Dashboard_ShowsRunningLiabilityByTaxType:
	// gain 9900, taxable 8630, tax due 2847.9 -> 2847 (rounded down).
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	sell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-06-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: mustDecimal(t, "1000"), Currency: "EUR",
	}
	if _, err := store.Insert(buy); err != nil {
		t.Fatalf("inserting buy: %v", err)
	}
	if _, err := store.Insert(sell); err != nil {
		t.Fatalf("inserting sell: %v", err)
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `<canvas id="liability-chart">`) {
		t.Error("expected the dashboard to include a liability-chart canvas")
	}
	if !strings.Contains(body, `"cgtTaxDue":"2847"`) {
		t.Errorf("expected embedded chart data to include cgtTaxDue 2847 (2847.9 rounded down), got: %s", body)
	}
	if !strings.Contains(body, `"exitTaxTaxDue":"0"`) {
		t.Errorf("expected embedded chart data to include exitTaxTaxDue 0, got: %s", body)
	}
	if !strings.Contains(body, `"dirtTaxDue":"0"`) {
		t.Errorf("expected embedded chart data to default dirtTaxDue to 0 when there's no interest, got: %s", body)
	}
}

func TestHandler_Dashboard_EmbedsPnLOverTimeChartData(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// Two holdings, disposals on different dates and out of
	// insertion order, so the series must be sorted by date and
	// accumulate across holdings: AAPL gain 150 on 2024-06-01, MSFT
	// gain 50 on 2024-03-01 (earlier). Cumulative: 50, then 200.
	aaplBuy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	aaplSell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-06-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: mustDecimal(t, "25"), Currency: "EUR",
	}
	msftBuy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "MSFT",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	msftSell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-03-01"), Instrument: "MSFT",
		Quantity: decimalTen(t), Price: mustDecimal(t, "15"), Currency: "EUR",
	}
	for _, tx := range []ledger.Transaction{aaplBuy, aaplSell, msftBuy, msftSell} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("inserting %s: %v", tx.Instrument, err)
		}
	}
	classifyStore := classify.NewStore(conn)
	if err := classifyStore.Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set AAPL: %v", err)
	}
	if err := classifyStore.Set("MSFT", classify.CGTAsset); err != nil {
		t.Fatalf("Set MSFT: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `<canvas id="pnl-chart">`) {
		t.Error("expected the dashboard to include a pnl-chart canvas")
	}

	wantSeries := `"pnlSeries":[{"date":"2024-03-01","cumulativeGain":"50"},{"date":"2024-06-01","cumulativeGain":"200"}]`
	if !strings.Contains(body, wantSeries) {
		t.Errorf("expected embedded chart data to include a date-sorted, accumulating pnlSeries;\nwant substring: %s\ngot body: %s", wantSeries, body)
	}
}

func TestHandler_Dashboard_PnLChartData_ExcludesHoldingWithComputeError(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	// AAPL: a clean, computable disposal. ZZZCO: left UNCLASSIFIED,
	// so ComputeCGT never runs for it and it must not appear in the
	// series (nor break rendering).
	aaplBuy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	aaplSell := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeSell,
		Date: mustDate(t, "2024-06-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: mustDecimal(t, "25"), Currency: "EUR",
	}
	zzzco := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "ZZZCO",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	for _, tx := range []ledger.Transaction{aaplBuy, aaplSell, zzzco} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("inserting %s: %v", tx.Instrument, err)
		}
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/?year=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	wantSeries := `"pnlSeries":[{"date":"2024-06-01","cumulativeGain":"150"}]`
	if !strings.Contains(body, wantSeries) {
		t.Errorf("expected the series to include only AAPL's disposal;\nwant substring: %s\ngot body: %s", wantSeries, body)
	}
}

func newInterestRequest(date, amount string) *http.Request {
	form := url.Values{"date": {date}, "amount": {amount}}
	req := httptest.NewRequest(http.MethodPost, "/interest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newInterestRequestFromSource(source, date, amount string) *http.Request {
	form := url.Values{"source": {source}, "date": {date}, "amount": {amount}}
	req := httptest.NewRequest(http.MethodPost, "/interest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// newInterestBatchRequest posts the batch grid: one source plus
// parallel date/amount fields, dates[i] pairing with amounts[i].
func newInterestBatchRequest(source string, dates, amounts []string) *http.Request {
	form := url.Values{"source": {source}, "date": dates, "amount": amounts}
	req := httptest.NewRequest(http.MethodPost, "/interest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandler_Interest_POST_InsertsInterestTransaction(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequest("2024-06-15", "12.34"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("len(txs) = %d, want 1", len(txs))
	}
	tx := txs[0]
	if tx.Type != ledger.TypeInterest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeInterest)
	}
	if tx.Platform != ledger.PlatformN26 {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformN26)
	}
	if !tx.Price.Equal(mustDecimal(t, "12.34")) {
		t.Errorf("Price = %s, want 12.34", tx.Price)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if !tx.Date.Equal(mustDate(t, "2024-06-15")) {
		t.Errorf("Date = %s, want 2024-06-15", tx.Date)
	}
}

func TestHandler_Interest_POST_TradeRepublicSource_InsertsTradeRepublicCredit(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequestFromSource("traderepublic", "2024-07-01", "37.50"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("len(txs) = %d, want 1", len(txs))
	}
	tx := txs[0]
	if tx.Platform != ledger.PlatformTradeRepublic {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformTradeRepublic)
	}
	if tx.Type != ledger.TypeInterest {
		t.Errorf("Type = %q, want interest", tx.Type)
	}
	if !tx.Price.Equal(mustDecimal(t, "37.50")) {
		t.Errorf("Price = %s, want 37.50", tx.Price)
	}
}

func TestHandler_Interest_POST_BatchLogsEveryFilledRowForOneSource(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	// Three filled rows interleaved with blank grid rows that must be
	// ignored, not rejected.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestBatchRequest("traderepublic",
		[]string{"2024-01-31", "", "2024-02-29", "", "2024-03-31"},
		[]string{"10.01", "", "10.02", "", "10.03"},
	))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	credits, err := ledger.NewStore(conn).ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(credits) != 3 {
		t.Fatalf("expected 3 logged credits, got %d", len(credits))
	}
	for _, ic := range credits {
		if ic.Platform != ledger.PlatformTradeRepublic {
			t.Errorf("credit %d: Platform = %q, want traderepublic", ic.ID, ic.Platform)
		}
	}
}

func TestHandler_Interest_POST_Batch_OneInvalidRow_WritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestBatchRequest("n26",
		[]string{"2024-01-31", "2024-02-29", "2024-03-31"},
		[]string{"10.00", "not-a-number", "12.00"},
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("a batch with one bad row must write nothing; got %d rows", len(txs))
	}
}

func TestHandler_Interest_POST_Batch_AllRowsBlank_Returns400(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestBatchRequest("n26",
		[]string{"", "", ""},
		[]string{"", "", ""},
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_Interest_POST_Batch_RowMissingAmount_Returns400(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestBatchRequest("n26",
		[]string{"2024-01-31"},
		[]string{""},
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_Interest_POST_UnknownSource_ReturnsErrorAndWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequestFromSource("revolut", "2024-07-01", "10.00"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("len(txs) = %d, want 0 after a rejected unknown-source request", len(txs))
	}
}

func TestHandler_Interest_POST_FeedsIntoDIRTOnDashboard(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	handler.ServeHTTP(httptest.NewRecorder(), newInterestRequest("2024-06-15", "100"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "100") {
		t.Errorf("expected dashboard to show the logged interest amount, got: %s", body)
	}
}

func TestHandler_Interest_POST_InvalidDate_ReturnsErrorAndWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequest("not-a-date", "12.34"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("len(txs) = %d, want 0 after a rejected request", len(txs))
	}
}

func TestHandler_Interest_POST_NonPositiveAmount_ReturnsError(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestRequest("2024-06-15", "-5"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_Interest_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/interest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func newInterestUpdateRequest(id int64, date, amount string) *http.Request {
	form := url.Values{"id": {strconv.FormatInt(id, 10)}, "date": {date}, "amount": {amount}}
	req := httptest.NewRequest(http.MethodPost, "/interest/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newInterestDeleteRequest(id int64) *http.Request {
	form := url.Values{"id": {strconv.FormatInt(id, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/interest/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// loggedInterestID logs one interest credit through the handler and
// returns its stored row id.
func loggedInterestID(t *testing.T, conn *sql.DB, handler http.Handler, date, amount string) int64 {
	t.Helper()
	handler.ServeHTTP(httptest.NewRecorder(), newInterestRequest(date, amount))
	credits, err := ledger.NewStore(conn).ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(credits) == 0 {
		t.Fatalf("no interest credit was logged")
	}
	// ManualInterestCredits is ordered by date, not insertion, so pick
	// the highest row id — the one just inserted.
	newest := credits[0].ID
	for _, c := range credits[1:] {
		if c.ID > newest {
			newest = c.ID
		}
	}
	return newest
}

func TestHandler_InterestUpdate_POST_CorrectsDateAndAmount(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()
	id := loggedInterestID(t, conn, handler, "2024-06-15", "12.34")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestUpdateRequest(id, "2024-07-01", "20.00"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	credits, err := ledger.NewStore(conn).ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(credits) != 1 {
		t.Fatalf("len(credits) = %d, want 1", len(credits))
	}
	if !credits[0].Date.Equal(mustDate(t, "2024-07-01")) {
		t.Errorf("Date = %s, want 2024-07-01", credits[0].Date)
	}
	if !credits[0].Price.Equal(mustDecimal(t, "20")) {
		t.Errorf("Price = %s, want 20", credits[0].Price)
	}
}

func TestHandler_InterestUpdate_POST_UnknownID_Returns404(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestUpdateRequest(999, "2024-07-01", "20.00"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandler_InterestUpdate_POST_InvalidAmount_Returns400(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()
	id := loggedInterestID(t, conn, handler, "2024-06-15", "12.34")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestUpdateRequest(id, "2024-07-01", "-5"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandler_InterestUpdate_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/interest/update", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_InterestDelete_POST_RemovesCredit(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()
	id := loggedInterestID(t, conn, handler, "2024-06-15", "12.34")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestDeleteRequest(id))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	credits, err := ledger.NewStore(conn).ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(credits) != 0 {
		t.Errorf("len(credits) = %d, want 0 after delete", len(credits))
	}
}

func TestHandler_InterestDelete_POST_UnknownID_Returns404(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newInterestDeleteRequest(999))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandler_InterestDelete_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/interest/delete", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Dashboard_ListsLoggedInterestCreditsWithEditAndDelete(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()
	loggedInterestID(t, conn, handler, "2024-06-15", "12.34")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	for _, want := range []string{`action="/interest/update"`, `action="/interest/delete"`, `value="2024-06-15"`, `value="12.34"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected dashboard body to contain %q\ngot: %s", want, body)
		}
	}
}

func TestHandler_Dashboard_InterestLog_ShowsSourcePerCreditAndPicker(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	handler.ServeHTTP(httptest.NewRecorder(), newInterestRequestFromSource("n26", "2024-06-15", "12.34"))
	handler.ServeHTTP(httptest.NewRecorder(), newInterestRequestFromSource("traderepublic", "2024-07-15", "56.78"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year=", nil))
	body := rec.Body.String()

	for _, want := range []string{
		`<select name="source">`,                                    // the entry-form picker
		`<option value="traderepublic">Trade Republic`,              // both sources offered
		`<th>Source</th>`,                                           // the log's new column
		`<input type="hidden" name="source" value="traderepublic">`, // edit form echoes the row's source
		`<input type="hidden" name="source" value="n26">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected dashboard body to contain %q\ngot: %s", want, body)
		}
	}
}

func TestHandler_Dashboard_InterestCredit_DoesNotAppearAsHolding(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()
	loggedInterestID(t, conn, handler, "2024-06-15", "12.34")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()

	if strings.Contains(body, "N26_SAVINGS") {
		t.Errorf("interest instrument N26_SAVINGS should not appear in the holdings table; got: %s", body)
	}
	if strings.Contains(body, "UNCLASSIFIED") {
		t.Errorf("an interest-only ledger should have no UNCLASSIFIED holding; got: %s", body)
	}
	// It still feeds the DIRT line.
	if !strings.Contains(body, "12.34") {
		t.Errorf("expected the DIRT summary / interest list to still show the credit; got: %s", body)
	}
}

func TestHandler_Dashboard_ShowsInterestEntryForm(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `action="/interest"`) {
		t.Error("expected the dashboard to include a form posting to /interest")
	}
	// The batch grid starts as a single date/amount row under one
	// source picker; the client-side "Add row" helper clones more.
	// `name="amount"` is unique to this grid (the RSU form uses `fmv`),
	// so its count is the starting row count.
	if !strings.Contains(body, `id="interest-batch"`) {
		t.Error("expected the interest form to render the batch grid")
	}
	if n := strings.Count(body, `name="amount"`); n != 1 {
		t.Errorf("expected exactly one blank amount input to start, got %d", n)
	}
	if !strings.Contains(body, "function addInterestRow()") {
		t.Error("expected the Add row helper script")
	}
}

func TestHandler_Dashboard_ShowsClassifyFormForEachHolding(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)

	tx := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2024-01-01"), Instrument: "AAPL",
		Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR",
	}
	if _, err := store.Insert(tx); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	handler := NewServer(conn).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `action="/classify"`) {
		t.Error("expected the dashboard to include a classification form posting to /classify")
	}
	if !strings.Contains(body, `value="AAPL"`) {
		t.Error("expected the classification form to identify the AAPL holding")
	}
	if !strings.Contains(body, "CGT_ASSET") || !strings.Contains(body, "EXIT_TAX_FUND") {
		t.Error("expected the classification form to offer both CGT_ASSET and EXIT_TAX_FUND options")
	}
}

// twoYearCGTLedger seeds one AAPL disposal in 2024 (tax due €2,847.90)
// and one MSFT disposal in 2025 (tax due €6,147.90), both CGT assets,
// so a year filter has something distinct to scope to.
func twoYearCGTLedger(t *testing.T, conn *sql.DB) {
	t.Helper()
	store := ledger.NewStore(conn)
	txs := []ledger.Transaction{
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-01-01"), Instrument: "AAPL", Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2024-06-01"), Instrument: "AAPL", Quantity: decimalTen(t), Price: mustDecimal(t, "1000"), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-01-01"), Instrument: "MSFT", Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2025-06-01"), Instrument: "MSFT", Quantity: decimalTen(t), Price: mustDecimal(t, "2000"), Currency: "EUR"},
	}
	for _, tx := range txs {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("inserting %s: %v", tx.Instrument, err)
		}
	}
	cs := classify.NewStore(conn)
	for _, in := range []string{"AAPL", "MSFT"} {
		if err := cs.Set(in, classify.CGTAsset); err != nil {
			t.Fatalf("Set %s: %v", in, err)
		}
	}
}

func dashboardBody(t *testing.T, conn *sql.DB, target string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", target, rec.Code)
	}
	return rec.Body.String()
}

func TestHandler_Dashboard_YearFilter_ScopesLiabilityToSelectedYear(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn)

	// AAPL's 2024 disposal: gain 9900, taxable 8630, tax 2847.9 -> 2847.
	// MSFT's 2025 disposal: gain 19900, taxable 18630, tax 6147.9 -> 6147.
	// (Tax rounds down to whole euro; the all-years total is the sum of
	// each year's own charge.)
	all := dashboardBody(t, conn, "/?year=")
	if !strings.Contains(all, "€8,994.00") { // 2847 + 6147
		t.Errorf("all-years view: expected combined CGT total €8,994.00, got: %s", all)
	}

	y2024 := dashboardBody(t, conn, "/?year=2024")
	if !strings.Contains(y2024, "€2,847.00") {
		t.Errorf("2024 view: expected €2,847.00, got: %s", y2024)
	}
	if strings.Contains(y2024, "€6,147.00") || strings.Contains(y2024, "€8,994.00") {
		t.Errorf("2024 view: leaked a figure from another year: %s", y2024)
	}

	y2025 := dashboardBody(t, conn, "/?year=2025")
	if !strings.Contains(y2025, "€6,147.00") {
		t.Errorf("2025 view: expected €6,147.00, got: %s", y2025)
	}
	if strings.Contains(y2025, "€2,847.00") {
		t.Errorf("2025 view: leaked 2024's figure: %s", y2025)
	}
}

func TestHandler_Dashboard_YearSelector_ListsYearsWithTransactions(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn)

	body := dashboardBody(t, conn, "/")
	for _, want := range []string{`name="year"`, ">All years<", `value="2023"`, `value="2024"`, `value="2025"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the year selector to contain %q, got: %s", want, body)
		}
	}
}

func TestHandler_Dashboard_YearSelector_MarksSelectedYear(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn)

	body := dashboardBody(t, conn, "/?year=2024")
	if !strings.Contains(body, `value="2024" selected`) {
		t.Errorf("expected the 2024 option to be marked selected, got: %s", body)
	}
}

func TestHandler_Dashboard_InvalidYear_Returns400(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn)

	for _, bad := range []string{"not-a-year", "1800", "9999", "0"} {
		rec := httptest.NewRecorder()
		NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?year="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /?year=%s: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestHandler_Dashboard_NoYearParam_DefaultsToCurrentTaxYear(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn) // disposals in 2024 and 2025 only

	// A bare "/" — no year key at all — scopes to the current tax year,
	// which twoYearCGTLedger has no disposals in, so none of the
	// past-year charges leak in and the current year is the marked
	// option.
	body := dashboardBody(t, conn, "/")

	currentYear := strconv.Itoa(time.Now().Year())
	if !strings.Contains(body, `value="`+currentYear+`" selected`) {
		t.Errorf("expected the current year (%s) option to be marked selected, got: %s", currentYear, body)
	}
	for _, leaked := range []string{"€2,847.00", "€6,147.00", "€8,994.00"} {
		if strings.Contains(body, leaked) {
			t.Errorf("default view leaked a past-year figure %s: %s", leaked, body)
		}
	}
}

func TestHandler_Dashboard_ExplicitEmptyYear_ShowsAllYears(t *testing.T) {
	conn := openMigratedTestDB(t)
	twoYearCGTLedger(t, conn)

	body := dashboardBody(t, conn, "/?year=")
	if !strings.Contains(body, `value="" selected`) {
		t.Errorf("expected the All years option to be marked selected, got: %s", body)
	}
	if !strings.Contains(body, "€8,994.00") {
		t.Errorf("expected the all-years combined total €8,994.00, got: %s", body)
	}
}

func TestHandler_Dashboard_InterestCreditsCollapsedByDefault(t *testing.T) {
	conn := openMigratedTestDB(t)

	body := dashboardBody(t, conn, "/")
	if !strings.Contains(body, `<details class="disclosure">`) {
		t.Errorf("expected the interest-credit log to render inside a collapsed <details>, got: %s", body)
	}
	if strings.Contains(body, `<details class="disclosure" open>`) {
		t.Errorf("interest-credit log should be collapsed (not open) by default, got: %s", body)
	}
	if !strings.Contains(body, "<summary>Interest credits logged") {
		t.Errorf("expected the disclosure summary to label the interest-credit log, got: %s", body)
	}
}

// --- Manual RSU vest entry (POST /rsu) ---

func newRSUVestRequest(fields map[string]string) *http.Request {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/rsu", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHandler_RSUVest_POST_InsertsRSUVestTransaction(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRSUVestRequest(map[string]string{
		"symbol": "ACME", "date": "2024-03-15", "quantity": "42", "fmv": "187.50", "currency": "USD",
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
	if len(txs) != 1 {
		t.Fatalf("len(txs) = %d, want 1", len(txs))
	}
	tx := txs[0]
	if tx.Type != ledger.TypeRSUVest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeRSUVest)
	}
	if tx.Platform != ledger.PlatformETRADE {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformETRADE)
	}
	if tx.Instrument != "ACME" {
		t.Errorf("Instrument = %q, want ACME", tx.Instrument)
	}
	if !tx.Quantity.Equal(mustDecimal(t, "42")) {
		t.Errorf("Quantity = %s, want 42", tx.Quantity)
	}
	if !tx.Price.Equal(mustDecimal(t, "187.50")) {
		t.Errorf("Price = %s, want 187.50", tx.Price)
	}
	if tx.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", tx.Currency)
	}
	if !tx.Date.Equal(mustDate(t, "2024-03-15")) {
		t.Errorf("Date = %s, want 2024-03-15", tx.Date)
	}
}

func TestHandler_RSUVest_POST_DefaultsCurrencyToUSD(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRSUVestRequest(map[string]string{
		"symbol": "ACME", "date": "2024-03-15", "quantity": "10", "fmv": "100",
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 1 || txs[0].Currency != "USD" {
		t.Fatalf("expected one vest defaulting to USD, got %+v", txs)
	}
}

func TestHandler_RSUVest_POST_InvalidField_ReturnsErrorAndWritesNothing(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	for _, bad := range []map[string]string{
		{"symbol": "", "date": "2024-03-15", "quantity": "42", "fmv": "187.50"},
		{"symbol": "ACME", "date": "nope", "quantity": "42", "fmv": "187.50"},
		{"symbol": "ACME", "date": "2024-03-15", "quantity": "-1", "fmv": "187.50"},
		{"symbol": "ACME", "date": "2024-03-15", "quantity": "42", "fmv": "0"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newRSUVestRequest(bad))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("fields %v: status = %d, want 400", bad, rec.Code)
		}
	}

	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("len(txs) = %d, want 0 after rejected requests", len(txs))
	}
}

func TestHandler_RSUVest_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rsu", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Dashboard_ShowsRSUVestForm(t *testing.T) {
	conn := openMigratedTestDB(t)
	body := dashboardBody(t, conn, "/")
	for _, want := range []string{`action="/rsu"`, `name="symbol"`, `name="fmv"`, `name="quantity"`, `name="currency"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the dashboard to include the RSU vest form fragment %q", want)
		}
	}
}

func TestHandler_RSUVest_POST_ThenClassify_ShowsUSDVestAsHolding(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	// A USD vest must reach the holdings table without an FX error —
	// the engine restates it to EUR at the vest-date rate.
	handler.ServeHTTP(httptest.NewRecorder(), newRSUVestRequest(map[string]string{
		"symbol": "ACME", "date": "2024-03-15", "quantity": "42", "fmv": "187.50", "currency": "USD",
	}))
	if err := classify.NewStore(conn).Set("ACME", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	body := dashboardBody(t, conn, "/?year=")
	if !strings.Contains(body, "ACME") {
		t.Errorf("expected the vested holding ACME in the dashboard, got: %s", body)
	}
	if strings.Contains(body, "converting the USD") {
		t.Errorf("USD vest should convert cleanly, got an FX error: %s", body)
	}
}

// --- Privacy mode (nav "Blur €" toggle) ---

func TestPageShell_HasPrivacyToggleWiredToLocalStorage(t *testing.T) {
	conn := openMigratedTestDB(t)
	body := dashboardBody(t, conn, "/")

	for _, want := range []string{
		`id="privacy-toggle"`,           // the nav button
		`class="navbtn"`,                // styled as a nav control
		`aria-pressed=`,                 // exposes toggle state
		`"taxman.privacy"`,              // the localStorage key
		`classList.add("privacy-mode")`, // applied to <html> before paint
		`location.reload()`,             // toggling reloads so charts rebuild
		// chartOptions strips the value axis + tooltips in privacy mode
		`document.documentElement.classList.contains("privacy-mode")`,
		`tooltip: {enabled: !priv}`,
		`ticks: {display: !priv}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the page shell to contain %q", want)
		}
	}
	// The toggle is in every page's shell, not just the dashboard.
	if !strings.Contains(portfolioBody(t, conn, "/portfolio"), `id="privacy-toggle"`) {
		t.Error("expected the privacy toggle on the portfolio page too")
	}
	// The stylesheet link is fingerprinted so a CSS change isn't masked
	// by a cached copy.
	if !strings.Contains(body, `href="/static/app.css?v=`) {
		t.Errorf("expected a versioned stylesheet link; got: %s", body)
	}
}

func TestHandler_StaticCSS_VersionedImmutableAndConditional(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("expected an ETag and an immutable Cache-Control; got ETag=%q CC=%q", etag, rec.Header().Get("Cache-Control"))
	}
	css := rec.Body.String()
	if !strings.Contains(css, ".privacy-mode .money") {
		t.Error("stylesheet should blur .money figures in privacy mode")
	}
	if !strings.Contains(css, ".privacy-mode .sensitive") {
		t.Error("stylesheet should blur .sensitive values (symbols, quantities) in privacy mode")
	}
	if strings.Contains(css, ".privacy-mode canvas") {
		t.Error("stylesheet should NOT blur chart canvases in privacy mode — charts stay visible")
	}

	// A matching If-None-Match is a 304 with no body.
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional GET: status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response should have no body, got %d bytes", rec.Body.Len())
	}
}

func TestHandler_Dashboard_MoneyFiguresWrappedForBlur(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := ledger.NewStore(conn)
	for _, tx := range []ledger.Transaction{
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2024-01-01"), Instrument: "AAPL", Quantity: decimalTen(t), Price: decimalTen(t), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2024-06-01"), Instrument: "AAPL", Quantity: decimalTen(t), Price: mustDecimal(t, "1000"), Currency: "EUR"},
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := classify.NewStore(conn).Set("AAPL", classify.CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	body := dashboardBody(t, conn, "/?year=2024")
	if !strings.Contains(body, `<span class="money">€2,847.00</span>`) {
		t.Errorf("expected euro figures wrapped in a .money span for the privacy toggle; got: %s", body)
	}
}

// --- Portfolio page (SPEC §9, task 11.3: skeleton, no live prices) ---

func newTickerRequest(instrument, symbol string) *http.Request {
	form := url.Values{"instrument": {instrument}, "symbol": {symbol}}
	req := httptest.NewRequest(http.MethodPost, "/portfolio/ticker", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// portfolioSeed inserts: one still-held position (bought 10 @ 100, sold
// 4 → 6 held, €600 FIFO cost basis), one fully-disposed instrument
// (must drop off the page), and an interest credit (must never appear).
func portfolioSeed(t *testing.T, conn *sql.DB) {
	t.Helper()
	store := ledger.NewStore(conn)
	txs := []ledger.Transaction{
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-01-01"), Instrument: "US0378331005", Description: "APPLE INC", Quantity: decimal.NewFromInt(10), Price: decimal.NewFromInt(100), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2024-02-01"), Instrument: "US0378331005", Description: "APPLE INC", Quantity: decimal.NewFromInt(4), Price: decimal.NewFromInt(150), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-03-01"), Instrument: "IE00B3RBWM25", Description: "VANGUARD FTSE AW", Quantity: decimal.NewFromInt(5), Price: decimal.NewFromInt(80), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2024-04-01"), Instrument: "IE00B3RBWM25", Description: "VANGUARD FTSE AW", Quantity: decimal.NewFromInt(5), Price: decimal.NewFromInt(90), Currency: "EUR"},
		{Platform: ledger.PlatformN26, Type: ledger.TypeInterest, Date: mustDate(t, "2024-05-01"), Instrument: "N26_SAVINGS", Quantity: decimal.NewFromInt(1), Price: decimal.NewFromInt(25), Currency: "EUR"},
	}
	for _, tx := range txs {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("seed insert %s: %v", tx.Instrument, err)
		}
	}
}

// stubQuoter fails every lookup — the default for portfolio tests that
// don't exercise pricing, so no test ever touches the network.
type stubQuoter struct{}

func (stubQuoter) Quote(context.Context, string) (prices.Quote, error) {
	return prices.Quote{}, fmt.Errorf("no quote (test stub)")
}

// fakeQuoter returns canned quotes (and canned errors) by symbol.
type fakeQuoter struct {
	quotes map[string]prices.Quote
	errs   map[string]error
}

func (f fakeQuoter) Quote(_ context.Context, symbol string) (prices.Quote, error) {
	if err, ok := f.errs[symbol]; ok {
		return prices.Quote{}, err
	}
	q, ok := f.quotes[symbol]
	if !ok {
		return prices.Quote{}, fmt.Errorf("fakeQuoter: no quote for %q", symbol)
	}
	return q, nil
}

// portfolioServer builds a Server whose price fetching and FX are
// stubbed: q supplies quotes, rate converts currency→EUR (pass nil for
// a 1:1 identity rate). No portfolio test hits the real network or the
// embedded FX file.
func portfolioServer(t *testing.T, conn *sql.DB, q prices.Quoter, rate func(string, time.Time) (decimal.Decimal, error)) http.Handler {
	t.Helper()
	s := NewServer(conn)
	s.quoter = q
	if rate != nil {
		s.rate = rate
	} else {
		s.rate = func(string, time.Time) (decimal.Decimal, error) { return decimal.NewFromInt(1), nil }
	}
	return s.Handler()
}

func mustUpsertTicker(t *testing.T, conn *sql.DB, instrument, symbol string) {
	t.Helper()
	if err := tickers.NewStore(conn).Upsert(instrument, symbol); err != nil {
		t.Fatalf("Upsert %s→%s: %v", instrument, symbol, err)
	}
}

func portfolioBody(t *testing.T, conn *sql.DB, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	portfolioServer(t, conn, stubQuoter{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200; body: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// portfolioSeedTwoHeld inserts two still-held positions: US0378331005
// ("APPLE INC", 10 bought @ 100, 4 sold → 6 held, €600 cost basis) and
// IE00B3RBWM25 ("VANGUARD FTSE AW", 10 bought @ 80 → 10 held, €800).
func portfolioSeedTwoHeld(t *testing.T, conn *sql.DB) {
	t.Helper()
	store := ledger.NewStore(conn)
	txs := []ledger.Transaction{
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-01-01"), Instrument: "US0378331005", Description: "APPLE INC", Quantity: decimal.NewFromInt(10), Price: decimal.NewFromInt(100), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeSell, Date: mustDate(t, "2024-02-01"), Instrument: "US0378331005", Description: "APPLE INC", Quantity: decimal.NewFromInt(4), Price: decimal.NewFromInt(150), Currency: "EUR"},
		{Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: mustDate(t, "2023-03-01"), Instrument: "IE00B3RBWM25", Description: "VANGUARD FTSE AW", Quantity: decimal.NewFromInt(10), Price: decimal.NewFromInt(80), Currency: "EUR"},
	}
	for _, tx := range txs {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("seed insert %s: %v", tx.Instrument, err)
		}
	}
}

func TestHandler_Portfolio_ShowsHeldPositionsOnly(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeed(t, conn)

	body := portfolioBody(t, conn, "/portfolio")

	if !strings.Contains(body, "US0378331005") {
		t.Errorf("expected held position US0378331005 on the page; got: %s", body)
	}
	if !strings.Contains(body, `<td><span class="sensitive">6</span></td>`) {
		t.Errorf("expected held quantity 6 (10 bought − 4 sold); got: %s", body)
	}
	if !strings.Contains(body, "€600.00") {
		t.Errorf("expected FIFO cost basis €600.00 (6 @ 100); got: %s", body)
	}
	if strings.Contains(body, "IE00B3RBWM25") {
		t.Errorf("fully-disposed IE00B3RBWM25 should not appear; got: %s", body)
	}
	if strings.Contains(body, "N26_SAVINGS") {
		t.Errorf("interest instrument N26_SAVINGS should never appear on the portfolio page; got: %s", body)
	}
}

func TestHandler_Portfolio_UnmappedShowsForm_MappedIsPriced(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeed(t, conn)

	// Unmapped: the held position offers a mapping form.
	body := portfolioBody(t, conn, "/portfolio")
	if !strings.Contains(body, `action="/portfolio/ticker"`) {
		t.Errorf("expected a ticker-mapping form for the unmapped held position; got: %s", body)
	}

	// Mapped + quoted: it moves into the priced holdings table with a
	// euro market value (6 held × €150 = €900, distinct from the €600
	// cost basis).
	mustUpsertTicker(t, conn, "US0378331005", "AAPL")
	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "150"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
	}}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if !strings.Contains(body, `<td><span class="sensitive">AAPL</span></td>`) {
		t.Errorf("expected the mapped symbol in the holdings table; got: %s", body)
	}
	if !strings.Contains(body, "€900.00") {
		t.Errorf("expected market value €900.00 (6 × €150); got: %s", body)
	}
}

func TestHandler_PortfolioTicker_POST_MapsInstrument(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTickerRequest("US0378331005", "AAPL"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/portfolio" {
		t.Errorf("Location = %q, want %q", loc, "/portfolio")
	}
	sym, ok, err := tickers.NewStore(conn).Get("US0378331005")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || sym != "AAPL" {
		t.Errorf("stored mapping = (%q, %v), want (\"AAPL\", true)", sym, ok)
	}
}

func TestHandler_PortfolioTicker_POST_EmptySymbol_Returns400(t *testing.T) {
	conn := openMigratedTestDB(t)
	handler := NewServer(conn).Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newTickerRequest("US0378331005", "  "))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if _, ok, _ := tickers.NewStore(conn).Get("US0378331005"); ok {
		t.Errorf("expected no mapping written after a rejected request")
	}
}

func TestHandler_PortfolioTicker_GET_NotAllowed(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio/ticker", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_Dashboard_LinksToPortfolio(t *testing.T) {
	conn := openMigratedTestDB(t)
	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `href="/portfolio"`) {
		t.Errorf("expected the dashboard to link to /portfolio; got: %s", rec.Body.String())
	}
}

func TestHandler_Portfolio_IgnoresYearParam(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeed(t, conn)

	withYear := portfolioBody(t, conn, "/portfolio?year=2024")
	without := portfolioBody(t, conn, "/portfolio")
	if withYear != without {
		t.Errorf("portfolio page should ignore ?year=; bodies differ:\nwith year: %s\nwithout:   %s", withYear, without)
	}
}

// --- Portfolio page: live prices, pie + bars (task 11.5) ---

func TestHandler_Portfolio_PieAndBars_EURConvertedAndSortedByValue(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL") // 6 held, cost €600
	mustUpsertTicker(t, conn, "IE00B3RBWM25", "VWRL") // 10 held, cost €800

	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "200"), Currency: "USD", AsOf: mustDate(t, "2024-06-01")},
		"VWRL": {Symbol: "VWRL", Price: mustDecimal(t, "100"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
	}}
	// USD→EUR at 0.9; every other currency 1:1.
	rate := func(cur string, _ time.Time) (decimal.Decimal, error) {
		if cur == "USD" {
			return mustDecimal(t, "0.9"), nil
		}
		return decimal.NewFromInt(1), nil
	}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, rate).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// AAPL: 6 × (200 USD × 0.9) = €1,080.00  →  the FX rate was applied
	// (without it the figure would be €1,200.00).
	if !strings.Contains(body, "€1,080.00") {
		t.Errorf("expected AAPL market value €1,080.00 (6 × 200 USD × 0.9); got: %s", body)
	}
	// VWRL: 10 × 100 EUR = €1,000.00
	if !strings.Contains(body, "€1,000.00") {
		t.Errorf("expected VWRL market value €1,000.00; got: %s", body)
	}
	if !strings.Contains(body, `Total market value: <strong><span class="money">€2,080.00</span></strong>`) {
		t.Errorf("expected total €2,080.00; got: %s", body)
	}

	// Pie ordered by market value, descending: AAPL (1080) before VWRL (1000).
	aapl := strings.Index(body, `{"label":"APPLE INC","valueEUR":`)
	vwrl := strings.Index(body, `{"label":"VANGUARD FTSE AW","valueEUR":`)
	if aapl < 0 || vwrl < 0 {
		t.Fatalf("pie JSON missing a label; body: %s", body)
	}
	if aapl > vwrl {
		t.Errorf("pie should be sorted by market value descending (AAPL before VWRL); body: %s", body)
	}
	// Bars carry both cost basis and market value per instrument.
	if !strings.Contains(body, `"bars":[{"label":"APPLE INC","costBasisEUR":"600","marketValueEUR":"1080"}`) {
		t.Errorf("bars JSON missing/incorrect for AAPL; body: %s", body)
	}
}

func TestHandler_Portfolio_ShowsGainLossPercentAgainstCostBasis(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL") // 6 held, cost €600
	mustUpsertTicker(t, conn, "IE00B3RBWM25", "VWRL") // 10 held, cost €800

	fq := fakeQuoter{quotes: map[string]prices.Quote{
		// AAPL: 6 × €200 = €1,200 vs €600 cost  →  +100.0% (a gain).
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "200"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
		// VWRL: 10 × €40 = €400 vs €800 cost  →  -50.0% (a loss).
		"VWRL": {Symbol: "VWRL", Price: mustDecimal(t, "40"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
	}}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if !strings.Contains(body, `<th>P&amp;L</th>`) {
		t.Errorf("expected a P&L column header; got: %s", body)
	}
	// Symbol and quantity are wrapped so the privacy toggle blurs them
	// alongside the euro figures.
	if !strings.Contains(body, `<td><span class="sensitive">AAPL</span></td>`) {
		t.Errorf("expected the Symbol cell wrapped in .sensitive; got: %s", body)
	}
	if !strings.Contains(body, `<td><span class="sensitive">6</span></td>`) {
		t.Errorf("expected the Quantity cell wrapped in .sensitive; got: %s", body)
	}
	if !strings.Contains(body, `<input class="sensitive" type="text" name="symbol" value="AAPL"`) {
		t.Errorf("expected the remap symbol input wrapped in .sensitive; got: %s", body)
	}
	if !strings.Contains(body, `<span class="money gain-pos">+100.0%</span>`) {
		t.Errorf("expected AAPL P&L +100.0%% (gain); got: %s", body)
	}
	if !strings.Contains(body, `<span class="money gain-neg">-50.0%</span>`) {
		t.Errorf("expected VWRL P&L -50.0%% (loss); got: %s", body)
	}
	// Totals: (1200 + 400) vs (600 + 800) = 1600 vs 1400  →  +14.3%.
	if !strings.Contains(body, `<span class="money gain-pos">+14.3%</span> vs cost`) {
		t.Errorf("expected total P&L +14.3%% in the summary; got: %s", body)
	}
}

func TestHandler_Portfolio_UnmappedHoldingExcludedFromTotalsAndCharts(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL") // mapped
	// IE00B3RBWM25 left unmapped

	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "100"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
	}}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, body)
	}
	// Only AAPL counts: 6 × €100 = €600.00.
	if !strings.Contains(body, `Total market value: <strong><span class="money">€600.00</span></strong>`) {
		t.Errorf("total should cover only the mapped holding (€600.00); got: %s", body)
	}
	// The unmapped one still offers a mapping form...
	if !strings.Contains(body, `name="instrument" value="IE00B3RBWM25"`) {
		t.Errorf("expected a mapping form for the unmapped held position; got: %s", body)
	}
	// ...and never appears in the pie.
	if strings.Contains(body, `"label":"VANGUARD FTSE AW"`) {
		t.Errorf("unmapped holding leaked into the charts; got: %s", body)
	}
}

func TestHandler_Portfolio_StaleQuote_ShowsMarkerAndAsOf(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL")

	asOf := time.Date(2024, 6, 1, 13, 37, 0, 0, time.UTC)
	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "100"), Currency: "EUR", AsOf: asOf, Stale: true},
	}}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "Prices as of 13:37") {
		t.Errorf("expected an 'as of' stamp from the stale quote's AsOf; got: %s", body)
	}
	if !strings.Contains(body, "Some prices are stale") {
		t.Errorf("expected a page-level stale notice; got: %s", body)
	}
	if !strings.Contains(body, "(stale)") {
		t.Errorf("expected a per-row stale marker; got: %s", body)
	}
}

func TestHandler_Portfolio_FXRateFailure_HoldingListedSeparately_PageStillRenders(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL") // USD — rate lookup will fail
	mustUpsertTicker(t, conn, "IE00B3RBWM25", "VWRL") // EUR — fine

	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "200"), Currency: "USD", AsOf: mustDate(t, "2024-06-01")},
		"VWRL": {Symbol: "VWRL", Price: mustDecimal(t, "100"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},
	}}
	rate := func(cur string, _ time.Time) (decimal.Decimal, error) {
		if cur == "USD" {
			return decimal.Zero, fmt.Errorf("fx: no reference rate for USD (test)")
		}
		return decimal.NewFromInt(1), nil
	}

	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, rate).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("an FX failure must not 500 the page; status = %d, body: %s", rec.Code, body)
	}
	if !strings.Contains(body, "Could not value these positions") {
		t.Errorf("expected a 'could not value' section; got: %s", body)
	}
	if !strings.Contains(body, "no EUR reference rate for USD") {
		t.Errorf("expected the per-row reason; got: %s", body)
	}
	if !strings.Contains(body, "Some holdings could not be valued") {
		t.Errorf("expected the page-level banner; got: %s", body)
	}
	// The unpriced row carries a remap form prefilled with the current
	// (wrong-for-this-purpose) symbol, so a bad mapping isn't a dead end.
	if !strings.Contains(body, `name="instrument" value="US0378331005"`) ||
		!strings.Contains(body, `name="symbol" value="AAPL"`) {
		t.Errorf("expected a prefilled remap form on the unpriced row; got: %s", body)
	}
	// VWRL is unaffected: 10 × €100 = €1,000.00 and it is the whole total.
	if !strings.Contains(body, `Total market value: <strong><span class="money">€1,000.00</span></strong>`) {
		t.Errorf("the EUR holding should still be valued; got: %s", body)
	}
}
