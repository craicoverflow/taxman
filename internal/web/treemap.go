package web

import (
	"fmt"
	"html/template"
	"math"

	"github.com/shopspring/decimal"
)

// The holdings map on /portfolio (SPEC.md §19): a squarified treemap
// whose tile area is euro market value and whose colour is unrealised
// P&L against cost. Everything here is presentation — float64 geometry
// for pixel layout, never a tax or money figure. The euro values and
// percentages shown come from decimals handlePortfolio already computed.

const (
	// treemapW and treemapH are the layout coordinate space. Their
	// ratio must match the .treemap CSS aspect-ratio so tiles come out
	// near-square on screen.
	treemapW = 200.0
	treemapH = 100.0

	// treemapSatPct is the |P&L %| at which a tile's colour saturates;
	// treemapMaxMix caps how much of the gain/loss colour is mixed in so
	// label text stays readable.
	treemapSatPct = 30.0
	treemapMaxMix = 55.0

	// Tiles narrower or shorter than this (percent of the container)
	// carry no label — it would not fit. The title attribute still
	// names them.
	treemapMinLabelW = 9.0
	treemapMinLabelH = 14.0
)

// treemapRect is a tile's placement in the layout coordinate space.
type treemapRect struct{ X, Y, W, H float64 }

// squarify lays values out as a treemap inside the rectangle
// (0,0,w,h) using the squarified algorithm (Bruls, Huizing, van Wijk).
// values must be positive and sorted descending; the returned rects are
// in the same order. Each rect's area is proportional to its value and
// together they tile the whole rectangle.
func squarify(values []float64, w, h float64) []treemapRect {
	out := make([]treemapRect, 0, len(values))
	if len(values) == 0 || w <= 0 || h <= 0 {
		return out
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	areas := make([]float64, len(values))
	for i, v := range values {
		areas[i] = v / total * w * h
	}

	x, y := 0.0, 0.0
	i := 0
	for i < len(areas) {
		side := math.Min(w, h)
		// Grow the row while doing so does not worsen its worst
		// aspect ratio.
		j := i + 1
		for j < len(areas) && worstRatio(areas[i:j+1], side) <= worstRatio(areas[i:j], side) {
			j++
		}
		row := areas[i:j]
		rowSum := 0.0
		for _, a := range row {
			rowSum += a
		}
		thick := rowSum / side
		if w >= h {
			// Wide remainder: the row is a vertical strip on the left.
			cy := y
			for _, a := range row {
				ih := a / thick
				out = append(out, treemapRect{X: x, Y: cy, W: thick, H: ih})
				cy += ih
			}
			x += thick
			w -= thick
		} else {
			// Tall remainder: the row is a horizontal strip on top.
			cx := x
			for _, a := range row {
				iw := a / thick
				out = append(out, treemapRect{X: cx, Y: y, W: iw, H: thick})
				cx += iw
			}
			y += thick
			h -= thick
		}
		i = j
	}
	return out
}

// worstRatio is the largest aspect ratio (>= 1) among row's tiles when
// the row is laid along a side of the given length.
func worstRatio(row []float64, side float64) float64 {
	sum, lo, hi := 0.0, math.Inf(1), 0.0
	for _, a := range row {
		sum += a
		lo = math.Min(lo, a)
		hi = math.Max(hi, a)
	}
	s2 := side * side
	return math.Max(s2*hi/(sum*sum), sum*sum/(s2*lo))
}

// treemapTile is one rendered tile. Left/Top/Width/Height are percent
// of the container.
type treemapTile struct {
	Label     string // short text shown on the tile; "" when too small
	Title     string // always set: the full name for hover/assistive tech
	Pct       string // signed P&L %, e.g. "+12.4%"; "" when cost basis is unusable
	Left, Top float64
	Width     float64
	Height    float64
	Style     template.CSS
	Class     string // "tm-pos", "tm-neg" or "tm-flat"
}

// buildTreemap turns the priced holdings (already sorted by market
// value, descending) into positioned, coloured tiles. Positions whose
// market value is unparseable or not positive have no area and are
// skipped; they still appear in the holdings table.
func buildTreemap(priced []portfolioPosition) []treemapTile {
	type item struct {
		pos portfolioPosition
		val float64
	}
	items := make([]item, 0, len(priced))
	for _, p := range priced {
		v, err := decimal.NewFromString(p.MarketValue)
		if err != nil || !v.IsPositive() {
			continue
		}
		items = append(items, item{pos: p, val: v.InexactFloat64()})
	}
	if len(items) == 0 {
		return nil
	}
	values := make([]float64, len(items))
	for i, it := range items {
		values[i] = it.val
	}
	rects := squarify(values, treemapW, treemapH)

	tiles := make([]treemapTile, len(items))
	for i, it := range items {
		r := rects[i]
		t := treemapTile{
			Left:   r.X / treemapW * 100,
			Top:    r.Y / treemapH * 100,
			Width:  r.W / treemapW * 100,
			Height: r.H / treemapH * 100,
			Class:  "tm-flat",
		}

		name := it.pos.label()
		short := it.pos.Symbol
		if short == "" {
			short = name
		}
		t.Title = name
		if it.pos.Stale {
			t.Title += " (stale price)"
		}

		pct, ok := gainPercent(it.pos.CostBasis, it.pos.MarketValue)
		mix := 0.0
		if ok {
			t.Pct = signedPct(pct)
			switch {
			case pct.IsPositive():
				t.Class = "tm-pos"
			case pct.IsNegative():
				t.Class = "tm-neg"
			}
			f, _ := pct.Abs().Float64()
			mix = math.Min(f/treemapSatPct, 1) * treemapMaxMix
		}
		t.Style = template.CSS(fmt.Sprintf(
			"left:%.3f%%;top:%.3f%%;width:%.3f%%;height:%.3f%%;--mix:%.1f%%",
			t.Left, t.Top, t.Width, t.Height, mix))

		if t.Width >= treemapMinLabelW && t.Height >= treemapMinLabelH {
			t.Label = short
		}
		tiles[i] = t
	}
	return tiles
}

// gainPercent is (value - cost) / cost as a percentage. ok is false for
// an unparseable or zero cost basis (no meaningful percentage), the
// same rule formatGainPct applies.
func gainPercent(cost, value string) (decimal.Decimal, bool) {
	c, err := decimal.NewFromString(cost)
	if err != nil || c.IsZero() {
		return decimal.Zero, false
	}
	v, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, false
	}
	return v.Sub(c).Div(c).Mul(decimal.NewFromInt(100)), true
}

// signedPct renders a percentage with one decimal and an explicit sign.
func signedPct(p decimal.Decimal) string {
	s := p.StringFixed(1)
	if s[0] != '-' {
		s = "+" + s
	}
	return s + "%"
}
