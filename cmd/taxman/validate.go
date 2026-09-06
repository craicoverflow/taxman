package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// disposalJSON mirrors engine.Disposal's fields with the JSON tags
// used by testdata/golden/*/expected.json. A local type rather than
// adding json tags to engine.Disposal itself, since engine has no
// reason to know about this CLI's serialization format.
type disposalJSON struct {
	Date      string `json:"date"`
	Quantity  string `json:"quantity"`
	Proceeds  string `json:"proceeds"`
	CostBasis string `json:"cost_basis"`
	Gain      string `json:"gain"`
}

// cgtResultJSON and exitTaxResultJSON mirror the shape of
// testdata/golden/cgt_simple_fifo/expected.json and
// testdata/golden/exittax_actual_disposal/expected.json respectively.
type cgtResultJSON struct {
	Disposals   []disposalJSON `json:"disposals"`
	TotalGain   string         `json:"total_gain"`
	TaxableGain string         `json:"taxable_gain"`
	TaxDue      string         `json:"tax_due"`
}

// dirtResultJSON mirrors testdata/golden/dirt_simple/expected.json.
type dirtResultJSON struct {
	TotalInterest string `json:"total_interest"`
	TaxDue        string `json:"tax_due"`
}

// cgtYearHoldingJSON and cgtYearResultJSON mirror
// testdata/golden/cgt_year_*/expected.json — the aggregate,
// year-level CGT charge across every CGT_ASSET holding (one €1,270
// exemption, gains/losses netted, losses carried forward).
type cgtYearHoldingJSON struct {
	Instrument   string         `json:"instrument"`
	Disposals    []disposalJSON `json:"disposals"`
	RealisedGain string         `json:"realised_gain"`
}

type cgtYearResultJSON struct {
	Year                   int                  `json:"year"`
	Holdings               []cgtYearHoldingJSON `json:"holdings"`
	NetChargeableGain      string               `json:"net_chargeable_gain"`
	LossBroughtForward     string               `json:"loss_brought_forward"`
	LossBroughtForwardUsed string               `json:"loss_brought_forward_used"`
	LossCarriedForward     string               `json:"loss_carried_forward"`
	AnnualExemptionUsed    string               `json:"annual_exemption_used"`
	TaxableGain            string               `json:"taxable_gain"`
	TaxDue                 string               `json:"tax_due"`
}

var validKinds = map[string]bool{"cgt": true, "cgt_year": true, "exit_tax": true, "dirt": true}

// runValidate implements `taxman validate --fixture <dir> --kind <cgt|exit_tax|dirt>`.
// dir must contain input.csv (platform,type,date,instrument,quantity,
// price,currency — see any testdata/golden/*/input.csv) and
// expected.json (shape depends on kind — see the *ResultJSON types
// above). It runs the corresponding engine.Compute* function against
// input.csv and reports a diff against expected.json rather than
// silently passing on any mismatch.
func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fixtureDir := fs.String("fixture", "", "path to a golden fixture directory (must contain input.csv and expected.json)")
	kind := fs.String("kind", "", "which engine computation to validate: cgt, cgt_year, exit_tax, or dirt")
	year := fs.Int("year", 0, "tax year (required for --kind cgt_year)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *fixtureDir == "" {
		return fmt.Errorf("usage: taxman validate --fixture <dir> --kind <cgt|cgt_year|exit_tax|dirt> [--year YYYY]")
	}
	if !validKinds[*kind] {
		return fmt.Errorf("validate: invalid --kind %q (expected cgt, cgt_year, exit_tax, or dirt)", *kind)
	}
	if *kind == "cgt_year" && *year == 0 {
		return fmt.Errorf("validate: --kind cgt_year requires --year YYYY")
	}

	txs, err := readFixtureInput(filepath.Join(*fixtureDir, "input.csv"))
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	expectedBytes, err := os.ReadFile(filepath.Join(*fixtureDir, "expected.json"))
	if err != nil {
		return fmt.Errorf("validate: reading expected.json: %w", err)
	}

	actual, err := computeForKind(*kind, txs, *year)
	if err != nil {
		return fmt.Errorf("validate: computing %s: %w", *kind, err)
	}

	actualBytes, err := json.Marshal(actual)
	if err != nil {
		return fmt.Errorf("validate: marshaling actual result: %w", err)
	}

	match, diff, err := jsonEqual(expectedBytes, actualBytes)
	if err != nil {
		return fmt.Errorf("validate: comparing results: %w", err)
	}
	if !match {
		return fmt.Errorf("validate: %s did not match expected.json:\n  expected: %s\n  actual:   %s", *fixtureDir, diff.expected, diff.actual)
	}

	fmt.Printf("validate: %s (%s) passed\n", *fixtureDir, *kind)
	return nil
}

