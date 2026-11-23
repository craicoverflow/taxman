package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/prices"
)

// The portfolio trend chart (GET /portfolio/trend.json) plots how the
// market prices of everything ever held have risen and fallen since the
// first purchase — each holding on its own, and the portfolio overall.
//
// It is a market view, not tax and not a return on money invested:
//
//   - A holding's line is the change in its euro market price since the
//     close on the day it was first bought. Buying more or selling some
//     does not move it; the price does.
//   - The overall line is a time-weighted return: each day's move is the
//     quantity-weighted price change of what was held at the previous
//     close, chained. Deposits and sales therefore don't register as
//     gains or losses — only the market does. It is what "how has my
//     portfolio's market done" means once money has been added over time.
//   - The value view plots euro market value against the cost basis of
//     the lots still open on each day (engine.OpenPositions, the same
//     FIFO view the holdings table uses).
//
// Historical closes come from internal/prices (only the ticker symbol
// leaves the process) and are converted to euro at each day's ECB
// reference rate. Nothing here is persisted to audit or feeds the tax
// engine.

// eurClose is one daily close already converted to euro.
type eurClose struct {
	Date  time.Time
	Price decimal.Decimal
}

// trendHolding is one instrument's input to buildTrend.
type trendHolding struct {
	Label  string
	Symbol string
	Txs    []ledger.Transaction // buy / sell / rsu_vest only
	Closes []eurClose           // ascending by date
}

// trendLine is one plotted series, aligned to trendSeries.Dates. A nil
// entry is a gap: before the holding was bought, after it was sold, or
// a day with no price yet.
type trendLine struct {
	Label  string         `json:"label"`
	Symbol string         `json:"symbol,omitempty"`
	Pct    []*json.Number `json:"pct"`
	Value  []*json.Number `json:"value"`
	Cost   []*json.Number `json:"cost"`
}

// trendSeries is the JSON the portfolio page's trend chart draws.
type trendSeries struct {
	Dates    []string    `json:"dates"`
	Overall  trendLine   `json:"overall"`
	Holdings []trendLine `json:"holdings"`
	Notes    []string    `json:"notes"`
	Stale    bool        `json:"stale"`
}

var hundred = decimal.NewFromInt(100)

func num(d decimal.Decimal, places int32) *json.Number {
	n := json.Number(d.StringFixed(places))
	return &n
}

// qtyStep is a holding's quantity and open cost basis from Date on.
type qtyStep struct {
	Date time.Time
	Qty  decimal.Decimal
	Cost decimal.Decimal
}

func truncDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// holdingSteps walks txs (any order) and returns, for each distinct
// transaction date, the quantity held and the euro cost basis of open
// lots at the end of that day. Cost basis comes from
// engine.OpenPositions over the transactions up to that day, so it is
// the same FIFO figure the holdings table shows.
func holdingSteps(txs []ledger.Transaction) ([]qtyStep, error) {
	sorted := make([]ledger.Transaction, len(txs))
	copy(sorted, txs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })

	var steps []qtyStep
	for i := 0; i < len(sorted); {
		d := truncDay(sorted[i].Date)
		j := i
		for j < len(sorted) && truncDay(sorted[j].Date).Equal(d) {
			j++
		}
		pos, err := engine.OpenPositions(sorted[:j])
		if err != nil {
			return nil, err
		}
		steps = append(steps, qtyStep{Date: d, Qty: pos.Quantity, Cost: pos.CostBasis})
		i = j
	}
	return steps, nil
}

