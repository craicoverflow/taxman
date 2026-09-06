# How taxman calculates each tax figure

This document describes, in plain English and with the Irish tax-law
reference for each rule, every calculation `taxman` performs to turn a
list of buy / sell / vest / interest events into a Capital Gains Tax,
fund exit-tax or DIRT figure.

It is written to be checked by someone who knows Irish investment tax
but not Go. Its companion is the set of executable scenarios in
[`../features/`](../features/): each `.feature` file is a worked example
whose figures are produced by the calculation engine itself and pinned
into the file, so the description here and the code cannot silently
drift apart — a change to either that breaks the agreement fails the
build (`make ci`).

> **Every number in this document is produced by `taxman` and pinned in
> a named `.feature` file or golden fixture. None of it is calculated by
> hand.** The arithmetic shown in a worked example is the engine's own
> chain of intermediate results, reproduced so it can be followed and
> checked against the law.

> **This is not tax advice and `taxman` is not certified tax software.**
> It is a single person's tool for reconstructing a running liability
> from broker exports. Every figure must still be reconciled against
> your own records — and, where practical, an independent calculation —
> before it is used for a return. Some rules are deliberately not
> implemented (§11).

---

## 1. Scope

| Tax | What `taxman` computes | Regime |
|---|---|---|
| **Capital Gains Tax (CGT)** | Gains on disposals of `CGT_ASSET` holdings — individual shares, most US-domiciled ETFs, vested RSU shares | §6, §7 |
| **Fund exit tax** | Gains on disposals of `EXIT_TAX_FUND` holdings — Irish/EU-domiciled UCITS ETFs and equivalent offshore funds | §8 |
| **Deposit Interest Retention Tax (DIRT)** | Tax on hand-entered deposit interest (N26, Trade Republic) | §9 |

A holding is `CGT_ASSET` or `EXIT_TAX_FUND` **only** because a human set
it. Until then it is `UNCLASSIFIED` and every calculation for it is
refused, not guessed (§11.3). Which regime a holding falls under is the
single most consequential question and `taxman` never answers it
itself.

Not computed at all: income tax on dividends or on RSU vesting (that is
payroll's), the four-week loss rule, deemed-disposal *liability*, and
anything filed to Revenue (§11).

---

## 2. Conventions that apply everywhere

**Decimal arithmetic.** All money is held as exact decimals, never as
binary floating point, so repeated addition and multiplication do not
accumulate representation error.

**Euro first.** Every amount is converted to euro (§4) *before* any
gain, loss or tax is computed. A lot's cost and a sale's proceeds are
each fixed in euro on their own transaction date and never
re-translated.

**Round once, at the end, downward.** Only the final tax figure is
rounded, and it is rounded **down** to whole euro — the cents fall in
the taxpayer's favour, which is the long-standing Revenue
self-assessment convention (the CGT return, Form CG1, and Form 11 are
completed in whole euro). Gains, proceeds, cost bases, the annual
exemption, loss pools and exchange-rate conversions all keep full
precision. Worked examples: [`whole_euro_rounding.feature`](../features/whole_euro_rounding.feature).
Implemented in [`internal/engine/round.go`](../internal/engine/round.go).

**The event's date decides the rule.** A rate or threshold is always
looked up by the date of the disposal, chargeable event or interest
credit it applies to — never by the date the calculation is run. A
disposal dated 31 December 2025 is taxed under the 2025 rules whenever
`taxman` is run (§10). Irish tax years are calendar years (TCA 1997
s.2(1) "year of assessment").

**Never guess.** An unresolvable exchange rate, an unknown currency, a
sale of more units than were ever bought, or an unclassified holding is
an explicit error. `taxman` stops rather than produce a plausible wrong
number.

---

## 3. From transactions to lots

Every acquisition opens a **lot**: a quantity, an acquisition date, and
a total cost in euro.

| Event | Lot cost basis |
|---|---|
| **Buy** | Quantity × price, converted to euro at the trade date (§4) |
| **RSU vest** | Quantity × fair market value per share at the vesting date, in euro |

**RSU vests.** When Restricted Stock Units vest, the market value of the
shares on the vesting date is charged to income tax through payroll —
that is outside `taxman`. The *same* vesting-date market value becomes
the CGT base cost of the shares, so the amount already taxed as income
is not taxed again as a capital gain (Revenue, *Restricted Stock Units
(RSUs)*; Revenue Share Schemes Manual, Chapter 2). `taxman` builds the
lot from the vest exactly as from a buy, using that value as cost —
never zero, never the eventual sale price. Worked example:
[`rsu_vest.feature`](../features/rsu_vest.feature).

---

## 4. Restating non-euro transactions in euro

A transaction in any currency other than euro is restated in euro at
the **European Central Bank euro reference rate for that transaction's
own date**, before matching.

**Irish basis.** Any currency other than the euro is itself an asset
for CGT; a gain or loss is "computed by reference to the corresponding
euro value of the purchase price and the sale proceeds", and foreign
consideration is converted "using the rate of exchange applicable at
the date of" the transaction (Revenue, *How to calculate CGT*; Revenue
Tax and Duty Manual Part 19-01-14a; TCA 1997 s.532, s.552).

