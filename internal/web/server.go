package web

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/audit"
	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/fx"
	"github.com/craicoverflow/taxman/internal/ingest"
	"github.com/craicoverflow/taxman/internal/ingest/etrade"
	"github.com/craicoverflow/taxman/internal/ingest/interest"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/prices"
	"github.com/craicoverflow/taxman/internal/tickers"
)

// maxUploadSize bounds how much of a multipart upload is buffered in
// memory before parsing. CSV statements are small; this is generous
// headroom, not a real capacity limit.
const maxUploadSize = 32 << 20 // 32 MiB

// interestBatchRows is how many blank date/amount rows the "Log
// interest payments" grid renders to start. It's one — the common
// case is logging a single credit — and the "Add row" button clones
// more when a batch is wanted. Blank rows are ignored on submit, so
// this is purely the initial count.
const interestBatchRows = 1

// appCSS is the whole UI stylesheet, embedded so the binary still needs
// no build step and no filesystem at runtime. Served verbatim at
// /static/app.css by handleStaticCSS.
//
//go:embed static/app.css
var appCSS string

// cssVersion is a short fingerprint of appCSS, appended to the
// stylesheet link as ?v=… (see pageShell). Because the URL changes
// whenever the embedded CSS changes, a browser refetches the new
// stylesheet immediately instead of serving a stale cached copy for
// up to the Cache-Control lifetime — the reason a CSS-only change
// could look like it "didn't take" after a rebuild.
var cssVersion = func() string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(appCSS))
	return strconv.FormatUint(uint64(h.Sum32()), 36)
}()

// Server serves the taxman dashboard over HTTP. See SPEC.md §7.
type Server struct {
	conn          *sql.DB
	ledgerStore   *ledger.Store
	classifyStore *classify.Store
	auditStore    *audit.Store
	tickerStore   *tickers.Store

	// quoter fetches live market prices for the portfolio page; rate
	// converts a quote's native currency to euro. Both are seams for
	// tests: NewServer wires the real HTTP client and fx.Rate, and
	// internal web tests overwrite these fields with fakes so no test
	// touches the network or depends on the embedded FX file's age.
	quoter prices.Quoter
	rate   func(currency string, on time.Time) (decimal.Decimal, error)
}

// NewServer wraps an already-migrated *sql.DB. Callers are
// responsible for having run db.Up beforehand (see internal/db).
func NewServer(conn *sql.DB) *Server {
	return &Server{
		conn:          conn,
		ledgerStore:   ledger.NewStore(conn),
		classifyStore: classify.NewStore(conn),
		auditStore:    audit.NewStore(conn),
		tickerStore:   tickers.NewStore(conn),
		quoter:        prices.NewHTTPQuoter(prices.WithCache(prices.NewSQLiteCache(conn))),
		rate:          fx.Rate,
	}
}

// Handler returns the http.Handler serving every route this Server
// exposes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/audit/", s.handleAuditDetail)
	mux.HandleFunc("/import", s.handleImport)
	mux.HandleFunc("/classify", s.handleClassify)
	mux.HandleFunc("/interest", s.handleInterest)
	mux.HandleFunc("/interest/update", s.handleInterestUpdate)
	mux.HandleFunc("/interest/delete", s.handleInterestDelete)
	mux.HandleFunc("/rsu", s.handleRSUVest)
	mux.HandleFunc("/portfolio", s.handlePortfolio)
	mux.HandleFunc("/portfolio/ticker", s.handlePortfolioTicker)
	mux.HandleFunc("/static/app.css", s.handleStaticCSS)
	return mux
}

// handleStaticCSS serves the embedded stylesheet. It's a single file
// baked into the binary, so there's no directory to traverse and no
// disk read — just the bytes. pageShell links it with a ?v=<hash>
// that changes whenever the CSS does, so the response is safe to
// treat as immutable: a new build always requests a new URL.
func (s *Server) handleStaticCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+cssVersion+`"`)
	if match := r.Header.Get("If-None-Match"); match == `"`+cssVersion+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = io.WriteString(w, appCSS)
}

// pageShell wraps a rendered body in the shared document chrome: the
// <head> (stylesheet link, viewport) and the sticky top nav. title and
// body may contain template actions — pageShell only concatenates, the
// result is handed to template.Parse by the caller. active is the nav
// key to highlight ("dashboard", "portfolio", or "" for none).
func pageShell(title, active, body string) string {
	navLink := func(href, label, key string) string {
		class := "navlink"
		if key == active {
			class += " navlink-active"
		}
		return fmt.Sprintf(`<a class=%q href=%q>%s</a>`, class, href, label)
	}
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + title + `</title>
<link rel="stylesheet" href="/static/app.css?v=` + cssVersion + `">
<script>
// Shared Chart.js theming, pulled from the CSS custom properties so the
// charts follow the light/dark palette. Defined here; the dashboard and
// portfolio pages call themeCharts() once Chart.js itself has loaded.
const chartPalette = ["#3a66db", "#167c45", "#d98a2b", "#c23b32", "#7b61c9", "#2a9d9d", "#b5487e"];
const chartFill = "rgba(58, 102, 219, 0.14)";
function cssVar(name, fallback) {
	const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
	return v || fallback;
}
function themeCharts() {
	if (typeof Chart === "undefined") return;
	Chart.defaults.font.family = cssVar("--font-sans", "sans-serif");
	Chart.defaults.font.size = 12;
	Chart.defaults.color = cssVar("--text-muted", "#666");
	Chart.defaults.borderColor = cssVar("--border", "#ddd");
}
function chartOptions(opts) {
	opts = opts || {};
	// Privacy mode (nav "Blur €" toggle): keep the chart shape but drop
	// the parts that spell out amounts — the value-axis tick labels and
	// the hover tooltips. Read at build time; the toggle reloads the
	// page, so charts are always constructed in the right mode.
	const priv = document.documentElement.classList.contains("privacy-mode");
	const o = {
		responsive: true,
		maintainAspectRatio: false,
		plugins: {
			legend: opts.legend === false ? {display: false} : {position: "bottom", labels: {boxWidth: 12, boxHeight: 12}},
			tooltip: {enabled: !priv},
		},
	};
	// Pie/doughnut charts have no axes — passing a scales config makes
	// Chart.js draw a stray grid behind them.
	if (opts.scales !== false) {
		o.scales = {
			x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true}},
			y: {grid: {color: cssVar("--border", "#e3e6eb")}, beginAtZero: true, ticks: {display: !priv}},
		};
	}
	return o;
}

// Privacy mode: the nav's toggle adds .privacy-mode to <html>. CSS
// blurs every .money figure while it's set; chartOptions() drops the
// value axis and tooltips from the charts. The choice is kept in
// localStorage and the class is applied here, before the body paints,
// so a reload never flashes the real numbers. Toggling reloads the
// page so the charts rebuild in the new mode from one code path.
(function () {
	var KEY = "taxman.privacy";
	var on = false;
	try { on = localStorage.getItem(KEY) === "1"; } catch (e) {}
	if (on) document.documentElement.classList.add("privacy-mode");
	document.addEventListener("DOMContentLoaded", function () {
		var btn = document.getElementById("privacy-toggle");
		if (!btn) return;
		btn.setAttribute("aria-pressed", on ? "true" : "false");
		btn.addEventListener("click", function () {
			on = !on;
			document.documentElement.classList.toggle("privacy-mode", on);
			try { localStorage.setItem(KEY, on ? "1" : "0"); } catch (e) {}
			location.reload();
		});
	});
})();
</script>
</head>
<body>
<header class="topnav">
<div class="topnav-inner">
<span class="brand">taxman</span>
<nav class="nav">` + navLink("/", "Dashboard", "dashboard") + navLink("/portfolio", "Portfolio", "portfolio") + `</nav>
<button type="button" id="privacy-toggle" class="navbtn" aria-pressed="false" title="Blur money figures for viewing in public">Blur &euro;</button>
</div>
</header>
<main class="container">
` + body + `
</main>
</body>
</html>
`
}

