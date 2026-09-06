package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// fakeClassifier is a map-backed test double, used so engine tests
// stay pure and independent of internal/classify's SQLite backing —
// see SPEC.md §4's rationale for keeping engine dependency-free.
type fakeClassifier map[string]classify.Classification

func (f fakeClassifier) Classify(instrument string) (classify.Classification, error) {
	if c, ok := f[instrument]; ok {
		return c, nil
	}
	return classify.Unclassified, nil
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func buyTx(t *testing.T, instrument, date string, qty, price int64) ledger.Transaction {
	t.Helper()
	return ledger.Transaction{
		Platform:   ledger.PlatformDegiro,
		Type:       ledger.TypeBuy,
		Date:       mustDate(t, date),
		Instrument: instrument,
		Quantity:   decimal.NewFromInt(qty),
		Price:      decimal.NewFromInt(price),
		Currency:   "EUR",
	}
}

func TestCompute_UnclassifiedHolding_BlocksOnlyThatHolding(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "UNKNOWN", "2024-01-01", 10, 100),
		buyTx(t, "AAPL", "2024-01-01", 5, 50),
	}
	classifier := fakeClassifier{
		"AAPL": classify.CGTAsset,
		// UNKNOWN deliberately has no entry.
	}

	result, err := Compute(txs, classifier)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	unknown, ok := result.Holdings["UNKNOWN"]
	if !ok {
		t.Fatal("expected UNKNOWN to appear in results")
	}
	var unclassifiedErr *UnclassifiedError
	if !errors.As(unknown.Err, &unclassifiedErr) {
		t.Errorf("expected UNKNOWN's Err to be an *UnclassifiedError, got %v", unknown.Err)
	}
	if unclassifiedErr != nil && unclassifiedErr.Instrument != "UNKNOWN" {
		t.Errorf("UnclassifiedError.Instrument = %q, want UNKNOWN", unclassifiedErr.Instrument)
	}

	aapl, ok := result.Holdings["AAPL"]
	if !ok {
		t.Fatal("expected AAPL to appear in results")
	}
	if aapl.Err != nil {
		t.Errorf("expected AAPL (classified) to compute without error, got %v", aapl.Err)
	}
}

func TestCompute_AllClassified_NoBlocking(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 5, 50),
	}
	classifier := fakeClassifier{"AAPL": classify.CGTAsset}

	result, err := Compute(txs, classifier)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if result.Holdings["AAPL"].Err != nil {
		t.Errorf("expected no error for a classified holding, got %v", result.Holdings["AAPL"].Err)
	}
}

func TestCompute_ClassifierError_Propagates(t *testing.T) {
	txs := []ledger.Transaction{buyTx(t, "AAPL", "2024-01-01", 5, 50)}
	classifier := erroringClassifier{}

	_, err := Compute(txs, classifier)
	if err == nil {
		t.Fatal("expected Compute to propagate a classifier lookup error")
	}
}

type erroringClassifier struct{}

func (erroringClassifier) Classify(instrument string) (classify.Classification, error) {
	return "", errors.New("boom")
}
