package web

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// handleAnniversariesICS serves the upcoming 8-year deemed-disposal
// anniversaries as an iCalendar feed, so the date a market value will
// be needed is in a calendar before it arrives, not discovered when a
// holding blocks.
//
// Privacy: the file is meant to be imported into a calendar app, which
// may sync to a third party. It therefore carries only the holding's
// name and the date — never quantities, cost, values or any tax figure
// (AGENTS.md §7: no account data leaves the machine).
//
// Only anniversaries still ahead are included. A reached anniversary
// with no value is already surfaced, loudly, on the dashboard; a past
// all-day event in a calendar is just clutter.
func (s *Server) handleAnniversariesICS(w http.ResponseWriter, r *http.Request) {
	txs, err := s.ledgerStore.All()
	if err != nil {
		http.Error(w, fmt.Sprintf("loading transactions: %v", err), http.StatusInternalServerError)
		return
	}

	var holdingTxs []ledger.Transaction
	byInstrument := map[string][]ledger.Transaction{}
	for _, tx := range txs {
		if tx.Type == ledger.TypeInterest {
			continue
		}
		holdingTxs = append(holdingTxs, tx)
		byInstrument[tx.Instrument] = append(byInstrument[tx.Instrument], tx)
	}

	now := time.Now().UTC()
	rows, _, err := s.anniversarySchedule(distinctInstruments(holdingTxs), byInstrument, now)
	if err != nil {
		http.Error(w, fmt.Sprintf("building the deemed-disposal schedule: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="taxman-deemed-disposals.ics"`)
	_, _ = w.Write([]byte(buildAnniversaryICS(rows, now)))
}

// buildAnniversaryICS renders the not-yet-reached rows as VEVENTs.
// Output is deterministic for a given input: UIDs are a hash of the
// holding, lot and date, so re-importing updates events rather than
// duplicating them.
func buildAnniversaryICS(rows []anniversaryRow, now time.Time) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(foldICSLine(s)); b.WriteString("\r\n") }

	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//taxman//deemed-disposal anniversaries//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:taxman deemed disposals")

	stamp := now.Format("20060102T150405Z")
	for _, row := range rows {
		if row.Reached {
			continue
		}
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			continue
		}

		name := row.Instrument
		if row.Description != "" {
			name = row.Description + " (" + row.Instrument + ")"
		}
		sum := sha1.Sum([]byte(row.Instrument + "|" + row.LotAcquired + "|" + row.Date))

		line("BEGIN:VEVENT")
		line("UID:anniv-" + hex.EncodeToString(sum[:8]) + "@taxman")
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + date.Format("20060102"))
		line("DTEND;VALUE=DATE:" + date.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + escapeICSText("8-year deemed disposal: "+name))
		line("DESCRIPTION:" + escapeICSText(
			"A fund lot reaches its 8-year deemed-disposal anniversary (TCA 1997 s.747E(6)). "+
				"Find the market value per unit on this date and enter it in taxman so the holding can compute."))
		line("TRANSP:TRANSPARENT")
		for _, trigger := range []string{"-P30D", "-P7D"} {
			line("BEGIN:VALARM")
			line("ACTION:DISPLAY")
			line("DESCRIPTION:" + escapeICSText("Deemed-disposal anniversary approaching: "+name))
			line("TRIGGER:" + trigger)
			line("END:VALARM")
		}
		line("END:VEVENT")
	}

	line("END:VCALENDAR")
	return b.String()
}

// escapeICSText escapes the characters RFC 5545 §3.3.11 reserves in a
// TEXT value.
func escapeICSText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`)
	return r.Replace(s)
}

// foldICSLine folds a content line at 75 octets (RFC 5545 §3.1) without
// splitting a UTF-8 sequence. Continuation lines start with one space.
func foldICSLine(s string) string {
	const limit = 75
	if len(s) <= limit {
		return s
	}
	var b strings.Builder
	size := 0
	for _, r := range s {
		n := utf8.RuneLen(r)
		if size+n > limit {
			b.WriteString("\r\n ")
			size = 1
		}
		b.WriteRune(r)
		size += n
	}
	return b.String()
}