// holdingRow is one row of the dashboard's holdings table, including
// its P&L when one was computable.
type holdingRow struct {
	Instrument     string
	Description    string // human-readable name, when the source export provided one — see ledger.Transaction.Description
	Classification classify.Classification
	Unclassified   bool

	// Kind is "cgt" or "exit_tax" when this holding's classification
	// has a P&L computation; empty otherwise (unclassified holdings,
	// or a classification with no engine support).
	Kind string

	// RealisedGain is this holding's net realised gain/loss for the
	// scope in view (the selected year, or all years). TaxableGain and
	// TaxDue are populated for exit-tax holdings only — CGT's exemption
	// and loss relief are personal and reported once at the year level
	// (dashboardSummary.CGT*), never per holding.
	RealisedGain string
	TaxableGain  string
	TaxDue       string
	ComputeError string // set instead of the above when computation failed for this holding alone
}

// dashboardSummary is the dashboard's running-liability-by-tax-type
// section: SPEC.md §7's "merged P&L, running liability by tax type",
// computed live across every holding currently in the ledger. It is
// NOT persisted as audit.Records — see the note on handleDashboard —
// so it intentionally does not claim to link to an audit trail the
// way a `taxman report` figure would.
type dashboardSummary struct {
	CGTTaxDue     string
	CGTError      string // set when CGT could not be aggregated (a holding failed to FIFO-match)
	ExitTaxTaxDue string
	DIRTInterest  string
	DIRTTaxDue    string
	DIRTError     string

	// CGTDetail is populated only when a single tax year is selected —
	// the year-level breakdown from engine.CGTYearResult. CGTAllYears
	// marks the all-years view, where CGTTaxDue is the sum of each
	// year's own charge (each year has its own €1,270 exemption and
	// loss carry-forward) and no single-year breakdown applies.
	CGTDetail                 bool
	CGTAllYears               bool
	CGTNetChargeableGain      string
	CGTLossBroughtForwardUsed string
	CGTLossCarriedForward     string
	CGTAnnualExemptionUsed    string
	CGTTaxableGain            string

	TotalLiability string
}

// handleDashboard computes and renders the running P&L/liability
// dashboard.
//
// The P&L shown here is computed fresh on every request directly from
// the ledger via internal/engine — it is deliberately NOT written to
// internal/audit's Records table, unlike the figures `taxman report`
// is meant to produce (see cmd/taxman/report.go's own note that it
// doesn't yet persist audit records either). Persisting on every GET
// would append a duplicate Record set on every page load, since
// audit.Store.Insert has no dedup; wiring live dashboard figures to
// the audit trail properly is left for when report's own persistence
// gap is closed, so both go through the same path.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// ?year=YYYY scopes every figure on the page to that calendar
	// (Irish tax) year. A bare "/" with no year key defaults to the
	// current tax year — the one you're most likely filing for. The
	// selector's "All years" option submits ?year= (the key present
	// but empty); that, and only that, gives the whole-history view.
	// An unparseable or out-of-range year is a 400 rather than a
	// silent fallback.
	selectedYear := time.Now().Year()
	if q := r.URL.Query(); q.Has("year") {
		raw := strings.TrimSpace(q.Get("year"))
		if raw == "" {
			selectedYear = 0 // "All years"
		} else {
			y, err := strconv.Atoi(raw)
			if err != nil || y < 1900 || y > 3000 {
				http.Error(w, fmt.Sprintf("invalid year %q", raw), http.StatusBadRequest)
				return
			}
			selectedYear = y
		}
	}

	txs, err := s.ledgerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading transactions: %v", err), http.StatusInternalServerError)
		return
	}

	// The holdings table and every per-holding P&L below it are about
	// lot-matched positions. Interest credits aren't a holding — they
	// have their own "Interest credits logged" card and feed the DIRT
	// line in the summary — so they're kept out of this view entirely
	// (otherwise every savings account collapses into a perpetually
	// UNCLASSIFIED holding row that can never be classified
	// meaningfully).
	var holdingTxs []ledger.Transaction
	for _, tx := range txs {
		if tx.Type != ledger.TypeInterest {
			holdingTxs = append(holdingTxs, tx)
		}
	}

	byInstrument := map[string][]ledger.Transaction{}
	for _, tx := range holdingTxs {
		byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
	}
	instruments := distinctInstruments(holdingTxs)

	computeExitTax := func(holdingTxs []ledger.Transaction) (*engine.ExitTaxResult, error) {
		if selectedYear == 0 {
			return engine.ComputeExitTax(holdingTxs)
		}
		return engine.ComputeExitTaxForYear(holdingTxs, selectedYear)
	}

	summary := dashboardSummary{}
	exitTaxDue := decimal.Zero
	var disposals []engine.Disposal

	// CGT is aggregated at the tax-year level (one €1,270 exemption,
	// gains/losses netted across every holding, losses carried
	// forward), so each CGT_ASSET holding is only FIFO-matched here;
	// engine.AggregateCGTYear does the charge after the loop.
	cgtDisposalsByHolding := map[string][]engine.Disposal{}
	cgtRowIndex := map[string]int{}
	cgtMatchBlocked := false

	rows := make([]holdingRow, 0, len(instruments))
	for _, instrument := range instruments {
		classification, err := s.classifyStore.Classify(instrument)
		if err != nil {
			http.Error(w, fmt.Sprintf("classifying %s: %v", instrument, err), http.StatusInternalServerError)
			return
		}
		row := holdingRow{
			Instrument:     instrument,
			Description:    holdingDescription(byInstrument[instrument]),
			Classification: classification,
			Unclassified:   classification == classify.Unclassified,
		}

		// A compute error (e.g. an unresolvable FX rate) blocks only
		// this holding, the same way an UNCLASSIFIED holding blocks only
		// itself: it's shown, not hidden, and the rest of the dashboard
		// still renders.
		switch classification {
		case classify.CGTAsset:
			row.Kind = "cgt"
			holdingDisposals, err := engine.CGTDisposals(byInstrument[instrument])
			if err != nil {
				row.ComputeError = err.Error()
				cgtMatchBlocked = true
			} else {
				cgtDisposalsByHolding[instrument] = holdingDisposals
				cgtRowIndex[instrument] = len(rows)
			}

		case classify.ExitTaxFund:
			row.Kind = "exit_tax"
			result, err := computeExitTax(byInstrument[instrument])
			if err != nil {
				row.ComputeError = err.Error()
			} else {
				row.RealisedGain = result.TotalGain.String()
				row.TaxableGain = result.TaxableGain.String()
				row.TaxDue = result.TaxDue.String()
				exitTaxDue = exitTaxDue.Add(result.TaxDue)
				disposals = append(disposals, result.Disposals...)
			}
		}

		rows = append(rows, row)
	}

	cgtTaxDue := decimal.Zero
	switch {
	case cgtMatchBlocked:
		summary.CGTError = "one or more CGT holdings could not be matched (see the holdings table); the CGT charge is not shown"
	case len(cgtDisposalsByHolding) == 0:
		// no CGT holdings; nothing to do
	case selectedYear != 0:
		cy, err := engine.AggregateCGTYear(cgtDisposalsByHolding, selectedYear)
		if err != nil {
			summary.CGTError = err.Error()
		} else {
			cgtTaxDue = cy.TaxDue
			summary.CGTDetail = true
			summary.CGTNetChargeableGain = cy.NetChargeableGain.String()
			summary.CGTLossBroughtForwardUsed = cy.LossBroughtForwardUsed.String()
			summary.CGTLossCarriedForward = cy.LossCarriedForward.String()
			summary.CGTAnnualExemptionUsed = cy.AnnualExemptionUsed.String()
			summary.CGTTaxableGain = cy.TaxableGain.String()
			for _, h := range cy.Holdings {
				if idx, ok := cgtRowIndex[h.Instrument]; ok {
					rows[idx].RealisedGain = h.RealisedGain.String()
				}
				disposals = append(disposals, h.Disposals...)
			}
		}
	default:
		// All-years view: the CGT charge is the sum of every disposal
		// year's own charge — each year gets its own exemption and the
		// deterministic loss-carry-forward chain. Per-holding realised
		// gain is the whole-history figure.
		total, err := allYearsCGTTaxDue(cgtDisposalsByHolding)
		if err != nil {
			summary.CGTError = err.Error()
		} else {
			cgtTaxDue = total
			summary.CGTAllYears = true
			for instrument, holdingDisposals := range cgtDisposalsByHolding {
				if idx, ok := cgtRowIndex[instrument]; ok {
					rows[idx].RealisedGain = sumDisposalGains(holdingDisposals).String()
				}
				disposals = append(disposals, holdingDisposals...)
			}
		}
	}

	if summary.CGTError == "" {
		summary.CGTTaxDue = cgtTaxDue.String()
	}
	summary.ExitTaxTaxDue = exitTaxDue.String()

	// DIRT is computed once across every interest credit (optionally
	// year-scoped), not per holding — interest isn't tied to a
	// lot-matched holding the way CGT/exit-tax disposals are (see
	// internal/engine/dirt.go).
	var interestTxs []ledger.Transaction
	for _, tx := range txs {
		if tx.Type == ledger.TypeInterest {
			interestTxs = append(interestTxs, tx)
		}
	}
	dirtTaxDue := decimal.Zero
	if len(interestTxs) > 0 {
		var result *engine.DIRTResult
		var derr error
		if selectedYear == 0 {
			result, derr = engine.ComputeDIRT(interestTxs)
		} else {
			result, derr = engine.ComputeDIRTForYear(interestTxs, selectedYear)
		}
		if derr != nil {
			summary.DIRTError = derr.Error()
		} else {
			dirtTaxDue = result.TaxDue
			summary.DIRTInterest = result.TotalInterest.String()
			summary.DIRTTaxDue = result.TaxDue.String()
		}
	}

	summary.TotalLiability = cgtTaxDue.Add(exitTaxDue).Add(dirtTaxDue).String()

	// The individual interest credits behind the DIRT figure, each with
	// its row id so the dashboard can offer an edit/delete control for a
	// mistyped date or amount. Not year-scoped — a mistake is worth
	// seeing whichever year is in view.
	stored, err := s.ledgerStore.ManualInterestCredits()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading interest credits: %v", err), http.StatusInternalServerError)
		return
	}
	interestCredits := make([]interestCreditRow, 0, len(stored))
	for _, ic := range stored {
		interestCredits = append(interestCredits, interestCreditRow{
			ID:     ic.ID,
			Source: ic.Instrument,
			Date:   ic.Date.Format("2006-01-02"),
			Amount: ic.Price.String(),
		})
	}

	// Names already used, offered as suggestions on the entry form so a
	// second credit for the same account needn't be retyped exactly.
	interestSources, err := s.ledgerStore.ManualInterestSources()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading interest sources: %v", err), http.StatusInternalServerError)
		return
	}

	chartJS, err := chartDataJS(summary, disposals)
	if err != nil {
		http.Error(w, fmt.Sprintf("building chart data: %v", err), http.StatusInternalServerError)
		return
	}

	// The selector only lists years that have transactions; make sure
	// whatever year is actually in view is offered too (the current-year
	// default, or a hand-typed ?year=, may not have any transactions yet)
	// so it can render as the selected option.
	years := distinctYears(txs)
	if selectedYear != 0 && !slices.Contains(years, selectedYear) {
		years = append(years, selectedYear)
		sort.Sort(sort.Reverse(sort.IntSlice(years)))
	}

	if err := dashboardTemplate.Execute(w, dashboardData{
		Holdings:          rows,
		Platforms:         ingest.Platforms,
		Summary:           summary,
		InterestCredits:   interestCredits,
		InterestSources:   interestSources,
		InterestBatchRows: make([]struct{}, interestBatchRows),
		ChartDataJS:       chartJS,
		Years:             years,
		SelectedYear:      selectedYear,
	}); err != nil {
		http.Error(w, fmt.Sprintf("rendering dashboard: %v", err), http.StatusInternalServerError)
	}
}