// buildTrend aligns every holding's euro closes onto one date axis that
// starts at the first purchase and ends at today, and derives the
// per-holding and overall series described at the top of this file.
func buildTrend(holdings []trendHolding, today time.Time) trendSeries {
	today = truncDay(today)
	out := trendSeries{Dates: []string{}, Holdings: []trendLine{}, Notes: []string{}}

	type state struct {
		h         trendHolding
		steps     []qtyStep
		firstBuy  time.Time
		end       time.Time // last day held when fully sold; zero while still held
		priceIdx  int       // next close not yet consumed
		price     decimal.Decimal
		havePrice bool
		stepIdx   int
		base      decimal.Decimal
		haveBase  bool
		prevQty   decimal.Decimal
		prevPrice decimal.Decimal
		prevOK    bool
		line      trendLine
	}

	var states []*state
	var start time.Time
	for _, h := range holdings {
		steps, err := holdingSteps(h.Txs)
		if err != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %v — left out of the chart", h.Label, err))
			continue
		}
		if len(steps) == 0 || len(h.Closes) == 0 {
			continue
		}
		st := &state{h: h, steps: steps, firstBuy: steps[0].Date}
		if last := steps[len(steps)-1]; !last.Qty.IsPositive() {
			st.end = last.Date
		}
		// A purchase on a weekend or market holiday is priced from the
		// next session without comment; a longer gap means the
		// provider's history doesn't reach back that far.
		if h.Closes[0].Date.After(st.firstBuy.AddDate(0, 0, 7)) {
			out.Notes = append(out.Notes, fmt.Sprintf(
				"%s: price history starts %s, after its first purchase on %s — its line starts there",
				h.Label, h.Closes[0].Date.Format("2006-01-02"), st.firstBuy.Format("2006-01-02")))
		}
		if start.IsZero() || st.firstBuy.Before(start) {
			start = st.firstBuy
		}
		states = append(states, st)
	}
	if len(states) == 0 {
		return out
	}

	// The axis is every trading day any holding has a close for, from
	// the first purchase to today.
	seen := map[time.Time]bool{}
	var axis []time.Time
	for _, st := range states {
		for _, c := range st.h.Closes {
			if c.Date.Before(start) || c.Date.After(today) || seen[c.Date] {
				continue
			}
			seen[c.Date] = true
			axis = append(axis, c.Date)
		}
	}
	sort.Slice(axis, func(i, j int) bool { return axis[i].Before(axis[j]) })

	for _, st := range states {
		st.line = trendLine{
			Label:  st.h.Label,
			Symbol: st.h.Symbol,
			Pct:    make([]*json.Number, len(axis)),
			Value:  make([]*json.Number, len(axis)),
			Cost:   make([]*json.Number, len(axis)),
		}
	}
	overall := trendLine{
		Label: "Overall",
		Pct:   make([]*json.Number, len(axis)),
		Value: make([]*json.Number, len(axis)),
		Cost:  make([]*json.Number, len(axis)),
	}
	index := decimal.NewFromInt(1)

	for k, d := range axis {
		out.Dates = append(out.Dates, d.Format("2006-01-02"))
		num1, den := decimal.Zero, decimal.Zero
		totalValue, totalCost := decimal.Zero, decimal.Zero
		anyActive := false

		for _, st := range states {
			// Latest close on or before d (carried over exchange holidays).
			for st.priceIdx < len(st.h.Closes) && !st.h.Closes[st.priceIdx].Date.After(d) {
				st.price = st.h.Closes[st.priceIdx].Price
				st.havePrice = true
				st.priceIdx++
			}
			// Quantity and open cost at the end of d.
			for st.stepIdx < len(st.steps) && !st.steps[st.stepIdx].Date.After(d) {
				st.stepIdx++
			}
			qty, cost := decimal.Zero, decimal.Zero
			if st.stepIdx > 0 {
				qty, cost = st.steps[st.stepIdx-1].Qty, st.steps[st.stepIdx-1].Cost
			}

			// Today's move on what was held at the previous close.
			if st.havePrice && st.prevOK && st.prevQty.IsPositive() {
				num1 = num1.Add(st.prevQty.Mul(st.price))
				den = den.Add(st.prevQty.Mul(st.prevPrice))
			}

			active := !d.Before(st.firstBuy) && (st.end.IsZero() || !d.After(st.end))
			if active && st.havePrice {
				if !st.haveBase {
					st.base, st.haveBase = st.price, true
				}
				st.line.Pct[k] = num(st.price.Div(st.base).Sub(decimal.NewFromInt(1)).Mul(hundred), 2)
				value := qty.Mul(st.price)
				st.line.Value[k] = num(value, 2)
				st.line.Cost[k] = num(cost, 2)
				totalValue = totalValue.Add(value)
				totalCost = totalCost.Add(cost)
				anyActive = true
			}

			st.prevQty, st.prevPrice, st.prevOK = qty, st.price, st.havePrice
		}

		if den.IsPositive() {
			index = index.Mul(num1.Div(den))
		}
		if anyActive {
			overall.Pct[k] = num(index.Sub(decimal.NewFromInt(1)).Mul(hundred), 2)
			overall.Value[k] = num(totalValue, 2)
			overall.Cost[k] = num(totalCost, 2)
		}
	}

	out.Overall = overall
	for _, st := range states {
		out.Holdings = append(out.Holdings, st.line)
	}
	return out
}

