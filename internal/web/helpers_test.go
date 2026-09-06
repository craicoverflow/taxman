package web

import (
	"html/template"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func decimalTen(t *testing.T) decimal.Decimal {
	t.Helper()
	return decimal.NewFromInt(10)
}

func mustDecimal(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("parsing decimal %q: %v", s, err)
	}
	return d
}

func TestFormatEUR(t *testing.T) {
	// formatEUR wraps the formatted figure in the .money span the
	// privacy toggle blurs; an empty input stays empty, an unparseable
	// one is HTML-escaped and passed through unwrapped.
	tests := []struct {
		in   string
		want template.HTML
	}{
		{"", ""},
		{"0", `<span class="money">€0.00</span>`},
		{"150", `<span class="money">€150.00</span>`},
		{"2847.9", `<span class="money">€2,847.90</span>`},
		{"1234567.5", `<span class="money">€1,234,567.50</span>`},
		{"-1234.5", `<span class="money">€-1,234.50</span>`},
		{"1000", `<span class="money">€1,000.00</span>`},
		{"999", `<span class="money">€999.00</span>`},
		{"not-a-number", "not-a-number"},
		{"<script>", "&lt;script&gt;"},
	}
	for _, tt := range tests {
		if got := formatEUR(tt.in); got != tt.want {
			t.Errorf("formatEUR(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatGainPct(t *testing.T) {
	tests := []struct {
		name        string
		cost, value string
		want        template.HTML
	}{
		{"gain", "100", "112.5", `<span class="money gain-pos">+12.5%</span>`},
		{"loss", "100", "95.5", `<span class="money gain-neg">-4.5%</span>`},
		{"flat", "100", "100", `<span class="money gain-pos">+0.0%</span>`},
		{"zero cost", "0", "100", ""},
		{"unparseable cost", "n/a", "100", ""},
		{"unparseable value", "100", "n/a", ""},
	}
	for _, tt := range tests {
		if got := formatGainPct(tt.cost, tt.value); got != tt.want {
			t.Errorf("formatGainPct(%q, %q) = %q, want %q", tt.cost, tt.value, got, tt.want)
		}
	}
}