// chartPnLPoint is one point on the P&L-over-time chart: the
// cumulative realized gain as of Date, across every disposal up to
// and including that date.
type chartPnLPoint struct {
	Date           string `json:"date"`
	CumulativeGain string `json:"cumulativeGain"`
}

// chartDashboardData is the JSON shape the dashboard's inline JS
// reads to draw both charts (SPEC.md §7). Tax-due fields default to
// "0" rather than an empty string when a dashboardSummary field
// wasn't set (e.g. no interest transactions yet), so the chart never
// has to handle a blank/NaN value.
type chartDashboardData struct {
	CGTTaxDue     string          `json:"cgtTaxDue"`
	ExitTaxTaxDue string          `json:"exitTaxTaxDue"`
	DIRTTaxDue    string          `json:"dirtTaxDue"`
	PnLSeries     []chartPnLPoint `json:"pnlSeries"`
}

// chartDataJS marshals the dashboard's chart data to JSON and wraps
// it as template.JS so html/template embeds it verbatim as a
// JavaScript object literal (see dashboardTemplate's `const
// dashboardData = {{.ChartDataJS}};`) rather than HTML-escaping it.
//
// disposals is the flat set of every CGT/exit-tax Disposal across all
// holdings that computed successfully this request (a holding left
// UNCLASSIFIED, or one whose computation errored, contributes
// nothing — same exclusion the rest of the dashboard already applies
// per-holding). It's sorted by date and turned into a cumulative-gain
// series; interest (DIRT) isn't a disposal and isn't included here.
func chartDataJS(summary dashboardSummary, disposals []engine.Disposal) (template.JS, error) {
	sort.Slice(disposals, func(i, j int) bool { return disposals[i].Date.Before(disposals[j].Date) })

	series := make([]chartPnLPoint, 0, len(disposals))
	cumulative := decimal.Zero
	for _, d := range disposals {
		cumulative = cumulative.Add(d.Gain)
		series = append(series, chartPnLPoint{
			Date:           d.Date.Format("2006-01-02"),
			CumulativeGain: cumulative.String(),
		})
	}

	data := chartDashboardData{
		CGTTaxDue:     orZero(summary.CGTTaxDue),
		ExitTaxTaxDue: orZero(summary.ExitTaxTaxDue),
		DIRTTaxDue:    orZero(summary.DIRTTaxDue),
		PnLSeries:     series,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshaling chart data: %w", err)
	}
	return template.JS(b), nil
}

// orZero returns "0" in place of an empty string, for numeric
// dashboardSummary fields that are left unset when there's nothing to
// compute (e.g. DIRTTaxDue with no interest transactions).
func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// sumDisposalGains totals a holding's disposal gains/losses (whole
// history), for the all-years dashboard view's per-holding realised
// gain column.
func sumDisposalGains(ds []engine.Disposal) decimal.Decimal {
	total := decimal.Zero
	for _, d := range ds {
		total = total.Add(d.Gain)
	}
	return total
}

// allYearsCGTTaxDue is the all-years CGT charge: the sum of each
// disposal year's own aggregate charge. Each year is assessed with its
// own €1,270 exemption and the same deterministic loss-carry-forward
// replay engine.AggregateCGTYear performs, so summing the years is
// internally consistent — it is not the same as taxing every year's
// gains in one lump with a single exemption.
func allYearsCGTTaxDue(disposalsByHolding map[string][]engine.Disposal) (decimal.Decimal, error) {
	years := map[int]bool{}
	for _, ds := range disposalsByHolding {
		for _, d := range ds {
			years[d.Date.Year()] = true
		}
	}
	ordered := make([]int, 0, len(years))
	for y := range years {
		ordered = append(ordered, y)
	}
	sort.Ints(ordered)

	total := decimal.Zero
	for _, y := range ordered {
		cy, err := engine.AggregateCGTYear(disposalsByHolding, y)
		if err != nil {
			return decimal.Zero, err
		}
		total = total.Add(cy.TaxDue)
	}
	return total, nil
}

