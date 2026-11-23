package web

// portfolioTrendJS draws the portfolio page's market-trend chart from
// GET /portfolio/trend.json (see trend.go). It is spliced into the
// portfolio template, so it must not contain "{{" and, being a Go raw
// string, no backticks.
//
// The JSON carries pre-computed decimals; the browser only re-bases a
// percentage series to the start of a shorter range for display —
// (1+p)/(1+p0)-1 — and never derives a money figure. In privacy mode
// chartOptions() already drops the value axis and tooltips, and the
// summary figures are wrapped in .money so they blur like the rest.
const portfolioTrendJS = `
(function () {
	var canvas = document.getElementById("portfolio-trend");
	if (!canvas || typeof Chart === "undefined") return;
	var statusEl = document.getElementById("trend-status");
	var notesEl = document.getElementById("trend-notes");
	var summaryEl = document.getElementById("trend-summary");
	var priv = document.documentElement.classList.contains("privacy-mode");
	var state = {mode: "pct", range: "all", show: "all"};
	var data = null, chart = null;
	var hidden = {}; // legend toggles survive re-renders, keyed by label

	document.querySelectorAll("#trend-card .seg").forEach(function (group) {
		var key = group.getAttribute("data-group");
		group.querySelectorAll("button").forEach(function (btn) {
			btn.addEventListener("click", function () {
				state[key] = btn.getAttribute("data-value");
				group.querySelectorAll("button").forEach(function (b) {
					b.setAttribute("aria-pressed", b === btn ? "true" : "false");
				});
				if (data) render();
			});
		});
	});

	fetch("/portfolio/trend.json", {headers: {"Accept": "application/json"}})
		.then(function (r) {
			if (!r.ok) throw new Error("HTTP " + r.status);
			return r.json();
		})
		.then(function (d) {
			data = d;
			renderNotes();
			if (!d.dates.length) {
				statusEl.textContent = "No price history to plot yet — map a ticker to a holding below.";
				return;
			}
			render();
		})
		.catch(function (e) {
			statusEl.textContent = "Could not load price history: " + e.message;
		});

	function renderNotes() {
		notesEl.innerHTML = "";
		(data.notes || []).forEach(function (n) {
			var li = document.createElement("li");
			li.textContent = n;
			notesEl.appendChild(li);
		});
	}

	function startIndex(dates) {
		if (state.range === "all") return 0;
		var last = new Date(dates[dates.length - 1] + "T00:00:00Z");
		var from = new Date(last.getTime());
		switch (state.range) {
		case "1m": from.setUTCMonth(from.getUTCMonth() - 1); break;
		case "3m": from.setUTCMonth(from.getUTCMonth() - 3); break;
		case "6m": from.setUTCMonth(from.getUTCMonth() - 6); break;
		case "ytd": from = new Date(Date.UTC(last.getUTCFullYear(), 0, 1)); break;
		case "1y": from.setUTCFullYear(from.getUTCFullYear() - 1); break;
		case "3y": from.setUTCFullYear(from.getUTCFullYear() - 3); break;
		}
		var iso = from.toISOString().slice(0, 10);
		for (var i = 0; i < dates.length; i++) if (dates[i] >= iso) return i;
		return 0;
	}

	function pctSeries(arr, i0) {
		var base = null;
		return arr.slice(i0).map(function (v) {
			if (v === null) return null;
			if (state.range === "all") return Number(v);
			var f = 1 + Number(v) / 100;
			if (base === null) base = f;
			return (f / base - 1) * 100;
		});
	}

	function numSeries(arr, i0) {
		return arr.slice(i0).map(function (v) { return v === null ? null : Number(v); });
	}

	function lastNonNull(a) {
		for (var i = a.length - 1; i >= 0; i--) if (a[i] !== null) return a[i];
		return null;
	}

	function fmtPct(v) {
		return (v >= 0 ? "+" : "") + v.toFixed(1) + "%";
	}

	function fmtEUR(v) {
		return "€" + v.toLocaleString(undefined, {minimumFractionDigits: 0, maximumFractionDigits: 0});
	}

	function pctSpan(v) {
		var s = document.createElement("span");
		s.className = "money " + (v >= 0 ? "gain-pos" : "gain-neg");
		s.textContent = fmtPct(v);
		return s;
	}

	function renderSummary(i0) {
		summaryEl.innerHTML = "";
		var overall = lastNonNull(pctSeries(data.overall.pct, i0));
		var ranked = data.holdings.map(function (h) {
			return {label: h.label, v: lastNonNull(pctSeries(h.pct, i0))};
		}).filter(function (h) { return h.v !== null; });
		ranked.sort(function (a, b) { return b.v - a.v; });
		function item(text, v) {
			var d = document.createElement("div");
			d.appendChild(document.createTextNode(text + " "));
			d.appendChild(pctSpan(v));
			summaryEl.appendChild(d);
		}
		if (overall !== null) item("Overall", overall);
		if (ranked.length > 1) {
			item("Best: " + ranked[0].label, ranked[0].v);
			item("Worst: " + ranked[ranked.length - 1].label, ranked[ranked.length - 1].v);
		}
	}

	function render() {
		var i0 = startIndex(data.dates);
		var labels = data.dates.slice(i0);
		var text = cssVar("--text", "#1b1e24");
		var datasets = [];
		var line = function (label, series, color, extra) {
			var ds = {
				label: label, data: series, borderColor: color, backgroundColor: color,
				borderWidth: 1.5, pointRadius: 0, pointHitRadius: 6, tension: 0, spanGaps: false,
				hidden: !!hidden[label],
			};
			for (var k in extra) ds[k] = extra[k];
			return ds;
		};

		if (state.mode === "pct") {
			datasets.push(line("Overall", pctSeries(data.overall.pct, i0), text, {borderWidth: 3, order: 0}));
		} else {
			datasets.push(line("Overall value", numSeries(data.overall.value, i0), text,
				{borderWidth: 3, order: 0, fill: "origin", backgroundColor: chartFill}));
			datasets.push(line("Cost basis (open lots)", numSeries(data.overall.cost, i0), cssVar("--text-muted", "#666"),
				{borderDash: [6, 4], order: 1}));
		}
		if (state.show === "all") {
			data.holdings.forEach(function (h, i) {
				var color = chartPalette[i % chartPalette.length];
				var series = state.mode === "pct" ? pctSeries(h.pct, i0) : numSeries(h.value, i0);
				datasets.push(line(h.label, series, color, {order: 2}));
			});
		}

		var opts = chartOptions({legend: true});
		opts.animation = false;
		opts.interaction = {mode: "index", intersect: false};
		opts.scales.y.beginAtZero = state.mode !== "pct";
		opts.scales.y.ticks.callback = function (v) {
			return state.mode === "pct" ? v + "%" : fmtEUR(v);
		};
		opts.scales.y.grid.color = function (ctx) {
			return state.mode === "pct" && ctx.tick && ctx.tick.value === 0
				? cssVar("--text-muted", "#888") : cssVar("--border", "#e3e6eb");
		};
		opts.scales.x.ticks.maxTicksLimit = 8;
		opts.plugins.legend.onClick = function (e, item, legend) {
			var ci = legend.chart, i = item.datasetIndex;
			hidden[item.text] = ci.isDatasetVisible(i);
			if (hidden[item.text]) ci.hide(i); else ci.show(i);
		};
		opts.plugins.tooltip.itemSort = function (a, b) { return b.parsed.y - a.parsed.y; };
		opts.plugins.tooltip.callbacks = {
			label: function (ctx) {
				if (ctx.parsed.y === null) return null;
				var v = ctx.parsed.y;
				return " " + ctx.dataset.label + ": " + (state.mode === "pct" ? fmtPct(v) : fmtEUR(v));
			},
		};

		if (chart) chart.destroy();
		chart = new Chart(canvas, {type: "line", data: {labels: labels, datasets: datasets}, options: opts});

		renderSummary(i0);
		var asOf = labels[labels.length - 1];
		statusEl.textContent = "Daily closes from " + labels[0] + " to " + asOf +
			(data.stale ? " — some served from cache because the price source was unreachable." : ".");
		if (priv && state.mode === "value") statusEl.textContent += " Values hidden in privacy mode.";
	}
})();
`