**Rate source.** The ECB euro foreign-exchange reference rates,
embedded in the tool as `eurofxref-hist.csv`
([`internal/fx`](../internal/fx/)). A transaction on a weekend or a
TARGET holiday resolves to the most recent prior publication day. A
date the series does not cover — before it begins in 1999, or after the
embedded file ends — is an error; the file must be refreshed rather
than the rate approximated.

Worked examples, including a full CGT computation on a US-dollar
holding: [`fx_restatement.feature`](../features/fx_restatement.feature).
Design rationale: [`docs/adr/0001-fx-conversion.md`](adr/0001-fx-conversion.md);
IBKR flat exports carry their own rate — [`docs/adr/0002-ibkr-flat-export-fx.md`](adr/0002-ibkr-flat-export-fx.md).

---

## 5. Matching disposals to lots: FIFO

Each sale is matched against the holding's open lots **oldest first** —
first in, first out.

For each disposal the engine records:

```
proceeds   = sale quantity × sale price          (in euro, at the sale date)
cost basis = Σ over the lots consumed of
             (units taken from the lot × that lot's cost per unit)
gain       = proceeds − cost basis               (may be negative)
```

A sale is matched across as many lots as needed; a lot is partly
consumed and its remainder stays open for the next sale. Selling more
units than were ever acquired is an explicit error.

**Irish basis.** For shares of the same class held without individual
identification, "the asset purchased first is treated as the first
asset sold (the first in first out (FIFO) rule)" (Revenue Tax and Duty
Manual Part 19-04-03, *Disposals of marketable shares and securities
(S.581)*, reviewed August 2023; TCA 1997 s.581). The one statutory
exception — a sale within four weeks of a purchase — is **not** applied
by `taxman` (§11.1).

Worked examples: [`fifo_matching.feature`](../features/fifo_matching.feature).

---

## 6. CGT on a single holding

The building block. The real annual charge nets every holding together
(§7); this is one holding in isolation, and is what several golden
fixtures exercise directly.

```
total gain   = Σ of every disposal's gain (for this holding)
taxable gain = max(0, total gain − €1,270)
CGT due      = floor_to_whole_euro( taxable gain × CGT rate )
```

- **CGT rate: 33%** for disposals on or after 6 December 2012 (Revenue,
  *How to calculate CGT*; TCA 1997 s.28). Resolved at the date of the
  most recent disposal in the set (§10).
- **Annual exempt amount: €1,270** — see §7 for how it actually applies
  across holdings.
- The tax is floored to whole euro (§2); the gain is not.

Worked examples: [`cgt_single_holding.feature`](../features/cgt_single_holding.feature).

---

## 7. CGT for a whole tax year, across every holding

This is how an individual's CGT for a year is actually assessed, and it
**cannot** be done one holding at a time, because the annual exemption
is personal and losses move between holdings.

### 7.1 Net the year's gains and losses across all holdings

Every `CGT_ASSET` holding's disposals in the year are FIFO-matched
against that holding's full history, then **all** the gains and losses
are added together:

```
net chargeable gain = Σ over every CGT_ASSET holding of
                      (Σ that holding's in-year disposal gains and losses)
```

A loss on one holding reduces a gain on another. This is the aggregation
in **TCA 1997 s.31** — CGT is charged on the aggregate of chargeable
gains for the year after deducting allowable losses (Revenue Tax and
Duty Manual Part 19-02-05). The net figure may be negative.

### 7.2 Bring forward unused losses from earlier years

An unused net capital loss from an earlier year is carried forward
indefinitely and set against a later year's net gain — but **only so
far as to leave the €1,270 annual exemption un-wasted**:

```
usable brought-forward loss = max(0, net chargeable gain − €1,270)
loss brought forward used   = min(loss pool, usable brought-forward loss)
```

Any remaining pool carries forward again. `taxman` derives the pool
statelessly: it replays every year from the earliest disposal on
record up to the year in question, so it needs the full history, not
just the target year's rows.