func (s *Server) handleAuditDetail(w http.ResponseWriter, r *http.Request) {
	instrument := strings.TrimPrefix(r.URL.Path, "/audit/")
	if instrument == "" {
		http.NotFound(w, r)
		return
	}

	records, err := s.auditStore.ForInstrument(instrument)
	if err != nil {
		http.Error(w, fmt.Sprintf("loading audit records for %s: %v", instrument, err), http.StatusInternalServerError)
		return
	}

	if err := auditDetailTemplate.Execute(w, auditDetailData{Instrument: instrument, Records: records}); err != nil {
		http.Error(w, fmt.Sprintf("rendering audit detail: %v", err), http.StatusInternalServerError)
	}
}

// handleImport implements the web equivalent of `taxman import`:
// POST /import with a multipart "file" field and an optional
// "platform" field (empty/omitted means auto-detect from the file's
// header, same as the CLI). On success it redirects back to the
// dashboard; on any failure — no file, an unparseable file, a header
// no detector recognizes — it responds 400 with the error message
// rather than silently dropping or guessing at the upload.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, fmt.Sprintf("parsing upload: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("reading uploaded file: %v", err), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	platform := r.FormValue("platform")

	resolvedPlatform, txs, warnings, err := ingest.ParseReader(file, header.Filename, platform)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	inserted, skipped, err := ingest.Store(s.conn, txs)
	if err != nil {
		http.Error(w, fmt.Sprintf("storing transactions from %s (platform: %s): %v", header.Filename, resolvedPlatform, err), http.StatusInternalServerError)
		return
	}

	// A clean import (no warnings) goes straight back to the
	// dashboard. A file with skipped rows worth knowing about (e.g.
	// corporate actions — see internal/ingest/degiro/account.go) gets
	// a summary page instead of a silent redirect, so those don't go
	// unnoticed.
	if len(warnings) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := importResultTemplate.Execute(w, importResultData{
		Filename: header.Filename,
		Platform: resolvedPlatform,
		Inserted: inserted,
		Skipped:  skipped,
		Warnings: warnings,
	}); err != nil {
		http.Error(w, fmt.Sprintf("rendering import result: %v", err), http.StatusInternalServerError)
	}
}

