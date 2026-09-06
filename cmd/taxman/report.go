package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// reportHolding is one holding's contribution to a tax-year report.
//
// For CGT_ASSET holdings only RealisedGain is populated — the taxable
// amount and tax due are a personal, year-level figure (one €1,270
// exemption, gains and losses netted across every holding, losses
// carried forward) reported once in yearReport.CGT, not per holding.
// Exit-tax holdings keep a per-holding TaxableGain/TaxDue because that
// regime has no exemption and no cross-holding relief.
type reportHolding struct {
	Instrument     string `json:"instrument"`
	Classification string `json:"classification"`
	Unclassified   bool   `json:"unclassified"`
	RealisedGain   string `json:"realised_gain,omitempty"` // net in-year gain/loss; CGT + exit-tax holdings
	TaxableGain    string `json:"taxable_gain,omitempty"`  // exit-tax holdings only
	TaxDue         string `json:"tax_due,omitempty"`       // exit-tax holdings only
	Interest       string `json:"interest,omitempty"`
	Error          string `json:"error,omitempty"`

	kind string // internal only ("cgt"/"exit_tax"), drives text rendering; not marshaled
}

// cgtYearSummary is the year-level CGT charge across every CGT_ASSET
// holding — see engine.CGTYearResult.
type cgtYearSummary struct {
	NetChargeableGain      string `json:"net_chargeable_gain"`
	LossBroughtForward     string `json:"loss_brought_forward"`
	LossBroughtForwardUsed string `json:"loss_brought_forward_used"`
	LossCarriedForward     string `json:"loss_carried_forward"`
	AnnualExemptionUsed    string `json:"annual_exemption_used"`
	TaxableGain            string `json:"taxable_gain"`
	TaxDue                 string `json:"tax_due"`
	Error                  string `json:"error,omitempty"`
}

// yearReport is the full report for one tax year.
type yearReport struct {
	Year     int             `json:"year"`
	Holdings []reportHolding `json:"holdings"`
	CGT      *cgtYearSummary `json:"cgt,omitempty"`
}

// runReport implements `taxman report --year <YYYY> [--format text|json] [--db path]`.
//
// PDF output is deliberately not implemented — per the user's answer
// when this task was scoped, generating a PDF needs a new
// third-party dependency (Go's standard library has none), which
// SPEC.md §6 requires asking about before adding. --format pdf
// returns an explicit "not yet supported" error rather than silently
// falling back to another format.
func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	yearFlag := fs.Int("year", 0, "tax year to report on, e.g. 2024")
	format := fs.String("format", "text", "output format: text or json")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *yearFlag == 0 {
		return fmt.Errorf("usage: taxman report --year <YYYY> [--format text|json] [--db path]")
	}
	year := *yearFlag

	switch *format {
	case "text", "json":
	case "pdf":
		return fmt.Errorf("report: --format pdf is not yet supported (needs a new dependency — deferred pending a library choice)")
	default:
		return fmt.Errorf("report: unsupported format %q (expected text or json)", *format)
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Up(conn, db.Migrations); err != nil {
		return fmt.Errorf("ensuring schema is up to date: %w", err)
	}

	report, err := buildYearReport(conn, year)
	if err != nil {
		return err
	}

	switch *format {
	case "json":
		return printJSON(report)
	default:
		printText(report)
		return nil
	}
}

func buildYearReport(conn *sql.DB, year int) (*yearReport, error) {
	txs, err := ledger.NewStore(conn).All()
	if err != nil {
		return nil, fmt.Errorf("report: loading transactions: %w", err)
	}

	classifier := classify.NewStore(conn)

	byInstrument := map[string][]ledger.Transaction{}
	for _, tx := range txs {
		byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
	}

	instruments := make([]string, 0, len(byInstrument))
	for instrument := range byInstrument {
		instruments = append(instruments, instrument)
	}
	sort.Strings(instruments)

	report := &yearReport{Year: year}

	// CGT is assessed at the year level across every CGT_ASSET holding:
	// FIFO-match each holding here, collect the disposals, and hand the
	// lot to engine.AggregateCGTYear after the per-holding loop.
	cgtDisposalsByHolding := map[string][]engine.Disposal{}
	cgtRowIdx := map[string]int{} // instrument -> index into report.Holdings
	cgtMatchBlocked := false

	for _, instrument := range instruments {
		classification, err := classifier.Classify(instrument)
		if err != nil {
			return nil, fmt.Errorf("report: classifying %s: %w", instrument, err)
		}

		holding := reportHolding{
			Instrument:     instrument,
			Classification: string(classification),
			Unclassified:   classification == classify.Unclassified,
		}

		if holding.Unclassified {
			report.Holdings = append(report.Holdings, holding)
			continue
		}

		holdingTxs := byInstrument[instrument]

		switch classification {
		case classify.CGTAsset:
			holding.kind = "cgt"
			disposals, err := engine.CGTDisposals(holdingTxs)
			if err != nil {
				holding.Error = err.Error()
				cgtMatchBlocked = true
			} else {
				cgtDisposalsByHolding[instrument] = disposals
				cgtRowIdx[instrument] = len(report.Holdings)
			}

		case classify.ExitTaxFund:
			holding.kind = "exit_tax"
			realisedGain, taxableGain, taxDue, err := reportYearExitTax(holdingTxs, year)
			if err != nil {
				holding.Error = err.Error()
			} else {
				holding.RealisedGain = realisedGain.String()
				holding.TaxableGain = taxableGain.String()
				holding.TaxDue = taxDue.String()
			}
		}

		// DIRT is orthogonal to CGT/exit-tax classification — any
		// holding's transactions may include interest credits (in
		// practice N26's savings "holding" is the only one that will,
		// but nothing in the domain model restricts it structurally).
		if interest, hasInterest := reportYearDIRT(holdingTxs, year); hasInterest {
			holding.Interest = interest.String()
			if holding.kind == "" {
				holding.kind = "dirt"
			}
		}

		report.Holdings = append(report.Holdings, holding)
	}

	report.CGT = buildCGTYearSummary(report, year, cgtDisposalsByHolding, cgtRowIdx, cgtMatchBlocked)

	return report, nil
}