func computeForKind(kind string, txs []ledger.Transaction, year int) (interface{}, error) {
	switch kind {
	case "cgt":
		result, err := engine.ComputeCGT(txs)
		if err != nil {
			return nil, err
		}
		return toCGTResultJSON(result), nil

	case "cgt_year":
		byHolding := map[string][]engine.Disposal{}
		for instrument, holdingTxs := range groupByInstrument(txs) {
			disposals, err := engine.CGTDisposals(holdingTxs)
			if err != nil {
				return nil, err
			}
			byHolding[instrument] = disposals
		}
		result, err := engine.AggregateCGTYear(byHolding, year)
		if err != nil {
			return nil, err
		}
		return toCGTYearResultJSON(result), nil

	case "exit_tax":
		result, err := engine.ComputeExitTax(txs)
		if err != nil {
			return nil, err
		}
		return toExitTaxResultJSON(result), nil

	case "dirt":
		result, err := engine.ComputeDIRT(txs)
		if err != nil {
			return nil, err
		}
		return dirtResultJSON{
			TotalInterest: result.TotalInterest.String(),
			TaxDue:        result.TaxDue.String(),
		}, nil

	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
}

func toCGTResultJSON(r *engine.CGTResult) cgtResultJSON {
	return cgtResultJSON{
		Disposals:   toDisposalsJSON(r.Disposals),
		TotalGain:   r.TotalGain.String(),
		TaxableGain: r.TaxableGain.String(),
		TaxDue:      r.TaxDue.String(),
	}
}

func toExitTaxResultJSON(r *engine.ExitTaxResult) cgtResultJSON {
	return cgtResultJSON{
		Disposals:   toDisposalsJSON(r.Disposals),
		TotalGain:   r.TotalGain.String(),
		TaxableGain: r.TaxableGain.String(),
		TaxDue:      r.TaxDue.String(),
	}
}

func groupByInstrument(txs []ledger.Transaction) map[string][]ledger.Transaction {
	out := map[string][]ledger.Transaction{}
	for _, tx := range txs {
		out[tx.Instrument] = append(out[tx.Instrument], tx)
	}
	return out
}

func toCGTYearResultJSON(r *engine.CGTYearResult) cgtYearResultJSON {
	holdings := make([]cgtYearHoldingJSON, len(r.Holdings))
	for i, h := range r.Holdings {
		holdings[i] = cgtYearHoldingJSON{
			Instrument:   h.Instrument,
			Disposals:    toDisposalsJSON(h.Disposals),
			RealisedGain: h.RealisedGain.String(),
		}
	}
	return cgtYearResultJSON{
		Year:                   r.Year,
		Holdings:               holdings,
		NetChargeableGain:      r.NetChargeableGain.String(),
		LossBroughtForward:     r.LossBroughtForward.String(),
		LossBroughtForwardUsed: r.LossBroughtForwardUsed.String(),
		LossCarriedForward:     r.LossCarriedForward.String(),
		AnnualExemptionUsed:    r.AnnualExemptionUsed.String(),
		TaxableGain:            r.TaxableGain.String(),
		TaxDue:                 r.TaxDue.String(),
	}
}

func toDisposalsJSON(disposals []engine.Disposal) []disposalJSON {
	out := make([]disposalJSON, len(disposals))
	for i, d := range disposals {
		out[i] = disposalJSON{
			Date:      d.Date.Format("2006-01-02"),
			Quantity:  d.Quantity.String(),
			Proceeds:  d.Proceeds.String(),
			CostBasis: d.CostBasis.String(),
			Gain:      d.Gain.String(),
		}
	}
	return out
}

// readFixtureInput parses a golden fixture's input.csv: header
// platform,type,date,instrument,quantity,price,currency.
func readFixtureInput(path string) ([]ledger.Transaction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header of %s: %w", path, err)
	}
	wantHeader := []string{"platform", "type", "date", "instrument", "quantity", "price", "currency"}
	if len(header) != len(wantHeader) {
		return nil, fmt.Errorf("%s: expected header %v, got %v", path, wantHeader, header)
	}
	for i, want := range wantHeader {
		if strings.TrimSpace(header[i]) != want {
			return nil, fmt.Errorf("%s: expected header %v, got %v", path, wantHeader, header)
		}
	}

	var out []ledger.Transaction
	rowNum := 1
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: reading row %d: %w", path, rowNum+1, err)
		}
		rowNum++

		if len(record) != len(wantHeader) {
			return nil, fmt.Errorf("%s: row %d: expected %d columns, got %d", path, rowNum, len(wantHeader), len(record))
		}

		date, err := time.Parse("2006-01-02", strings.TrimSpace(record[2]))
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: parsing date %q: %w", path, rowNum, record[2], err)
		}
		quantity, err := decimal.NewFromString(strings.TrimSpace(record[4]))
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: parsing quantity %q: %w", path, rowNum, record[4], err)
		}
		price, err := decimal.NewFromString(strings.TrimSpace(record[5]))
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: parsing price %q: %w", path, rowNum, record[5], err)
		}

		out = append(out, ledger.Transaction{
			Platform:   ledger.Platform(strings.TrimSpace(record[0])),
			Type:       ledger.Type(strings.TrimSpace(record[1])),
			Date:       date.UTC(),
			Instrument: strings.TrimSpace(record[3]),
			Quantity:   quantity,
			Price:      price,
			Currency:   strings.TrimSpace(record[6]),
		})
	}

	return out, nil
}

type jsonDiff struct {
	expected string
	actual   string
}

// jsonEqual compares two JSON byte slices for deep equality after
// unmarshaling (so key order and whitespace differences don't cause a
// false mismatch), returning a human-readable diff on failure.
func jsonEqual(expected, actual []byte) (bool, jsonDiff, error) {
	var expectedVal, actualVal interface{}
	if err := json.Unmarshal(expected, &expectedVal); err != nil {
		return false, jsonDiff{}, fmt.Errorf("parsing expected.json: %w", err)
	}
	if err := json.Unmarshal(actual, &actualVal); err != nil {
		return false, jsonDiff{}, fmt.Errorf("parsing actual result: %w", err)
	}

	if reflect.DeepEqual(expectedVal, actualVal) {
		return true, jsonDiff{}, nil
	}

	prettyExpected, _ := json.MarshalIndent(expectedVal, "", "  ")
	prettyActual, _ := json.MarshalIndent(actualVal, "", "  ")
	return false, jsonDiff{expected: string(prettyExpected), actual: string(prettyActual)}, nil
}
