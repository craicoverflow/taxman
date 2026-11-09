package manualtrade

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestNewTrade_BuildsManualBuyAndSell(t *testing.T) {
	cases := []struct {
		kind     string
		wantType ledger.Type
	}{
		{"buy", ledger.TypeBuy},
		{"sell", ledger.TypeSell},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			tx, err := NewTrade(tc.kind, " IE00B4L5Y983 ", "2024-06-20", "10.5", "112.40", "eur")
			if err != nil {
				t.Fatalf("NewTrade: %v", err)
			}
			if tx.Platform != ledger.PlatformManual {
				t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformManual)
			}
			if tx.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", tx.Type, tc.wantType)
			}
			if tx.Instrument != "IE00B4L5Y983" {
				t.Errorf("Instrument = %q, want trimmed IE00B4L5Y983", tx.Instrument)
			}
			if !tx.Quantity.Equal(decimal.RequireFromString("10.5")) {
				t.Errorf("Quantity = %s, want 10.5", tx.Quantity)
			}
			if !tx.Price.Equal(decimal.RequireFromString("112.40")) {
				t.Errorf("Price = %s, want 112.40", tx.Price)
			}
			if tx.Currency != "EUR" {
				t.Errorf("Currency = %q, want EUR (upper-cased)", tx.Currency)
			}
			if !tx.Date.Equal(time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("Date = %s, want 2024-06-20", tx.Date)
			}
		})
	}
}

func TestNewTrade_RejectsBadFields(t *testing.T) {
	cases := []struct {
		name                                         string
		kind, instrument, date, quantity, price, cur string
	}{
		{"unknown kind", "dividend", "ACME", "2024-06-20", "1", "1", "EUR"},
		{"empty kind", "", "ACME", "2024-06-20", "1", "1", "EUR"},
		{"rsu_vest is not a trade", "rsu_vest", "ACME", "2024-06-20", "1", "1", "EUR"},
		{"empty instrument", "buy", "  ", "2024-06-20", "1", "1", "EUR"},
		{"malformed date", "buy", "ACME", "20-06-2024", "1", "1", "EUR"},
		{"non-numeric quantity", "buy", "ACME", "2024-06-20", "lots", "1", "EUR"},
		{"zero quantity", "buy", "ACME", "2024-06-20", "0", "1", "EUR"},
		{"negative quantity", "buy", "ACME", "2024-06-20", "-1", "1", "EUR"},
		{"non-numeric price", "buy", "ACME", "2024-06-20", "1", "cheap", "EUR"},
		{"zero price", "buy", "ACME", "2024-06-20", "1", "0", "EUR"},
		{"negative price", "buy", "ACME", "2024-06-20", "1", "-1", "EUR"},
		{"empty currency is never defaulted", "buy", "ACME", "2024-06-20", "1", "1", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTrade(tc.kind, tc.instrument, tc.date, tc.quantity, tc.price, tc.cur); err == nil {
				t.Fatal("NewTrade: expected an error, got nil")
			}
		})
	}
}

func tx(t *testing.T, typ ledger.Type, instrument, date, qty string) ledger.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parsing %q: %v", date, err)
	}
	return ledger.Transaction{
		Platform:   ledger.PlatformManual,
		Type:       typ,
		Date:       d,
		Instrument: instrument,
		Quantity:   decimal.RequireFromString(qty),
		Price:      decimal.NewFromInt(1),
		Currency:   "EUR",
	}
}

