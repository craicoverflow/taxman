package interest

import (
	"testing"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestSources_ListsN26AndTradeRepublicInPickerOrder(t *testing.T) {
	got := Sources()
	if len(got) != 2 {
		t.Fatalf("expected 2 sources, got %d: %+v", len(got), got)
	}
	if got[0].Key != "n26" || got[1].Key != "traderepublic" {
		t.Errorf("unexpected source order: %s, %s", got[0].Key, got[1].Key)
	}
	// Sources returns a copy — mutating it must not affect the registry.
	got[0].Label = "mutated"
	if Sources()[0].Label != "N26" {
		t.Errorf("Sources() leaked its backing slice")
	}
}

func TestSourceByKey(t *testing.T) {
	if s, err := SourceByKey(""); err != nil || s.Key != Default.Key {
		t.Errorf(`SourceByKey("") = (%+v, %v), want the default source`, s, err)
	}
	if s, err := SourceByKey("traderepublic"); err != nil || s.Platform != ledger.PlatformTradeRepublic {
		t.Errorf(`SourceByKey("traderepublic") = (%+v, %v)`, s, err)
	}
	if _, err := SourceByKey("revolut"); err == nil {
		t.Error(`SourceByKey("revolut") should error on an unknown source, not fall back`)
	}
}

func TestSourceForPlatform(t *testing.T) {
	if s, ok := SourceForPlatform(ledger.PlatformN26); !ok || s.Key != "n26" {
		t.Errorf("SourceForPlatform(n26) = (%+v, %v)", s, ok)
	}
	if _, ok := SourceForPlatform(ledger.PlatformDegiro); ok {
		t.Error("SourceForPlatform(degiro) should report no manual-interest source")
	}
}

func TestSource_NewCredit_BuildsEURCreditForItsPlatform(t *testing.T) {
	for _, src := range Sources() {
		tx, err := src.NewCredit("2024-05-01", "12.34")
		if err != nil {
			t.Fatalf("%s NewCredit: %v", src.Key, err)
		}
		if tx.Platform != src.Platform {
			t.Errorf("%s: Platform = %q, want %q", src.Key, tx.Platform, src.Platform)
		}
		if tx.Type != ledger.TypeInterest {
			t.Errorf("%s: Type = %q, want interest", src.Key, tx.Type)
		}
		if tx.Currency != "EUR" {
			t.Errorf("%s: Currency = %q, want EUR", src.Key, tx.Currency)
		}
		if tx.Instrument == "" {
			t.Errorf("%s: Instrument is empty", src.Key)
		}
	}
}

func TestSource_NewCredit_PropagatesValidationErrors(t *testing.T) {
	if _, err := Default.NewCredit("not-a-date", "12.34"); err == nil {
		t.Error("expected NewCredit to surface a malformed-date error")
	}
	if _, err := Default.NewCredit("2024-05-01", "-1"); err == nil {
		t.Error("expected NewCredit to surface a non-positive-amount error")
	}
}