// historyFetchConcurrency bounds parallel history requests so a large
// portfolio doesn't trip the provider's rate limit in one burst.
const historyFetchConcurrency = 4

// handlePortfolioTrend serves the trend chart's data as JSON. It is
// fetched by the portfolio page after it renders, so slow or failing
// history requests never hold up (or break) the page itself. A holding
// with no ticker, or whose history can't be fetched or converted to
// euro, is left out with a note saying why — never guessed.
func (s *Server) handlePortfolioTrend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	txs, err := s.ledgerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading transactions: %v", err), http.StatusInternalServerError)
		return
	}
	mappings, err := s.tickerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading ticker mappings: %v", err), http.StatusInternalServerError)
		return
	}

	byInstrument := map[string][]ledger.Transaction{}
	var tradeTxs []ledger.Transaction
	for _, tx := range txs {
		switch tx.Type {
		case ledger.TypeBuy, ledger.TypeSell, ledger.TypeRSUVest:
			tradeTxs = append(tradeTxs, tx)
			byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
		}
	}

	notes := []string{}
	type job struct {
		holding trendHolding
		hist    prices.History
		err     error
	}
	var jobs []*job
	for _, instrument := range distinctInstruments(tradeTxs) {
		htxs := byInstrument[instrument]
		label := holdingDescription(htxs)
		if label == "" {
			label = instrument
		}
		symbol, ok := mappings[instrument]
		if !ok {
			notes = append(notes, fmt.Sprintf("%s has no ticker mapped — not in the chart", label))
			continue
		}
		jobs = append(jobs, &job{holding: trendHolding{Label: label, Symbol: symbol, Txs: htxs}})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	sem := make(chan struct{}, historyFetchConcurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			j.hist, j.err = s.historian.History(ctx, j.holding.Symbol)
		}(j)
	}
	wg.Wait()

	var holdings []trendHolding
	stale := false
	for _, j := range jobs {
		if j.err != nil {
			notes = append(notes, fmt.Sprintf("%s (%s): no price history — %v", j.holding.Label, j.holding.Symbol, j.err))
			continue
		}
		closes, missing, firstMissing := s.closesToEUR(j.hist)
		if len(closes) == 0 {
			notes = append(notes, fmt.Sprintf("%s (%s): no EUR reference rate for %s — not in the chart", j.holding.Label, j.holding.Symbol, j.hist.Currency))
			continue
		}
		if missing > 0 {
			notes = append(notes, fmt.Sprintf("%s (%s): %d day(s) skipped with no %s→EUR rate (first %s)", j.holding.Label, j.holding.Symbol, missing, j.hist.Currency, firstMissing))
		}
		stale = stale || j.hist.Stale
		j.holding.Closes = closes
		holdings = append(holdings, j.holding)
	}

	series := buildTrend(holdings, time.Now())
	series.Notes = append(notes, series.Notes...)
	series.Stale = stale

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(series); err != nil {
		http.Error(w, fmt.Sprintf("encoding trend: %v", err), http.StatusInternalServerError)
	}
}

// closesToEUR converts a native-currency series to euro at each day's
// ECB reference rate. A day with no rate is dropped (and counted) rather
// than converted at some other day's rate; firstMissing describes the
// first such day and why, for the note shown under the chart.
func (s *Server) closesToEUR(h prices.History) (out []eurClose, missing int, firstMissing string) {
	out = make([]eurClose, 0, len(h.Closes))
	for _, c := range h.Closes {
		if h.Currency == "EUR" {
			out = append(out, eurClose{Date: c.Date, Price: c.Price})
			continue
		}
		rate, err := s.rate(h.Currency, c.Date)
		if err != nil {
			if missing == 0 {
				firstMissing = fmt.Sprintf("%s: %v", c.Date.Format("2006-01-02"), err)
			}
			missing++
			continue
		}
		out = append(out, eurClose{Date: c.Date, Price: c.Price.Mul(rate)})
	}
	return out, missing, firstMissing
}
