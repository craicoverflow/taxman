package features

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// worldKey retrieves the per-scenario world from the context godog
// threads through the steps.
type worldKey struct{}

func worldFrom(ctx context.Context) *world {
	w, _ := ctx.Value(worldKey{}).(*world)
	return w
}

// holding is one security under test: its classification (which decides
// the regime a When step runs) and the raw ledger events entered for it.
type holding struct {
	name           string
	classification classify.Classification
	txs            []ledger.Transaction
}

// world is the state one scenario builds up (Given), acts on (When) and
// checks (Then). Exactly one result field is populated by the When step.
type world struct {
	scenarioURI  string
	scenarioLine int

	order    []string
	holdings map[string]*holding
	current  string

	cgt        *engine.CGTResult
	cgtYear    *engine.CGTYearResult
	exitTax    *engine.ExitTaxResult
	dirt       *engine.DIRTResult
	disposals  []engine.Disposal
	rate       *taxrules.Rate
	fxRate     *decimal.Decimal
	deemedDate *time.Time
	blocked    *engine.Result // from engine.Compute, for the unclassified guard
	whenErr    error
}

func newWorld(uri string, line int) *world {
	return &world{
		scenarioURI:  uri,
		scenarioLine: line,
		holdings:     map[string]*holding{},
	}
}

// holdingNamed returns the named holding, creating it (Unclassified) on
// first mention so a bare "a buy of ..." step can implicitly start one.
func (w *world) holdingNamed(name string) *holding {
	h, ok := w.holdings[name]
	if !ok {
		h = &holding{name: name, classification: classify.Unclassified}
		w.holdings[name] = h
		w.order = append(w.order, name)
	}
	w.current = name
	return h
}

func (w *world) currentHolding() (*holding, error) {
	if w.current == "" {
		return nil, fmt.Errorf("no holding referenced yet in this scenario")
	}
	return w.holdings[w.current], nil
}

// needOK is called by a success-path Then step: if the When step stashed
// an engine error, surface it here rather than reporting a confusing
// "no result" further down.
func (w *world) needOK() error {
	if w.whenErr != nil {
		return fmt.Errorf("the computation returned an error: %w", w.whenErr)
	}
	return nil
}

// allTxns returns every holding's events, in the order holdings were
// introduced — the input to the year-level aggregation steps.
func (w *world) allTxns() []ledger.Transaction {
	var out []ledger.Transaction
	for _, name := range w.order {
		out = append(out, w.holdings[name].txs...)
	}
	return out
}

func (w *world) txnsByInstrument() map[string][]ledger.Transaction {
	out := map[string][]ledger.Transaction{}
	for _, name := range w.order {
		out[name] = w.holdings[name].txs
	}
	return out
}

// ---- parsing helpers -------------------------------------------------

const dateLayout = "2006-01-02"

func parseDate(s string) (time.Time, error) {
	return time.Parse(dateLayout, strings.TrimSpace(s))
}

// parseAmount accepts "1000", "1,270.00", "€100", "$99.50", "-40".
func parseAmount(s string) (decimal.Decimal, error) {
	clean := strings.NewReplacer(",", "", "€", "", "$", "", "£", "", " ", "").Replace(strings.TrimSpace(s))
	d, err := decimal.NewFromString(clean)
	if err != nil {
		return decimal.Zero, fmt.Errorf("not a number: %q", s)
	}
	return d, nil
}

// ---- formatting helpers (canonical .feature value tokens) -----------

// formatEUR renders a euro figure the way the .feature files pin it:
// "€1,270.00", "-€40.00". Two decimals, thousands separators.
func formatEUR(d decimal.Decimal) string {
	neg := d.IsNegative()
	abs := d.Abs().StringFixed(2)
	intPart, frac, _ := strings.Cut(abs, ".")
	intPart = groupThousands(intPart)
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s€%s.%s", sign, intPart, frac)
}

func groupThousands(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if n > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// formatPercent renders a rate fraction (0.33) as "33%", (0.385) as
// "38.5%" — trailing zeros trimmed.
func formatPercent(rateFraction decimal.Decimal) string {
	p := rateFraction.Mul(decimal.NewFromInt(100)).String()
	if strings.Contains(p, ".") {
		p = strings.TrimRight(strings.TrimRight(p, "0"), ".")
	}
	return p + "%"
}

// formatRate renders a euro-per-unit FX rate to 6 decimal places, the
// precision the .feature files pin exchange rates at.
func formatRate(d decimal.Decimal) string {
	return d.StringFixed(6)
}

// assertDecimal is the shared body of every "Then ... is €X" step: in
// regen mode it records got; otherwise it compares got to the pinned
// want. label/stepText carry through for the regen locator and the
// failure message.
func assertDecimal(w *world, stepText, want string, got decimal.Decimal, render func(decimal.Decimal) string) error {
	if err := w.needOK(); err != nil {
		return err
	}
	rendered := render(got)
	if regenMode {
		recordPinned(w.scenarioURI, w.scenarioLine, stepText, rendered)
		return nil
	}
	wantD, err := parseAmount(want)
	if err != nil {
		return fmt.Errorf("pinned value %q in the feature file is unparseable: %w", want, err)
	}
	// Compare on the rendered form so a pinned "€1,230.00" matches an
	// engine value of 1230 exactly, and a real discrepancy (1230.90 vs
	// 1230) is caught.
	if render(wantD) != rendered {
		return fmt.Errorf("engine produced %s, feature file pins %s — regenerate with `make regen-features` if the engine is right, otherwise the number or the rule is wrong", rendered, render(wantD))
	}
	return nil
}

func assertString(w *world, stepText, want, got string) error {
	if err := w.needOK(); err != nil {
		return err
	}
	if regenMode {
		recordPinned(w.scenarioURI, w.scenarioLine, stepText, quoteOrBare(got))
		return nil
	}
	if strings.Trim(want, `"`) != got {
		return fmt.Errorf("engine produced %q, feature file pins %q", got, strings.Trim(want, `"`))
	}
	return nil
}

func quoteOrBare(s string) string {
	if _, err := parseDate(s); err == nil {
		return s
	}
	return `"` + s + `"`
}
