package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Platform identifies which brokerage/bank a Transaction came from.
type Platform string

const (
	PlatformDegiro        Platform = "degiro"
	PlatformIBKR          Platform = "ibkr"
	PlatformETRADE        Platform = "etrade"
	PlatformN26           Platform = "n26"
	PlatformTradeRepublic Platform = "traderepublic"
)

// Type identifies the kind of event a Transaction represents.
type Type string

const (
	TypeBuy      Type = "buy"
	TypeSell     Type = "sell"
	TypeDividend Type = "dividend" // recorded but not processed by the engine in v1 — see SPEC.md §2
	TypeRSUVest  Type = "rsu_vest"
	TypeInterest Type = "interest"
)

// Transaction is a single raw event ingested from a platform export:
// a buy, sell, dividend, RSU vest, or interest credit. It is immutable
// once ingested — see SPEC.md §2 for the full domain model.
type Transaction struct {
	// Platform, Type, Date, Instrument, Quantity, Price, and Currency
	// together identify the transaction: fingerprinting is derived
	// only from these fields.
	Platform   Platform
	Type       Type
	Date       time.Time
	Instrument string // ISIN or platform-specific identifier
	Quantity   decimal.Decimal
	Price      decimal.Decimal
	Currency   string // ISO 4217

	// SourceRef is the broker's own reference for this transaction
	// (e.g. an order/note ID). It's kept for human traceability only
	// and deliberately excluded from the fingerprint: the same
	// logical transaction can appear under a different SourceRef
	// across overlapping exports, and that must not be treated as a
	// distinct transaction.
	SourceRef string

	// Description is a human-readable name for Instrument, when the
	// source export provided one (e.g. Degiro's "Product" column, or
	// IBKR/eTrade's ticker "Symbol") — Instrument itself is usually an
	// ISIN, which nobody can eyeball and recognize. Like SourceRef,
	// it's for display only and deliberately excluded from the
	// fingerprint: the same holding's Description may read
	// differently across exports (or be entirely absent) without that
	// being a different logical transaction.
	Description string
}

// Fingerprint returns a deterministic identifier for this transaction,
// derived only from its identifying fields (Platform, Type, Date,
// Instrument, Quantity, Price, Currency). Two Transactions describing
// the same logical event — even if ingested from different exports
// with different SourceRefs — produce the same Fingerprint, which is
// what makes ingestion idempotent.
func (t Transaction) Fingerprint() string {
	h := sha256.New()
	// hash.Hash.Write never returns an error (see the hash.Hash
	// doc comment); the error is discarded deliberately, not
	// overlooked.
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s",
		t.Platform,
		t.Type,
		t.Date.UTC().Format(time.RFC3339),
		t.Instrument,
		t.Quantity.String(),
		t.Price.String(),
		t.Currency,
	)
	return hex.EncodeToString(h.Sum(nil))
}