// buildCGTYearSummary runs engine.AggregateCGTYear over the FIFO-matched
// CGT holdings and fills both the year-level summary and each CGT
// holding row's RealisedGain. It returns nil when there are no CGT
// holdings at all. If any CGT holding failed to FIFO-match
// (matchBlocked), no aggregate charge is shown — a partial total would
// understate the liability — only an error pointing at the per-holding
// errors already on the rows.
func buildCGTYearSummary(
	report *yearReport,
	year int,
	disposalsByHolding map[string][]engine.Disposal,
	rowIdx map[string]int,
	matchBlocked bool,
) *cgtYearSummary {
	if len(disposalsByHolding) == 0 && !matchBlocked {
		return nil
	}
	if matchBlocked {
		return &cgtYearSummary{
			Error: "one or more CGT holdings could not be matched (see per-holding errors); the aggregate CGT charge is not shown",
		}
	}

	cy, err := engine.AggregateCGTYear(disposalsByHolding, year)
	if err != nil {
		return &cgtYearSummary{Error: err.Error()}
	}

	for _, h := range cy.Holdings {
		if idx, ok := rowIdx[h.Instrument]; ok {
			report.Holdings[idx].RealisedGain = h.RealisedGain.String()
		}
	}

	return &cgtYearSummary{
		NetChargeableGain:      cy.NetChargeableGain.String(),
		LossBroughtForward:     cy.LossBroughtForward.String(),
		LossBroughtForwardUsed: cy.LossBroughtForwardUsed.String(),
		LossCarriedForward:     cy.LossCarriedForward.String(),
		AnnualExemptionUsed:    cy.AnnualExemptionUsed.String(),
		TaxableGain:            cy.TaxableGain.String(),
		TaxDue:                 cy.TaxDue.String(),
	}
}

// reportYearExitTax computes a single exit-tax holding's in-year
// position: realised gain (raw sum, may be negative), taxable gain
// (positive disposals only — no loss relief), and tax due.
func reportYearExitTax(txs []ledger.Transaction, year int) (realisedGain, taxableGain, taxDue decimal.Decimal, err error) {
	result, err := engine.ComputeExitTaxForYear(txs, year)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}
	return result.TotalGain, result.TaxableGain, result.TaxDue, nil
}

// reportYearDIRT computes DIRT on interest credits dated within year,
// returning hasInterest=false if there were none (distinguishing "no
// interest this year" from "zero interest, but there was a credit").
func reportYearDIRT(txs []ledger.Transaction, year int) (taxDue decimal.Decimal, hasInterest bool) {
	var hasAny bool
	for _, tx := range txs {
		if tx.Type == ledger.TypeInterest && tx.Date.Year() == year {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return decimal.Zero, false
	}

	result, err := engine.ComputeDIRTForYear(txs, year)
	if err != nil {
		return decimal.Zero, true // hasInterest is still true; the amount just couldn't be computed
	}
	return result.TaxDue, true
}

func printJSON(report *yearReport) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func printText(report *yearReport) {
	fmt.Printf("Tax year %d\n", report.Year)
	fmt.Println(strings.Repeat("=", 40))
	for _, h := range report.Holdings {
		if h.Unclassified {
			fmt.Printf("%s: UNCLASSIFIED (set a classification before this holding's tax can be computed)\n", h.Instrument)
			continue
		}
		fmt.Printf("%s (%s)\n", h.Instrument, h.Classification)
		if h.Error != "" {
			fmt.Printf("  error: %s\n", h.Error)
			continue
		}
		switch h.kind {
		case "cgt":
			// CGT tax is reported once, year-level, below — only the
			// per-holding realised gain belongs on the row.
			fmt.Printf("  CGT realised gain (this year): %s\n", h.RealisedGain)
		case "exit_tax":
			fmt.Printf("  exit tax realised gain (this year): %s\n", h.RealisedGain)
			fmt.Printf("  exit tax taxable gain: %s\n", h.TaxableGain)
			fmt.Printf("  exit tax tax due: %s\n", h.TaxDue)
		}
		if h.Interest != "" {
			fmt.Printf("  DIRT due on interest: %s\n", h.Interest)
		}
	}

	if c := report.CGT; c != nil {
		fmt.Println(strings.Repeat("-", 40))
		fmt.Println("CGT (year-level: one €1,270 exemption, gains/losses netted across holdings)")
		if c.Error != "" {
			fmt.Printf("  error: %s\n", c.Error)
		} else {
			fmt.Printf("  net chargeable gain:        %s\n", c.NetChargeableGain)
			fmt.Printf("  loss brought forward:       %s (used: %s)\n", c.LossBroughtForward, c.LossBroughtForwardUsed)
			fmt.Printf("  annual exemption used:      %s\n", c.AnnualExemptionUsed)
			fmt.Printf("  taxable gain:               %s\n", c.TaxableGain)
			fmt.Printf("  CGT tax due:                %s\n", c.TaxDue)
			fmt.Printf("  loss carried forward:       %s\n", c.LossCarriedForward)
		}
	}
}