**Irish basis.** TCA 1997 s.601 and the restriction in s.601(3)–(4):
losses carried forward from an earlier year may not be used to reduce a
year's chargeable gains below the annual exempt amount (Revenue Tax and
Duty Manual Part 19-07-01, *Annual exempt amount (S.601)*, reviewed
December 2024). **Current-year losses are not so protected** — §7.1
nets them in full even if that wastes the exemption. `taxman` reflects
this asymmetry: current-year losses net unconditionally in §7.1;
brought-forward losses are throttled here.

> Capital losses realised *before* the earliest transaction in the
> ledger are unknowable and are not included. If there are such losses,
> the figure understates the relief available.

### 7.3 Deduct one annual exemption, apply the rate, round

```
after losses  = net chargeable gain − loss brought forward used
exemption used = min( max(0, after losses), €1,270 )
taxable gain   = max(0, after losses − exemption used)
CGT due        = floor_to_whole_euro( taxable gain × CGT rate at 31 December )
```

- **One €1,270 exemption for the year**, never one per holding. It is
  per individual, is not available to companies or trustees, and an
  unused part **cannot** be transferred to a spouse or civil partner
  (TCA 1997 s.601; Revenue Tax and Duty Manual Part 19-07-01). The
  €1,270 amount has been unchanged since the 2002 euro changeover.
- The **rate is resolved at 31 December** of the tax year (§10).
- The tax is floored to whole euro (§2).

### 7.4 Worked examples

All three are pinned in
[`cgt_year_aggregation.feature`](../features/cgt_year_aggregation.feature)
and mirror the golden fixtures in
[`testdata/golden/`](../testdata/golden/):

| Scenario | Shows |
|---|---|
| Two holdings sold in one year | One €1,270 exemption shared, not applied twice |
| Net loss carried into a later gaining year | Brought-forward loss set against a later gain, down to the exemption |
| Small later gain, large brought-forward loss | Only enough brought-forward loss is used to reach the exemption; the rest carries on |

For the first: two holdings with in-year gains of €2,000 and €3,000
net to a €5,000 chargeable gain; one €1,270 exemption leaves €3,730
taxable; at 33% that is €1,230.90, floored to **€1,230**. A per-holding
model would wrongly apply the exemption twice (tax €811).

---

## 8. Fund exit tax

Applied to a holding classified `EXIT_TAX_FUND` — Irish and EU-domiciled
UCITS ETFs and equivalent offshore funds, taxed under the fund
"gross roll-up" / exit-tax regime rather than CGT (TCA 1997 Part 27
Chapter 1A, s.739B–739G; equivalent offshore-fund regime s.747AA–747FA;
Revenue, *Funds*; Revenue Tax and Duty Manual Part 27-01a-02 and
Part 27-04-01).

Disposals are FIFO-matched exactly as for CGT (§5), then:

```
total gain   = Σ of every disposal's gain          (shown for transparency; may be negative)
taxable gain = Σ of only the POSITIVE per-disposal gains
exit tax due = floor_to_whole_euro( taxable gain × exit-tax rate )
```

Three ways this differs from CGT, each pinned in
[`exit_tax.feature`](../features/exit_tax.feature):

1. **Rate: 41%**, reducing to **38% for a chargeable event on or after
   1 January 2026** (Finance Act 2025 — see gov.ie, *Minister Donohoe
   publishes Finance Bill 2025*; the reduction also covers equivalent
   offshore funds including ETFs in this regime, life assurance exit tax
   and certain foreign life policies). The rate is fixed by the event's
   date (§10): a disposal on 31 December 2025 is at 41%; the same fund
   sold on 1 January 2026 is at 38%.
2. **No annual exemption.** The €1,270 is never applied.
3. **No loss relief per chargeable event.** A disposal that makes a
   loss contributes **zero** — it never nets against a gaining disposal,
   not even the same fund in the same year. `taxable gain` sums only the
   positive gains. (Example: a €1,000 gain and a €700 loss on the same
   fund in the same year are taxed as €1,000, not €300.)

`taxman` also tracks the 8-year deemed-disposal date for each fund lot,
but does **not** compute the tax charge for it — §11.2.

> **Not separately verified:** that Revenue's exit-tax rules match
> disposals in strict FIFO order (as CGT does). `taxman` assumes so.
> Worth a check before relying on an exit-tax figure for a fund with
> multiple purchase lots.

---

## 9. DIRT

No lots, no matching. `taxman` totals the hand-entered interest credits
(N26, Trade Republic — neither bank has a clean export) and applies the
DIRT rate:

