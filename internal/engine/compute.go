package engine

import (
	"fmt"
	"sort"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// Classifier resolves a holding's tax classification. internal/classify.Store
// satisfies this; tests use a lightweight fake to keep engine's own
// tests free of a SQLite dependency, per SPEC.md §4.
type Classifier interface {
	Classify(instrument string) (classify.Classification, error)
}

// UnclassifiedError blocks computation for a single holding. Compute
// does not fail the whole run on an UnclassifiedError — every other
// classified holding in the same run still computes. See SPEC.md §2:
// classification is a "blocking state... before any tax computation
// touches that holding's lots" — read as a per-holding block, not a
// whole-run abort.
type UnclassifiedError struct {
	Instrument string
}

func (e *UnclassifiedError) Error() string {
	return fmt.Sprintf("engine: holding %s is unclassified; set its classification before computing tax on it", e.Instrument)
}

// HoldingResult is the outcome of computing tax for a single holding's
// transactions. Err is set (typically to an *UnclassifiedError, or —
// once later tasks add them — an unverified-rule error) when this
// holding's computation was blocked; downstream liability fields land
// in later tasks (4.2+).
type HoldingResult struct {
	Instrument     string
	Classification classify.Classification
	Transactions   []ledger.Transaction
	Err            error
}

// Result is the outcome of a full Compute run, one HoldingResult per
// distinct instrument seen across the input transactions.
type Result struct {
	Holdings map[string]*HoldingResult
}

// Compute groups txs by instrument, resolves each holding's
// Classification via classifier, and blocks any Unclassified holding
// with an *UnclassifiedError recorded on its HoldingResult — without
// aborting computation for the other, classified holdings in the same
// run. Actual liability computation (FIFO matching, deemed disposal,
// etc.) is added by later tasks; this entry point only establishes the
// per-holding guard those tasks build on.
func Compute(txs []ledger.Transaction, classifier Classifier) (*Result, error) {
	byInstrument := map[string][]ledger.Transaction{}
	for _, tx := range txs {
		byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
	}

	// Deterministic iteration order for reproducible results/tests.
	instruments := make([]string, 0, len(byInstrument))
	for instrument := range byInstrument {
		instruments = append(instruments, instrument)
	}
	sort.Strings(instruments)

	holdings := map[string]*HoldingResult{}
	for _, instrument := range instruments {
		classification, err := classifier.Classify(instrument)
		if err != nil {
			return nil, fmt.Errorf("engine: classifying %s: %w", instrument, err)
		}

		result := &HoldingResult{
			Instrument:     instrument,
			Classification: classification,
			Transactions:   byInstrument[instrument],
		}

		if classification == classify.Unclassified {
			result.Err = &UnclassifiedError{Instrument: instrument}
		}

		holdings[instrument] = result
	}

	return &Result{Holdings: holdings}, nil
}
