package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestHandler_AnniversariesICS_ListsUpcomingWithoutAmounts(t *testing.T) {
	conn := openMigratedTestDB(t)
	// Bought 2022-06-01 -> first anniversary 2030-06-01 (upcoming).
	buy := ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy,
		Date: mustDate(t, "2022-06-01"), Instrument: "IE_ETF",
		Quantity: mustDecimal(t, "123.456"), Price: decimalTen(t), Currency: "EUR",
	}
	if _, err := ledger.NewStore(conn).Insert(buy); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := classify.NewStore(conn).Set("IE_ETF", classify.ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anniversaries.ics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"BEGIN:VCALENDAR", "DTSTART;VALUE=DATE:20300601", "SUMMARY:8-year deemed disposal: IE_ETF", "TRIGGER:-P30D", "TRIGGER:-P7D", "END:VCALENDAR"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	// Account data must not reach a calendar.
	if strings.Contains(body, "123.456") {
		t.Error("quantity leaked into the calendar file")
	}
}

func TestHandler_AnniversariesICS_OmitsReachedAnniversaries(t *testing.T) {
	conn := openMigratedTestDB(t)
	seedFundLot(t, conn) // 2016 buy -> 2024 reached, 2032 next

	rec := httptest.NewRecorder()
	NewServer(conn).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anniversaries.ics", nil))
	body := rec.Body.String()

	if strings.Contains(body, "20240315") {
		t.Error("reached 2024 anniversary should not be in the calendar")
	}
	if !strings.Contains(body, "DTSTART;VALUE=DATE:20320315") {
		t.Errorf("expected the 2032 anniversary in:\n%s", body)
	}
}

func TestFoldICSLine_FoldsAt75OctetsAndKeepsRunes(t *testing.T) {
	long := "SUMMARY:" + strings.Repeat("é", 60)
	folded := foldICSLine(long)
	for _, part := range strings.Split(folded, "\r\n") {
		if len(part) > 75 {
			t.Errorf("segment is %d octets: %q", len(part), part)
		}
	}
	if got := strings.ReplaceAll(folded, "\r\n ", ""); got != long {
		t.Error("unfolding did not restore the original line")
	}
}

func TestAnniversaryCountdown(t *testing.T) {
	today := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	cases := map[string]string{"2026-10-14": "", "2026-10-13": "", "2026-10-15": "tomorrow", "2026-11-14": "in 31 days"}
	for date, want := range cases {
		d, _ := time.Parse("2006-01-02", date)
		if got := anniversaryCountdown(d, today); got != want {
			t.Errorf("%s: got %q, want %q", date, got, want)
		}
	}
}