```
total interest = Σ of the interest-credit amounts   (euro only)
DIRT due       = floor_to_whole_euro( total interest × DIRT rate )
```

- DIRT is the "appropriate tax" of **TCA 1997 s.256(1)** (Revenue Tax
  and Duty Manual Part 08-04-01, ss.256–267).
- **Rate: 33%** since 1 January 2020 (Revenue, *What DIRT rate is
  applicable?*). Earlier years resolve to their own rate — the 41% rate
  of 2014–2016 was reduced two points a year from 2017 under Finance
  Act 2016 (39% / 37% / 35%) before reaching 33% in 2020.
- The whole-history figure resolves the rate at the **latest** credit's
  date; the per-year figure resolves it within that year (§10).
- Interest must be in euro; a non-euro credit is an error.

Worked examples: [`dirt.feature`](../features/dirt.feature).

---

## 10. The rate schedule

Every rate lives as **effective-dated data** in
[`internal/taxrules`](../internal/taxrules/), never as a constant in the
calculation engine, and is looked up by the date of the event it
applies to. A lookup for an unknown tax, or for a date before the
schedule begins, returns an error — never a zero treated as valid.

| Tax | Current rate | From | Also on record |
|---|---|---|---|
| CGT | 33% | 6 Dec 2012 (TCA 1997 s.28) | 30% / 25% / 22% / 20% earlier — **pre-2013 entries UNVERIFIED** |
| Exit tax | 41%; **38% from 1 Jan 2026** (Finance Act 2025) | 41% from 1 Jan 2014 (Finance Act 2013) | 36% from 2013; earlier entries **UNVERIFIED** |
| DIRT | 33% | 1 Jan 2020 | 41% (2014–2016) stepping down to 33% — held with confidence; **pre-2014 UNVERIFIED** |
| CGT annual exemption | €1,270 | unchanged since 2002 | — |

> **UNVERIFIED entries** were reconstructed from memory of past Budgets
> so that a historical backfill has a defensible per-year rate rather
> than taxing every past year at today's rate. Before relying on a
> figure for a tax year earlier than the current one, confirm the rate
> and its effective date against the relevant Revenue Tax and Duty
> Manual or Finance Act.

**Runtime override.** Each rate (not the exemption) can be overridden
for a run by an environment variable — `TAXMAN_CGT_RATE`,
`TAXMAN_EXIT_TAX_RATE`, `TAXMAN_DIRT_RATE`, each a fraction such as
`0.38`. The override replaces that rate for **all** event dates, so it
is only correct when every figure in the run falls under the new rate.

Worked examples: [`rate_schedule.feature`](../features/rate_schedule.feature).

---

## 11. What `taxman` deliberately does not compute

These are not oversights. Each fails safe — an explicit gap rather than
a wrong number — and is pinned in
[`known_limitations.feature`](../features/known_limitations.feature).

### 11.1 The four-week ("bed-and-breakfast") loss rule — TCA 1997 s.581

Where shares are sold and shares of the same class are reacquired within
four weeks, two things change (Revenue Tax and Duty Manual Part 19-04-03):

- the sale is matched against the **reacquired** shares, not the older
  holding (last-in-first-out within the window); and
- a **loss** on that sale is allowed **only** against a later gain on
  the disposal of those reacquired shares — it cannot net against other
  gains. A partial reacquisition restricts a proportionate part of the
  loss.

`taxman` does neither: it matches plain FIFO and lets the loss net
normally (§7.1). A CGT return covering a sell-and-rebuy inside four
weeks **must be adjusted by hand**. Blocked pending a cited Revenue
source for the exact expected figures.

### 11.2 The 8-year deemed-disposal charge

Under the fund regime, tax is levied eight years after an investment is
made, and every eight years after that, whether or not a disposal
actually occurs (Revenue, *Funds*; TCA 1997 s.739E). `taxman`:

- **does** track the next deemed-disposal date for every fund lot
  (acquisition + 8 years, + 16, …), so it is not missed;
- **does not** turn a reached anniversary into an exit-tax charge, and
  does not handle the credit for that charge against the eventual actual
  disposal.

Those figures are computed outside `taxman`. Blocked pending a cited
Revenue source for the credit/refund mechanics.

### 11.3 An unclassified holding

`taxman` never guesses `CGT_ASSET` vs `EXIT_TAX_FUND`. Until the
classification is set, every calculation for that holding is refused
(other, classified holdings in the same run are unaffected).

### 11.4 Also out of scope