// handleClassify implements the web equivalent of `taxman classify
// --set`: POST /classify with "instrument" and "classification"
// ("CGT_ASSET" or "EXIT_TAX_FUND" — Unclassified is deliberately not
// settable here either, same as the CLI: it's the absence of an
// override, not a choice). On success it redirects back to the
// dashboard; a missing instrument or an unrecognized classification
// value returns 400 without writing anything, rather than guessing.
func (s *Server) handleClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	instrument := r.FormValue("instrument")
	if instrument == "" {
		http.Error(w, "classify: instrument is required", http.StatusBadRequest)
		return
	}

	classificationArg := r.FormValue("classification")
	classification, ok := classify.ValidClassifications[classificationArg]
	if !ok {
		http.Error(w, fmt.Sprintf("classify: invalid classification %q (expected CGT_ASSET or EXIT_TAX_FUND)", classificationArg), http.StatusBadRequest)
		return
	}

	if err := s.classifyStore.Set(instrument, classification); err != nil {
		http.Error(w, fmt.Sprintf("classify: setting %s: %v", instrument, err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleInterest logs one or more hand-entered interest credits for a
// single account: POST /interest with one "source" (the user's own
// name for the account — free text, required) and parallel "date"
// (YYYY-MM-DD) / "amount" (positive decimal) fields, one pair per row
// of the dashboard's batch grid. Rows left entirely blank are skipped;
// a row with only one of the pair filled is a 400 naming the row, and
// nothing is written — the batch is all-or-nothing on validation.
// Savings accounts have no clean CSV export, so this is the only way
// interest enters the ledger; currency is fixed to EUR, which is what
// DIRT is computed in. On success it redirects back to the dashboard.
// A single filled row is just a batch of one, so this stays the
// single-entry path too.
func (s *Server) handleInterest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	source := r.FormValue("source")

	dates, amounts := r.Form["date"], r.Form["amount"]
	if len(dates) != len(amounts) {
		http.Error(w, "interest: mismatched date and amount fields", http.StatusBadRequest)
		return
	}
	var credits []ledger.Transaction
	for i := range dates {
		date, amount := strings.TrimSpace(dates[i]), strings.TrimSpace(amounts[i])
		if date == "" && amount == "" {
			continue // untouched grid row
		}
		tx, err := interest.NewCredit(source, date, amount)
		if err != nil {
			http.Error(w, fmt.Sprintf("interest: row %d: %v", i+1, err), http.StatusBadRequest)
			return
		}
		credits = append(credits, tx)
	}
	if len(credits) == 0 {
		http.Error(w, "interest: no interest payments to log", http.StatusBadRequest)
		return
	}

	for _, tx := range credits {
		if _, err := s.ledgerStore.Insert(tx); err != nil {
			http.Error(w, fmt.Sprintf("interest: inserting: %v", err), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleInterestUpdate corrects a hand-entered interest credit: POST
// /interest/update with an `id` (the row shown on the dashboard) and
// the same `source`, `date` and `amount` fields handleInterest takes.
// All three are editable — a mistyped account name is as correctable
// as a mistyped amount — and the fingerprint is recomputed over the
// corrected values. An id that isn't a manual interest credit is a
// 404; a correction that would duplicate another credit is a 409; a
// validation failure is a 400.
func (s *Server) handleInterestUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, fmt.Sprintf("interest: invalid id %q", r.FormValue("id")), http.StatusBadRequest)
		return
	}

	tx, err := interest.NewCredit(r.FormValue("source"), r.FormValue("date"), r.FormValue("amount"))
	if err != nil {
		http.Error(w, fmt.Sprintf("interest: %v", err), http.StatusBadRequest)
		return
	}

	switch err := s.ledgerStore.UpdateManualInterestCredit(id, tx); {
	case errors.Is(err, ledger.ErrNotManualInterest):
		http.Error(w, "interest: no such interest credit", http.StatusNotFound)
		return
	case errors.Is(err, ledger.ErrDuplicateInterestCredit):
		http.Error(w, "interest: an interest credit with that date and amount already exists", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, fmt.Sprintf("interest: updating: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleInterestDelete removes a hand-entered interest credit: POST
// /interest/delete with an `id`. An id that isn't a manual interest
// credit is a 404. No `source` is needed — the delete keys on id
// alone, scoped to hand-entered credits.
func (s *Server) handleInterestDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, fmt.Sprintf("interest: invalid id %q", r.FormValue("id")), http.StatusBadRequest)
		return
	}

	switch err := s.ledgerStore.DeleteManualInterestCredit(id); {
	case errors.Is(err, ledger.ErrNotManualInterest):
		http.Error(w, "interest: no such interest credit", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, fmt.Sprintf("interest: deleting: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleRSUVest logs a hand-entered RSU vesting event: POST /rsu with
// "symbol", "date" (YYYY-MM-DD), "quantity", "fmv" (vest-date fair
// market value per share) and an optional "currency" (defaults to USD)
// form field. It's for when the E*TRADE export on hand doesn't present
// vests as importable rows. The vest is the CGT acquisition event, so
// it's stored as an rsu_vest transaction — a FIFO lot whose cost basis
// is the FMV, restated to euro at the vest-date ECB rate by the engine
// (never here). On success it redirects to the dashboard, same as
// handleInterest; a missing/invalid field is a 400 that writes
// nothing.
func (s *Server) handleRSUVest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	currency := strings.TrimSpace(r.FormValue("currency"))
	if currency == "" {
		currency = "USD" // the common case; E*TRADE RSUs are US-listed
	}
	tx, err := etrade.NewRSUVest(
		r.FormValue("symbol"),
		r.FormValue("date"),
		r.FormValue("quantity"),
		r.FormValue("fmv"),
		currency,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("rsu: %v", err), http.StatusBadRequest)
		return
	}

	if _, err := s.ledgerStore.Insert(tx); err != nil {
		http.Error(w, fmt.Sprintf("rsu: inserting: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// portfolioPosition is one currently-held instrument on the portfolio
// page: what remains after FIFO disposals (engine.OpenPositions), the
// market symbol mapped to it, and — when a live quote and a EUR rate
// were both available — its current market value. See SPEC.md §9.
//
// Quantity, CostBasis and MarketValue are decimal.Decimal.String()
// output; CostBasis and MarketValue are euro (engine.OpenPositions
// restates non-EUR lots at their transaction-date ECB rate; the quote
// is converted at today's rate). Note carries the reason instead of a
// value when a holding could not be priced or matched — the row is
// still shown, never hidden.
type portfolioPosition struct {
	Instrument  string
	Description string
	Symbol      string
	Quantity    string
	CostBasis   string
	MarketValue string
	Stale       bool
	Note        string
}

// label is this position's chart label: its human name when the export
// gave one, else its mapped symbol, else the raw instrument id.
func (p portfolioPosition) label() string {
	switch {
	case p.Description != "":
		return p.Description
	case p.Symbol != "":
		return p.Symbol
	default:
		return p.Instrument
	}
}

// portfolioData is what portfolioTemplate renders.
//
//   - Priced:   held, ticker-mapped, quote + EUR rate available — these
//     are the pie and the cost-vs-value bars, and TotalValue sums them.
//   - Unmapped: held but with no ticker yet — each gets a mapping form.
//   - Unpriced: held and mapped, but no quote or no EUR rate — shown
//     with the reason and a remap form (a wrong symbol is the usual
//     cause), excluded from every total.
//   - Errored:  engine.OpenPositions itself failed for the holding.
type portfolioData struct {
	Priced   []portfolioPosition
	Unmapped []portfolioPosition
	Unpriced []portfolioPosition
	Errored  []portfolioPosition

	TotalValue string
	TotalCost  string
	PricesAsOf string // "15:04", the oldest quote time across Priced; "" when none
	AnyStale   bool

	ChartDataJS template.JS
}

type portfolioPieSlice struct {
	Label string `json:"label"`
	Value string `json:"valueEUR"`
}

type portfolioBar struct {
	Label       string `json:"label"`
	CostBasis   string `json:"costBasisEUR"`
	MarketValue string `json:"marketValueEUR"`
}

type portfolioChartData struct {
	Pie  []portfolioPieSlice `json:"pie"`
	Bars []portfolioBar      `json:"bars"`
}

// handlePortfolio renders the portfolio page (SPEC.md §9): every
// currently-held position (quantity still held > 0, per
// engine.OpenPositions), valued at a live market price converted to
// euro, as an allocation pie and cost-vs-value bars. It is always "as
// of now" — a ?year= query param (meaningful on the dashboard) is
// accepted and ignored here.
//
// Nothing about a price fetch can 500 this page or blank it: a missing
// quote or missing FX rate moves that one holding to a visible
// "couldn't value" list and drops it from the totals; a stale quote
// (served from cache because the providers were unreachable) renders
// with a marker and a "prices as of" stamp.
func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	txs, err := s.ledgerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading transactions: %v", err), http.StatusInternalServerError)
		return
	}

	// Only buy/sell/rsu_vest events form a market position. Interest
	// credits (and dividends, unprocessed in v1) have no lots and no
	// price — SPEC §9 keeps them off this page entirely.
	var tradeTxs []ledger.Transaction
	for _, tx := range txs {
		switch tx.Type {
		case ledger.TypeBuy, ledger.TypeSell, ledger.TypeRSUVest:
			tradeTxs = append(tradeTxs, tx)
		}
	}

	byInstrument := map[string][]ledger.Transaction{}
	for _, tx := range tradeTxs {
		byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
	}

	mappings, err := s.tickerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading ticker mappings: %v", err), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	var data portfolioData
	totalValue, totalCost := decimal.Zero, decimal.Zero
	var oldestAsOf time.Time

	for _, instrument := range distinctInstruments(tradeTxs) {
		holdingTxs := byInstrument[instrument]
		row := portfolioPosition{
			Instrument:  instrument,
			Description: holdingDescription(holdingTxs),
		}

		pos, err := engine.OpenPositions(holdingTxs)
		if err != nil {
			row.Note = err.Error()
			data.Errored = append(data.Errored, row)
			continue
		}
		if !pos.Quantity.IsPositive() {
			continue // nothing held — off the portfolio page
		}
		row.Quantity = pos.Quantity.String()
		row.CostBasis = pos.CostBasis.String()

		symbol, mapped := mappings[instrument]
		if !mapped {
			data.Unmapped = append(data.Unmapped, row)
			continue
		}
		row.Symbol = symbol

		quote, err := s.quoter.Quote(r.Context(), symbol)
		if err != nil {
			row.Note = fmt.Sprintf("no price available for %s: %v", symbol, err)
			data.Unpriced = append(data.Unpriced, row)
			continue
		}

		priceEUR, ok := s.toEUR(quote, now)
		if !ok {
			if strings.TrimSpace(quote.Currency) == "" {
				row.Note = fmt.Sprintf("%s returned a price but no currency — cannot convert to EUR", symbol)
			} else {
				row.Note = fmt.Sprintf("no EUR reference rate for %s (refresh internal/fx/eurofxref-hist.csv)", quote.Currency)
			}
			data.Unpriced = append(data.Unpriced, row)
			continue
		}

		marketValue := priceEUR.Mul(pos.Quantity)
		row.MarketValue = marketValue.String()
		row.Stale = quote.Stale

		totalValue = totalValue.Add(marketValue)
		totalCost = totalCost.Add(pos.CostBasis)
		if quote.Stale {
			data.AnyStale = true
		}
		if !quote.AsOf.IsZero() && (oldestAsOf.IsZero() || quote.AsOf.Before(oldestAsOf)) {
			oldestAsOf = quote.AsOf
		}
		data.Priced = append(data.Priced, row)
	}

	// Pie and bars, largest market value first.
	sort.SliceStable(data.Priced, func(i, j int) bool {
		return decimalOrZero(data.Priced[i].MarketValue).GreaterThan(decimalOrZero(data.Priced[j].MarketValue))
	})

	data.TotalValue = totalValue.String()
	data.TotalCost = totalCost.String()
	if !oldestAsOf.IsZero() {
		data.PricesAsOf = oldestAsOf.Format("15:04")
	}

	chartJS, err := portfolioChartDataJS(data.Priced)
	if err != nil {
		http.Error(w, fmt.Sprintf("building portfolio chart data: %v", err), http.StatusInternalServerError)
		return
	}
	data.ChartDataJS = chartJS

	if err := portfolioTemplate.Execute(w, data); err != nil {
		http.Error(w, fmt.Sprintf("rendering portfolio: %v", err), http.StatusInternalServerError)
	}
}

// toEUR converts a quote to a euro price-per-share. ok is false when
// the quote carries no currency (a fallback source that reports only a
// price) or when fx has no reference rate for that currency — the
// caller then lists the holding as unpriced rather than guessing.
func (s *Server) toEUR(quote prices.Quote, on time.Time) (decimal.Decimal, bool) {
	cur := strings.ToUpper(strings.TrimSpace(quote.Currency))
	switch cur {
	case "EUR":
		return quote.Price, true
	case "":
		return decimal.Zero, false
	}
	rate, err := s.rate(cur, on)
	if err != nil {
		return decimal.Zero, false
	}
	return quote.Price.Mul(rate), true
}

// decimalOrZero parses a decimal string, treating "" or an unparseable
// value as zero — used only to order the pie/bars by market value.
func decimalOrZero(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

// portfolioChartDataJS marshals the priced positions into the JSON the
// portfolio page's inline JS reads to draw the pie and the bars, in
// the order given (already sorted by market value, descending).
func portfolioChartDataJS(priced []portfolioPosition) (template.JS, error) {
	chart := portfolioChartData{
		Pie:  make([]portfolioPieSlice, 0, len(priced)),
		Bars: make([]portfolioBar, 0, len(priced)),
	}
	for _, p := range priced {
		chart.Pie = append(chart.Pie, portfolioPieSlice{Label: p.label(), Value: p.MarketValue})
		chart.Bars = append(chart.Bars, portfolioBar{
			Label:       p.label(),
			CostBasis:   p.CostBasis,
			MarketValue: p.MarketValue,
		})
	}
	b, err := json.Marshal(chart)
	if err != nil {
		return "", fmt.Errorf("marshaling portfolio chart data: %w", err)
	}
	return template.JS(b), nil
}

// handlePortfolioTicker records an instrument→market-symbol mapping:
// POST /portfolio/ticker with "instrument" and "symbol" form fields.
// It mirrors handleClassify — validate, store, redirect — and never
// guesses a symbol from the instrument name or ISIN (SPEC.md §6). The
// store's Upsert overwrites, so the same endpoint serves both the
// first mapping (Unmapped rows) and correcting a wrong one (the remap
// forms on the Priced and Unpriced rows). On success it redirects back
// to /portfolio; an empty instrument or symbol is a 400 that writes
// nothing.
func (s *Server) handlePortfolioTicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("parsing form: %v", err), http.StatusBadRequest)
		return
	}

	instrument := strings.TrimSpace(r.FormValue("instrument"))
	if instrument == "" {
		http.Error(w, "portfolio: instrument is required", http.StatusBadRequest)
		return
	}
	symbol := strings.TrimSpace(r.FormValue("symbol"))
	if symbol == "" {
		http.Error(w, "portfolio: symbol is required", http.StatusBadRequest)
		return
	}

	if err := s.tickerStore.Upsert(instrument, symbol); err != nil {
		http.Error(w, fmt.Sprintf("portfolio: mapping %s: %v", instrument, err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/portfolio", http.StatusSeeOther)
}

type importResultData struct {
	Filename string
	Platform string
	Inserted int
	Skipped  int
	Warnings []string
}

var importResultTemplate = template.Must(template.New("import-result").Parse(pageShell("taxman: import result", "dashboard", `<div class="page-head"><h1>Imported {{.Filename}} ({{.Platform}})</h1></div>
<section class="card">
<p>{{.Inserted}} new transaction(s), {{.Skipped}} already present.</p>
<h3>Warnings</h3>
<ul class="audit-list">
{{range .Warnings}}<li>{{.}}</li>
{{end}}
</ul>
<p><a href="/">&larr; back to dashboard</a></p>
</section>`)))

// holdingDescription returns the first non-empty Description across
// txs (typically the source export's own product name / ticker for
// this instrument), or "" if none of them had one. Different
// transactions for the same instrument could in principle carry
// differently-worded descriptions across platforms; the first one
// found is good enough for a display label, not itself a value
// anything computes from.
func holdingDescription(txs []ledger.Transaction) string {
	for _, tx := range txs {
		if tx.Description != "" {
			return tx.Description
		}
	}
	return ""
}

// formatEUR renders a decimal string (as produced by
// decimal.Decimal.String) as a euro amount: a "€" prefix, two
// decimal places, and thousands separators — "1234.5" becomes
// "€1,234.50". Every figure the engine produces is already in euro
// (non-EUR transactions are restated at their transaction-date ECB
// rate before matching — see internal/engine); this is display only.
// An empty string stays empty, so a holding with no P&L computed
// shows a blank cell rather than "€0.00"; a value that doesn't parse
// is passed through unchanged.
//
// The result is wrapped in <span class="money"> so the nav's "Blur €"
// privacy toggle (see pageShell) can obscure every figure on the page
// at once. It returns template.HTML, so it must only ever wrap
// content that is safe to emit unescaped — digits, separators, and a
// currency sign from groupThousands, or an HTML-escaped fallback.
func formatEUR(s string) template.HTML {
	if s == "" {
		return ""
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return money("€" + groupThousands(d.StringFixed(2)))
}

// money wraps an already-safe figure string in the .money span the
// privacy toggle blurs. inner must contain no HTML metacharacters —
// callers pass formatted numbers only.
func money(inner string) template.HTML {
	return template.HTML(`<span class="money">` + inner + `</span>`)
}

// formatGainPct renders value's gain or loss against cost as a signed
// percentage — "+12.3%", "-4.5%" — for the portfolio P&L column. Both
// are decimal.Decimal.String output in euro. A zero or unparseable
// cost basis yields "" (no meaningful percentage), shown as a blank
// cell. Like formatEUR the result is a blur-able .money span.
func formatGainPct(cost, value string) template.HTML {
	c, err := decimal.NewFromString(cost)
	if err != nil || c.IsZero() {
		return ""
	}
	v, err := decimal.NewFromString(value)
	if err != nil {
		return ""
	}
	pct := v.Sub(c).Div(c).Mul(decimal.NewFromInt(100)).StringFixed(1)
	if !strings.HasPrefix(pct, "-") {
		pct = "+" + pct
	}
	cls := "money gain-pos"
	if strings.HasPrefix(pct, "-") {
		cls = "money gain-neg"
	}
	return template.HTML(`<span class="` + cls + `">` + pct + `%</span>`)
}

// groupThousands inserts thousands separators into the integer part
// of a plain decimal string in [-]int[.frac] form, as produced by
// decimal.Decimal.StringFixed: "-1234.50" becomes "-1,234.50".
func groupThousands(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i:]
	}
	n := len(intPart)
	if n <= 3 {
		return sign + intPart + fracPart
	}

	var b strings.Builder
	lead := n % 3
	if lead > 0 {
		b.WriteString(intPart[:lead])
		b.WriteByte(',')
	}
	for i := lead; i < n; i += 3 {
		b.WriteString(intPart[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return sign + b.String() + fracPart
}

// distinctInstruments returns the sorted, de-duplicated set of
// instruments across txs.
func distinctInstruments(txs []ledger.Transaction) []string {
	seen := map[string]bool{}
	for _, tx := range txs {
		seen[tx.Instrument] = true
	}
	out := make([]string, 0, len(seen))
	for instrument := range seen {
		out = append(out, instrument)
	}
	sort.Strings(out)
	return out
}

// distinctYears returns the calendar years that have at least one
// transaction, most recent first — the option list for the
// dashboard's tax-year selector.
func distinctYears(txs []ledger.Transaction) []int {
	seen := map[int]bool{}
	for _, tx := range txs {
		seen[tx.Date.Year()] = true
	}
	out := make([]int, 0, len(seen))
	for year := range seen {
		out = append(out, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// interestCreditRow is one hand-entered interest credit shown on the
// dashboard, with its row id so the edit and delete forms can target
// it. Date is YYYY-MM-DD; Amount is the credited euro amount as a
// plain decimal string; Source is the user's own name for the account
// it was paid on. All three pre-fill the edit form, and all three are
// correctable there.
type interestCreditRow struct {
	ID     int64
	Date   string
	Amount string
	Source string
}

type dashboardData struct {
	Holdings          []holdingRow
	Platforms         []string
	Summary           dashboardSummary
	InterestCredits   []interestCreditRow
	InterestSources   []string   // account names already used, suggested on the entry form
	InterestBatchRows []struct{} // blank grid rows to render; only the count matters
	ChartDataJS       template.JS

	// Years is the tax-year selector's options (descending);
	// SelectedYear is the active filter, 0 meaning "all years".
	Years        []int
	SelectedYear int
}

type auditDetailData struct {
	Instrument string
	Records    []audit.Record
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"eur": formatEUR,
}).Parse(pageShell("taxman", "dashboard", `<div class="page-head"><h1>Dashboard</h1></div>

<section class="card">
<h2>Import transactions</h2>
<form class="form-row" action="/import" method="post" enctype="multipart/form-data">
<label>File: <input type="file" name="file" required></label>
<label>Platform:
<select name="platform">
<option value="">auto-detect</option>
{{range .Platforms}}<option value="{{.}}">{{.}}</option>{{end}}
</select>
</label>
<button type="submit">Import</button>
</form>
</section>

<section class="card">
<h2>Tax year</h2>
<form class="form-row" action="/" method="get">
<label>Show figures for:
<select name="year" onchange="this.form.submit()">
<option value="" {{if eq .SelectedYear 0}}selected{{end}}>All years</option>
{{range .Years}}<option value="{{.}}" {{if eq $.SelectedYear .}}selected{{end}}>{{.}}</option>{{end}}
</select>
</label>
<noscript><button type="submit">Show</button></noscript>
</form>
</section>

<section class="card">
<h2>Running liability by tax type{{if .SelectedYear}} — {{.SelectedYear}}{{else}} — all years{{end}}</h2>
<table class="data">
<thead><tr><th>Tax type</th><th>Interest</th><th>Tax due</th></tr></thead>
<tbody>
<tr><td>CGT</td><td></td><td>{{if .Summary.CGTError}}<strong>error: {{.Summary.CGTError}}</strong>{{else}}{{eur .Summary.CGTTaxDue}}{{end}}</td></tr>
<tr><td>Exit tax</td><td></td><td>{{eur .Summary.ExitTaxTaxDue}}</td></tr>
<tr><td>DIRT</td><td>{{eur .Summary.DIRTInterest}}</td><td>{{if .Summary.DIRTError}}<strong>error: {{.Summary.DIRTError}}</strong>{{else}}{{eur .Summary.DIRTTaxDue}}{{end}}</td></tr>
<tr><th>Total</th><th></th><th>{{eur .Summary.TotalLiability}}</th></tr>
</tbody>
</table>
{{if .Summary.CGTAllYears}}<p><em>CGT shown is the sum of each tax year's charge — every year has its own &euro;1,270 exemption and loss carry-forward. Pick a tax year above for the year-level breakdown.</em></p>{{end}}
{{if .Summary.CGTDetail}}
<h3>CGT &mdash; {{.SelectedYear}} (one &euro;1,270 exemption; gains and losses netted across all holdings)</h3>
<table class="data">
<tbody>
<tr><td>Net chargeable gain (gains less losses, all holdings)</td><td>{{eur .Summary.CGTNetChargeableGain}}</td></tr>
<tr><td>Loss brought forward, used this year</td><td>{{eur .Summary.CGTLossBroughtForwardUsed}}</td></tr>
<tr><td>Annual exemption used</td><td>{{eur .Summary.CGTAnnualExemptionUsed}}</td></tr>
<tr><td>Taxable gain</td><td>{{eur .Summary.CGTTaxableGain}}</td></tr>
<tr><th>CGT due</th><th>{{eur .Summary.CGTTaxDue}}</th></tr>
<tr><td>Loss carried forward to later years</td><td>{{eur .Summary.CGTLossCarriedForward}}</td></tr>
</tbody>
</table>
{{end}}
</section>

<section class="card">
<h2>Log interest payments</h2>
<form action="/interest" method="post">
<div class="form-row">
<label>Account: <input type="text" name="source" list="interest-sources" placeholder="Rainy day savings" required></label>
<datalist id="interest-sources">
{{range .InterestSources}}<option value="{{.}}"></option>{{end}}
</datalist>
</div>
<table class="data" id="interest-batch">
<thead><tr><th>Date</th><th>Amount (EUR)</th></tr></thead>
<tbody>
{{range .InterestBatchRows}}<tr><td><input type="date" name="date"></td><td><input type="text" name="amount" inputmode="decimal" placeholder="0.00"></td></tr>
{{end}}</tbody>
</table>
<div class="form-row">
<button type="button" onclick="addInterestRow()">Add row</button>
<button type="submit">Log interest</button>
</div>
</form>
<script>
function addInterestRow() {
	var body = document.querySelector('#interest-batch tbody');
	var row = body.rows[0].cloneNode(true);
	row.querySelectorAll('input').forEach(function (input) { input.value = ''; });
	body.appendChild(row);
}
</script>
</section>

<section class="card">
<h2>Log RSU vest</h2>
<form class="form-row" action="/rsu" method="post">
<label>Symbol: <input type="text" name="symbol" placeholder="ACME" required></label>
<label>Vest date: <input type="date" name="date" required></label>
<label>Quantity: <input type="text" name="quantity" inputmode="decimal" required></label>
<label>FMV per share: <input type="text" name="fmv" inputmode="decimal" placeholder="0.00" required></label>
<label>Currency: <input type="text" name="currency" value="USD" required></label>
<button type="submit">Log vest</button>
</form>
<p class="muted">The vest is the CGT acquisition event: cost basis is this fair market value, restated to euro at the vest-date rate. Set the holding to <code>CGT_ASSET</code> once it appears below.</p>
</section>

<section class="card">
<details class="disclosure">
<summary>Interest credits logged{{if .InterestCredits}} ({{len .InterestCredits}}){{end}}</summary>
<div class="disclosure-body">
{{if .InterestCredits}}
<table class="data">
<thead><tr><th>Account</th><th>Date</th><th>Amount (EUR)</th><th></th><th></th></tr></thead>
<tbody>
{{range .InterestCredits}}
<tr>
<td><input form="ic-{{.ID}}" type="text" name="source" list="interest-sources" value="{{.Source}}" required></td>
<td><input form="ic-{{.ID}}" type="date" name="date" value="{{.Date}}" required></td>
<td><input form="ic-{{.ID}}" class="money" type="text" name="amount" value="{{.Amount}}" required></td>
<td>
<form id="ic-{{.ID}}" action="/interest/update" method="post">
<input type="hidden" name="id" value="{{.ID}}">
<button type="submit">Save</button>
</form>
</td>
<td>
<form action="/interest/delete" method="post" onsubmit="return confirm('Delete this interest credit?')">
<input type="hidden" name="id" value="{{.ID}}">
<button type="submit">Delete</button>
</form>
</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p>No interest credits logged yet.</p>
{{end}}
</div>
</details>
</section>

<section class="card">
<h2>Liability by tax type</h2>
<div class="chart-card"><canvas id="liability-chart"></canvas></div>
</section>

<section class="card">
<h2>P&amp;L over time{{if .SelectedYear}} — {{.SelectedYear}}{{end}}</h2>
<div class="chart-card"><canvas id="pnl-chart"></canvas></div>
</section>

<section class="card">
<h2>Holdings{{if .SelectedYear}} — {{.SelectedYear}}{{end}}</h2>
<table class="data">
<thead><tr><th>Instrument</th><th>Classification</th><th>Set classification</th><th>Realised gain</th><th>Taxable gain</th><th>Tax due</th><th></th></tr></thead>
<tbody>
{{range .Holdings}}
<tr>
<td>{{if .Description}}{{.Description}} ({{.Instrument}}){{else}}{{.Instrument}}{{end}}</td>
<td>
{{if .Unclassified}}<strong>UNCLASSIFIED</strong>{{else}}{{.Classification}}{{end}}
</td>
<td>
<form action="/classify" method="post">
<input type="hidden" name="instrument" value="{{.Instrument}}">
<select name="classification">
<option value="CGT_ASSET" {{if eq (print .Classification) "CGT_ASSET"}}selected{{end}}>CGT_ASSET</option>
<option value="EXIT_TAX_FUND" {{if eq (print .Classification) "EXIT_TAX_FUND"}}selected{{end}}>EXIT_TAX_FUND</option>
</select>
<button type="submit">Set</button>
</form>
</td>
{{if .ComputeError}}
<td colspan="3"><strong>error: {{.ComputeError}}</strong></td>
{{else}}
<td>{{eur .RealisedGain}}</td>
<td>{{if eq .Kind "cgt"}}&mdash;{{else}}{{eur .TaxableGain}}{{end}}</td>
<td>{{if eq .Kind "cgt"}}&mdash;{{else}}{{eur .TaxDue}}{{end}}</td>
{{end}}
<td><a href="/audit/{{.Instrument}}">audit trail</a></td>
</tr>
{{end}}
</tbody>
</table>
</section>

<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.4/dist/chart.umd.min.js"></script>
<script>
const dashboardData = {{.ChartDataJS}};
themeCharts();
new Chart(document.getElementById("liability-chart"), {
	type: "bar",
	data: {
		labels: ["CGT", "Exit tax", "DIRT"],
		datasets: [{
			label: "Tax due (EUR)",
			data: [
				parseFloat(dashboardData.cgtTaxDue),
				parseFloat(dashboardData.exitTaxTaxDue),
				parseFloat(dashboardData.dirtTaxDue),
			],
			backgroundColor: chartPalette,
			borderRadius: 4,
			maxBarThickness: 72,
		}],
	},
	options: chartOptions({legend: false}),
});
new Chart(document.getElementById("pnl-chart"), {
	type: "line",
	data: {
		labels: dashboardData.pnlSeries.map(p => p.date),
		datasets: [{
			label: "Cumulative realized gain (EUR)",
			data: dashboardData.pnlSeries.map(p => parseFloat(p.cumulativeGain)),
			borderColor: chartPalette[0],
			backgroundColor: chartFill,
			fill: true,
			tension: 0.25,
			pointRadius: 2,
			borderWidth: 2,
		}],
	},
	options: chartOptions({legend: false}),
});
</script>
`)))

var portfolioTemplate = template.Must(template.New("portfolio").Funcs(template.FuncMap{
	"eur":     formatEUR,
	"gainpct": formatGainPct,
}).Parse(pageShell("taxman: portfolio", "portfolio", `<div class="page-head"><h1>Portfolio</h1></div>

<section class="card">
<p>
Total market value: <strong>{{eur .TotalValue}}</strong>
(cost basis {{eur .TotalCost}}){{with gainpct .TotalCost .TotalValue}} &middot; {{.}} vs cost{{end}}
{{if .PricesAsOf}}<br>Prices as of {{.PricesAsOf}}.{{end}}
{{if .AnyStale}}<br><strong>Some prices are stale</strong> — served from cache because a price source was unreachable.{{end}}
{{if .Unpriced}}<br><strong>Some holdings could not be valued</strong> and are excluded from the totals and charts — see below.{{end}}
</p>
</section>

{{if .Priced}}
<section class="card">
<h2>Allocation by market value</h2>
<div class="chart-card"><canvas id="portfolio-pie"></canvas></div>
</section>

<section class="card">
<h2>Cost basis vs market value</h2>
<div class="chart-card"><canvas id="portfolio-bars"></canvas></div>
</section>

<section class="card">
<h2>Holdings</h2>
<table class="data">
<thead><tr><th>Instrument</th><th>Symbol</th><th>Quantity</th><th>Cost basis</th><th>Market value</th><th>P&amp;L</th><th>Remap</th></tr></thead>
<tbody>
{{range .Priced}}
<tr>
<td>{{if .Description}}{{.Description}} ({{.Instrument}}){{else}}{{.Instrument}}{{end}}</td>
<td><span class="sensitive">{{.Symbol}}</span></td>
<td><span class="sensitive">{{.Quantity}}</span></td>
<td>{{eur .CostBasis}}</td>
<td>{{eur .MarketValue}}{{if .Stale}} <strong>(stale)</strong>{{end}}</td>
<td>{{gainpct .CostBasis .MarketValue}}</td>
<td>
<form action="/portfolio/ticker" method="post">
<input type="hidden" name="instrument" value="{{.Instrument}}">
<input class="sensitive" type="text" name="symbol" value="{{.Symbol}}" required>
<button type="submit">Remap</button>
</form>
</td>
</tr>
{{end}}
</tbody>
</table>
</section>
{{else}}
<section class="card">
<p>No held position has both a ticker mapping and a live price yet.</p>
</section>
{{end}}

<section class="card">
<h2>Held positions without a ticker</h2>
{{if .Unmapped}}
<p>Add a market symbol so these can be valued (e.g. <code>AAPL</code>, <code>VWRL.L</code>).</p>
<table class="data">
<thead><tr><th>Instrument</th><th>Quantity</th><th>Cost basis</th><th>Ticker</th></tr></thead>
<tbody>
{{range .Unmapped}}
<tr>
<td>{{if .Description}}{{.Description}} ({{.Instrument}}){{else}}{{.Instrument}}{{end}}</td>
<td><span class="sensitive">{{.Quantity}}</span></td>
<td>{{eur .CostBasis}}</td>
<td>
<form action="/portfolio/ticker" method="post">
<input type="hidden" name="instrument" value="{{.Instrument}}">
<input type="text" name="symbol" placeholder="AAPL" required>
<button type="submit">Map</button>
</form>
</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p>Every held position has a ticker mapped.</p>
{{end}}
</section>

{{if .Unpriced}}
<section class="card">
<h2>Could not value these positions</h2>
<p>A wrong symbol is the usual cause — correct it below (e.g. <code>GOOGL</code>, not <code>GOOGL:NASDAQ</code>).</p>
<table class="data">
<thead><tr><th>Instrument</th><th>Quantity</th><th>Cost basis</th><th>Reason</th><th>Remap</th></tr></thead>
<tbody>
{{range .Unpriced}}
<tr>
<td>{{if .Description}}{{.Description}} ({{.Instrument}}){{else}}{{.Instrument}}{{end}}</td>
<td><span class="sensitive">{{.Quantity}}</span></td>
<td>{{eur .CostBasis}}</td>
<td><strong>{{.Note}}</strong></td>
<td>
<form action="/portfolio/ticker" method="post">
<input type="hidden" name="instrument" value="{{.Instrument}}">
<input class="sensitive" type="text" name="symbol" value="{{.Symbol}}" required>
<button type="submit">Remap</button>
</form>
</td>
</tr>
{{end}}
</tbody>
</table>
</section>
{{end}}

{{if .Errored}}
<section class="card">
<h2>Could not read these positions</h2>
<ul class="audit-list">
{{range .Errored}}
<li>{{if .Description}}{{.Description}} ({{.Instrument}}){{else}}{{.Instrument}}{{end}}: <strong>{{.Note}}</strong></li>
{{end}}
</ul>
</section>
{{end}}

{{if .Priced}}
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.4/dist/chart.umd.min.js"></script>
<script>
const portfolioData = {{.ChartDataJS}};
themeCharts();
new Chart(document.getElementById("portfolio-pie"), {
	type: "pie",
	data: {
		labels: portfolioData.pie.map(s => s.label),
		datasets: [{
			label: "Market value (EUR)",
			data: portfolioData.pie.map(s => parseFloat(s.valueEUR)),
			backgroundColor: chartPalette,
			borderColor: cssVar("--surface", "#fff"),
			borderWidth: 2,
		}],
	},
	options: chartOptions({legend: true, scales: false}),
});
new Chart(document.getElementById("portfolio-bars"), {
	type: "bar",
	data: {
		labels: portfolioData.bars.map(b => b.label),
		datasets: [
			{ label: "Cost basis (EUR)", data: portfolioData.bars.map(b => parseFloat(b.costBasisEUR)), backgroundColor: chartPalette[5], borderRadius: 4 },
			{ label: "Market value (EUR)", data: portfolioData.bars.map(b => parseFloat(b.marketValueEUR)), backgroundColor: chartPalette[0], borderRadius: 4 },
		],
	},
	options: chartOptions({legend: true}),
});
</script>
{{end}}
`)))

var auditDetailTemplate = template.Must(template.New("audit").Parse(pageShell("taxman audit: {{.Instrument}}", "", `<div class="page-head"><h1>Audit trail: {{.Instrument}}</h1></div>
<section class="card">
<ul class="audit-list">
{{range .Records}}
<li>
{{.Kind}} on {{.DisposalDate.Format "2006-01-02"}}: liability {{.LiabilityAmount}} at rate {{.RuleRate}} (effective {{.RuleEffectiveFrom.Format "2006-01-02"}})
<br>source transactions: {{range .SourceTransactions}}{{.}} {{end}}
<br>lots: {{range .LotIDs}}{{.}} {{end}}
</li>
{{end}}
</ul>
<p><a href="/">&larr; back to dashboard</a></p>
</section>`)))
