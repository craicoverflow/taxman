package web

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/craicoverflow/taxman/internal/prices"
)

func TestSquarify_AreasProportionalAndTileTheContainer(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
	}{
		{"single", []float64{5}},
		{"two", []float64{3, 1}},
		{"six (Bruls example)", []float64{6, 6, 4, 3, 2, 2, 1}},
		{"many equal", []float64{1, 1, 1, 1, 1, 1, 1, 1, 1}},
		{"one dominant", []float64{1000, 1, 1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const w, h = 200.0, 100.0
			rects := squarify(tt.values, w, h)
			if len(rects) != len(tt.values) {
				t.Fatalf("got %d rects for %d values", len(rects), len(tt.values))
			}
			total := 0.0
			for _, v := range tt.values {
				total += v
			}
			sum := 0.0
			for i, r := range rects {
				const eps = 1e-6
				if r.X < -eps || r.Y < -eps || r.X+r.W > w+eps || r.Y+r.H > h+eps {
					t.Errorf("rect %d %+v outside container", i, r)
				}
				area := r.W * r.H
				sum += area
				if want := tt.values[i] / total * w * h; math.Abs(area-want) > 1e-6 {
					t.Errorf("rect %d area = %v, want %v", i, area, want)
				}
				for j := i + 1; j < len(rects); j++ {
					o := rects[j]
					ox := math.Min(r.X+r.W, o.X+o.W) - math.Max(r.X, o.X)
					oy := math.Min(r.Y+r.H, o.Y+o.H) - math.Max(r.Y, o.Y)
					if ox > 1e-6 && oy > 1e-6 {
						t.Errorf("rects %d and %d overlap", i, j)
					}
				}
			}
			if math.Abs(sum-w*h) > 1e-6 {
				t.Errorf("total area = %v, want %v", sum, w*h)
			}
		})
	}
}

func TestSquarify_Empty(t *testing.T) {
	if got := squarify(nil, 200, 100); len(got) != 0 {
		t.Errorf("squarify(nil) = %v, want empty", got)
	}
}

func TestBuildTreemap(t *testing.T) {
	priced := []portfolioPosition{
		{Instrument: "A", Symbol: "AAA", CostBasis: "100", MarketValue: "130", Description: "Alpha"},
		{Instrument: "B", Symbol: "BBB", CostBasis: "100", MarketValue: "50", Stale: true},
		{Instrument: "C", Symbol: "CCC", CostBasis: "0", MarketValue: "40"},
		{Instrument: "D", Symbol: "DDD", CostBasis: "10", MarketValue: ""},  // skipped: no value
		{Instrument: "E", Symbol: "EEE", CostBasis: "10", MarketValue: "0"}, // skipped: no area
	}
	tiles := buildTreemap(priced)
	if len(tiles) != 3 {
		t.Fatalf("got %d tiles, want 3 (unpriceable skipped): %+v", len(tiles), tiles)
	}
	if tiles[0].Pct != "+30.0%" || tiles[0].Class != "tm-pos" {
		t.Errorf("tile 0 = %q/%s, want +30.0%%/tm-pos", tiles[0].Pct, tiles[0].Class)
	}
	if !strings.Contains(string(tiles[0].Style), "--mix:55.0%") {
		t.Errorf("tile 0 should be fully saturated at +30%%: %s", tiles[0].Style)
	}
	if tiles[1].Pct != "-50.0%" || tiles[1].Class != "tm-neg" || !strings.Contains(tiles[1].Title, "stale") {
		t.Errorf("tile 1 = %+v, want -50.0%%/tm-neg and a stale title", tiles[1])
	}
	if tiles[2].Pct != "" || tiles[2].Class != "tm-flat" {
		t.Errorf("tile 2 (zero cost) = %q/%s, want no pct and tm-flat", tiles[2].Pct, tiles[2].Class)
	}
	if buildTreemap(nil) != nil {
		t.Error("buildTreemap(nil) should be nil")
	}
}

func TestBuildTreemap_TinyTileHasNoLabelButKeepsTitle(t *testing.T) {
	priced := []portfolioPosition{
		{Instrument: "BIG", Symbol: "BIG", CostBasis: "1", MarketValue: "100000"},
		{Instrument: "TINY", Symbol: "TNY", CostBasis: "1", MarketValue: "1"},
	}
	tiles := buildTreemap(priced)
	if tiles[0].Label != "BIG" {
		t.Errorf("big tile label = %q, want BIG", tiles[0].Label)
	}
	if tiles[1].Label != "" || tiles[1].Title != "TNY" {
		t.Errorf("tiny tile = label %q title %q, want no label, title TNY", tiles[1].Label, tiles[1].Title)
	}
}

func TestHandler_Portfolio_RendersHoldingsMap(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn)
	mustUpsertTicker(t, conn, "US0378331005", "AAPL") // cost €600
	mustUpsertTicker(t, conn, "IE00B3RBWM25", "VWRL") // cost €800

	fq := fakeQuoter{quotes: map[string]prices.Quote{
		"AAPL": {Symbol: "AAPL", Price: mustDecimal(t, "200"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")}, // €1200, +100%
		"VWRL": {Symbol: "VWRL", Price: mustDecimal(t, "40"), Currency: "EUR", AsOf: mustDate(t, "2024-06-01")},  // €400, -50%
	}}
	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fq, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	for _, want := range []string{
		`<h2>Holdings map</h2>`,
		`class="tm-tile tm-pos"`,
		`class="tm-tile tm-neg"`,
		`<span class="tm-label">AAPL</span><span class="money tm-pct">&#43;100.0%</span>`, // html/template escapes "+"
		`<span class="tm-label">VWRL</span><span class="money tm-pct">-50.0%</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got: %s", want, body)
		}
	}
	// The style attribute must survive html/template's CSS filter.
	if strings.Contains(body, "ZgotmplZ") {
		t.Errorf("template sanitiser rejected a tile style; got: %s", body)
	}
}

func TestHandler_Portfolio_NoHoldingsMapWhenNothingPriced(t *testing.T) {
	conn := openMigratedTestDB(t)
	portfolioSeedTwoHeld(t, conn) // held but unmapped → nothing priced
	rec := httptest.NewRecorder()
	portfolioServer(t, conn, fakeQuoter{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/portfolio", nil))
	if strings.Contains(rec.Body.String(), "Holdings map") {
		t.Errorf("holdings map should not render with no priced positions")
	}
}