Dividend and distribution income (recorded, not processed); income tax,
USC or PRSI on RSU vesting (payroll's); any submission to Revenue / ROS.

---

## 12. Tracing a figure back to its inputs

`taxman report` links every figure it produces, through
[`internal/audit`](../internal/audit/), back to the specific
transactions and the specific dated rule version that produced it. The
one exception is the live dashboard summary, which is recomputed on
every page view and not persisted (by design).

The intent (SPEC.md success criteria) is that every output is
cross-checked against your own records — and, where practical, an
independent calculation such as a Tax-Wizard run — before it is
trusted for a return.

---

## 13. Index: rule → executable proof

| Rule | Section | Feature file | Golden fixture(s) |
|---|---|---|---|
| Lot cost basis; RSU vest = vesting-date value | §3 | [`rsu_vest.feature`](../features/rsu_vest.feature) | `rsu_vest_disposal` |
| Euro restatement at transaction-date ECB rate | §4 | [`fx_restatement.feature`](../features/fx_restatement.feature) | `cgt_fx_conversion` |
| FIFO lot matching; oversell is an error | §5 | [`fifo_matching.feature`](../features/fifo_matching.feature) | `cgt_simple_fifo` |
| CGT on a single holding | §6 | [`cgt_single_holding.feature`](../features/cgt_single_holding.feature) | `cgt_simple_fifo` |
| Year-level CGT: s.31 netting, s.601 exemption & loss carry-forward | §7 | [`cgt_year_aggregation.feature`](../features/cgt_year_aggregation.feature) | `cgt_year_single_exemption`, `cgt_loss_carryforward` |
| Exit tax: rate, no exemption, no loss relief, 2026 transition | §8 | [`exit_tax.feature`](../features/exit_tax.feature) | `exittax_actual_disposal`, `exittax_no_intra_fund_netting`, `exittax_rate_transition` |
| DIRT: total × rate at credit date; historical rates | §9 | [`dirt.feature`](../features/dirt.feature) | `dirt_simple`, `dirt_historical_rate` |
| Effective-dated rate lookup | §10 | [`rate_schedule.feature`](../features/rate_schedule.feature) | (all of the above) |
| Round the tax down to whole euro | §2 | [`whole_euro_rounding.feature`](../features/whole_euro_rounding.feature) | `cgt_fx_conversion` |
| s.581, deemed-disposal charge, unclassified holding | §11 | [`known_limitations.feature`](../features/known_limitations.feature) | — |

To run every scenario: `make bdd`. To rebuild the pinned figures after
an engine change: `make regen-features` (the build fails if this is not
kept current). To render this document plus the feature files as one
page for sharing: `make docs`.

---

## Sources

Irish primary and Revenue guidance referenced above (retrieved
September 2026):

- **Taxes Consolidation Act 1997** — s.2 (year of assessment); s.28
  (CGT rate); s.31 (aggregation of gains and losses); s.532, s.545,
  s.552 (assets, consideration, allowable cost); s.581 (disposals of
  marketable shares — four-week rule); s.601 (annual exempt amount);
  Part 27 Chapter 1A, s.739B–739G (investment undertakings / exit tax);
  s.747AA–747FA (equivalent offshore funds); s.256–267 (DIRT).
- **Finance Act 2025** — reduction of the investment-undertaking /
  exit-tax rate from 41% to 38% for chargeable events on or after
  1 January 2026 (gov.ie, *Minister Donohoe publishes Finance Bill
  2025*).
- **Revenue Tax and Duty Manuals** — Part 19-07-01, *Annual exempt
  amount (S.601)* (reviewed December 2024); Part 19-04-03, *Disposals
  of marketable shares and securities (S.581)* (reviewed August 2023);
  Part 19-02-05 (loss relief); Part 19-01-14a (foreign-currency
  gains/losses); Part 27-01a-02 and Part 27-04-01 (investment
  undertakings and offshore funds); Part 08-04-01 (DIRT, ss.256–267).
- **Revenue.ie guidance pages** — *How to calculate CGT*; *What is
  exempt from CGT?*; *What DIRT rate is applicable?*; *Funds*;
  *Restricted Stock Units (RSUs)*.
- **Revenue Share Schemes Manual** — Chapter 2 (Restricted Stock
  Units).
- **European Central Bank** — euro foreign-exchange reference rates
  (`eurofxref-hist.csv`).

The exact subsection of s.601 for the brought-forward-loss restriction
(s.601(3) and/or (4)) should be confirmed against a current consolidated
copy of the Act; the rule as `taxman` applies it — brought-forward
losses throttled to the exemption, current-year losses not — matches
Revenue Tax and Duty Manual Part 19-07-01.