func TestCheckNoOversell(t *testing.T) {
	cases := []struct {
		name     string
		existing []ledger.Transaction
		added    []ledger.Transaction
		wantErr  string // substring; empty means no error
	}{
		{
			name:     "sell within holdings",
			existing: []ledger.Transaction{tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "10")},
			added:    []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "10")},
		},
		{
			name:     "sell exceeding holdings names the shortfall",
			existing: []ledger.Transaction{tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "10")},
			added:    []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "10.5")},
			wantErr:  "exceeds",
		},
		{
			name:     "sell dated before the buy it depends on",
			existing: []ledger.Transaction{tx(t, ledger.TypeBuy, "ACME", "2024-03-01", "10")},
			added:    []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "5")},
			wantErr:  "exceeds",
		},
		{
			name: "buy and sell added together, buy first by date",
			added: []ledger.Transaction{
				tx(t, ledger.TypeSell, "ACME", "2024-02-01", "5"),
				tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "5"),
			},
		},
		{
			name:     "other instruments do not count",
			existing: []ledger.Transaction{tx(t, ledger.TypeBuy, "OTHER", "2024-01-01", "100")},
			added:    []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "1")},
			wantErr:  "exceeds",
		},
		{
			name:     "an RSU vest is a lot",
			existing: []ledger.Transaction{tx(t, ledger.TypeRSUVest, "ACME", "2024-01-01", "10")},
			added:    []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "10")},
		},
		{
			name: "earlier sales already consumed lots",
			existing: []ledger.Transaction{
				tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "10"),
				tx(t, ledger.TypeSell, "ACME", "2024-02-01", "8"),
			},
			added:   []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-03-01", "3")},
			wantErr: "exceeds",
		},
		{
			name: "a pre-existing problem in an unrelated holding does not block this entry",
			existing: []ledger.Transaction{
				tx(t, ledger.TypeSell, "BROKEN", "2024-01-01", "5"),
				tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "10"),
			},
			added: []ledger.Transaction{tx(t, ledger.TypeSell, "ACME", "2024-02-01", "10")},
		},
		{
			name: "same-day order follows the ledger: existing rows come first",
			existing: []ledger.Transaction{
				tx(t, ledger.TypeSell, "ACME", "2024-02-01", "5"),
				tx(t, ledger.TypeBuy, "ACME", "2024-01-01", "10"),
			},
			added: []ledger.Transaction{tx(t, ledger.TypeBuy, "ACME", "2024-02-01", "1")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckNoOversell(tc.existing, tc.added)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("CheckNoOversell: unexpected error %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("CheckNoOversell: expected an error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("CheckNoOversell: error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckHoldings_ChecksNamedInstrumentsOnly(t *testing.T) {
	txs := []ledger.Transaction{
		tx(t, ledger.TypeBuy, "A", "2024-01-01", "5"),
		tx(t, ledger.TypeSell, "A", "2024-02-01", "8"), // oversold
		tx(t, ledger.TypeBuy, "B", "2024-01-01", "5"),
	}
	if err := CheckHoldings(txs, "A"); err == nil {
		t.Error("A is oversold; want an error")
	}
	if err := CheckHoldings(txs, "B"); err != nil {
		t.Errorf("B is fine and A's problem must not block it; got %v", err)
	}
	if err := CheckHoldings(txs); err != nil {
		t.Errorf("no instruments named → nothing checked; got %v", err)
	}
}

func TestRemove_DropsOneMatchingRowOnly(t *testing.T) {
	a := tx(t, ledger.TypeBuy, "A", "2024-01-01", "5")
	b := tx(t, ledger.TypeSell, "A", "2024-02-01", "2")
	got := Remove([]ledger.Transaction{a, b}, a)
	if len(got) != 1 || got[0].Type != ledger.TypeSell {
		t.Fatalf("want only the sell left, got %+v", got)
	}
	// A target that is not present changes nothing.
	missing := tx(t, ledger.TypeBuy, "Z", "2024-01-01", "1")
	if got := Remove([]ledger.Transaction{a, b}, missing); len(got) != 2 {
		t.Errorf("absent target must leave the list intact, got %d rows", len(got))
	}
}

func TestRemove_DoesNotMutateInput(t *testing.T) {
	a := tx(t, ledger.TypeBuy, "A", "2024-01-01", "5")
	b := tx(t, ledger.TypeSell, "A", "2024-02-01", "2")
	in := []ledger.Transaction{a, b}
	_ = Remove(in, a)
	if in[0].Type != ledger.TypeBuy || in[1].Type != ledger.TypeSell {
		t.Errorf("input mutated: %+v", in)
	}
}
