# State machines in prisjeger

Every long-lived thing here changes state: a run, an item, an offer, a row, a cache entry, a managed note block, a process. This file names each one, its legal states and transitions, and the decision that keeps it honest.

The rule is simple. If two fields must agree, they are one machine. If a transition can be skipped by a race, it is guarded in one place. If a state is written to disk, its legal values live in one table.

## Inventory

| # | Machine | States | Source | Status |
| --- | --- | --- | --- | --- |
| 1 | Search run | `idle`, `running` | `runstate.go`, `server.go`, `search.go` | Explicit tracker |
| 2 | Item search | `queued`, `searching`, `searched`, `followed`, `written`, `missed`, `failed` | `search.go` | Named pipeline, fallbacks documented |
| 3 | Offer qualification | `raw`, `valid`, `deal`, `trusted`, `kept` | `offer.go`, `trust.go` | Explicit gates and drop counters |
| 4 | Stock | `unknown`, `in_stock`, `out_of_stock`, `preorder` | `offer.go` | Parsed at the boundary, alias table |
| 5 | Deal evidence | `at_low`, `below_trend`, `regular`, `unknown`, `none` | `offer.go` | Normalized enum, `none` reserved for miss rows |
| 6 | Price status | `new`, `same`, `change`, `none` | `store.go` | Typed `priceChange` |
| 7 | Row persistence | `loaded`, `dirty`, `committed`, `closed` | `store.go` | Atomic CSV, best-effort note |
| 8 | Price history | `unseen`, `seen`, `capped` | `store.go` | Known limitation, documented |
| 9 | Trustpilot entry | `absent`, `fresh`, `stale`, `unavailable` | `trust.go` | TTL cache, no negative cache |
| 10 | Notification | `disabled`, `ready`, `cooling`, `failed` | `notify.go` | Explicit snapshot |
| 11 | Scheduler | `disabled`, `waiting`, `fired`, `skipped` | `server.go` | `next_run_at` exposed |
| 12 | Obsidian block | `absent`, `closed`, `open` | `note.go` | Explicit span parser |
| 13 | CSV schema | `missing`, `legacy`, `current`, `corrupt` | `store.go` | Optional-column migration |
| 14 | Service process | `stopped`, `running`, `stale` | `start.sh`, `stop.sh` | PID file with identity check |

## 1. Search run

The only real concurrency machine. Before this change its state was three counters, a semaphore, and a boolean argument.

```text
            POST /search, startup, schedule
idle ───────────────────────────────────────► running
  ▲                                              │
  └──────────────── last item finishes ──────────┘
```

Invariants:

- At most one run is `running`.
- `total == active + done + failed` at every observation.
- `active` reaches zero exactly once per run, and that transition fires the notification.
- A start request during a run is a no-op, not a second run.

The tracker in `runstate.go` owns every transition under one mutex. `begin` is the only way to enter `running`; `finish` is the only way to leave it. `startRun` no longer takes `skipIfActive`; it always asks `begin`, and the answer decides. That deletes the check-then-act race where two requests both saw `active == 0`.

`/status` reports `state`, the current `run`, `next_run_at`, and the notification snapshot. The old top-level `active`, `done`, `failed`, and `last_error` fields remain for existing clients and mirror the current run.

## 2. Item search

One item walks a fixed pipeline:

```text
queued → searching → first pass → follow-up → normalize → trust → write
                          │            │
                          │            └─ follow-up failed: keep first pass
                          └─ first pass failed: keep old rows, or record a miss
```

The states are implicit in straight-line code, and that is fine. There are no parallel branches that can disagree. The one decision worth naming is the failure fallback:

- A failed first pass with existing rows leaves those rows alone.
- A failed first pass with no rows writes a `No offer found` row.
- A failed follow-up never fails the item; pass one still wins.
- A successful write replaces the item's rows; it never merges with the old set.

`finish` runs in a `defer`, so an early return cannot leave the run's `active` count stuck.

## 3. Offer qualification

An offer moves through gates in a fixed order. Each gate can only remove:

| Gate | Removes | Counted as |
| --- | --- | --- |
| Parse | missing store, bad URL, non-positive price, out-of-range numbers | `dropped` |
| Prisjakt | a Prisjakt row that is neither `at_low` nor `below_trend` | `dropped` |
| Dedupe | a repeated URL, keeping the first (follow-up pass first) | `dropped` |
| Deal | a non-cheapest row with no sale, low, or trend evidence | `dropped` |
| Cap | rows beyond five, except the pinned cheapest | `capped` |
| Trust | a known score below `-min-trust` | `low_trust` |

The cheapest buyable offer is pinned before the deal gate. It survives the gate and the cap. `buyable` is the state predicate: out of stock is never buyable, and a foreign offer with no landed cost is not comparable.

## 4. Stock

```text
page text ──parse──► unknown | in_stock | out_of_stock | preorder
                                │
                                └─ stock_detail carries the page's own wording
```

`splitStock` accepts the enum, Norwegian and English page wording, and a bare count. A count of zero is `out_of_stock`; a positive count is `in_stock` with the count as detail. `stock_detail` is display-only and never changes the ranking.

## 5. Deal evidence

`at_low`, `below_trend`, and `regular` mean evidence was checked. `unknown` means the search could not check. `none` is reserved for a miss row and never describes an offer.

`normalizeEnum` maps a typo to `unknown` rather than admitting a new value. Persisted rows are validated against the same table on load.

## 6. Price status

```text
no previous price ──────────────► new
previous price, |delta| ≤ 0.005 ► same  (0.00, 0.0, same)
previous price, delta > 0 ──────► change (up,   +NOK, +P%)
previous price, delta < 0 ──────► change (down, -NOK, -P%)
miss row ───────────────────────► none
```

`diffPrice` returns one `priceChange` value. A `change` cannot exist without its NOK, percent, and direction, because they are produced together. `newStoredRow` writes the same strings as before.

## 7. Row persistence

```text
loaded ──Replace/NoOffer/Prune──► dirty ──write CSV──► committed
                                      │                    │
                                      └── write failed ────┘ (memory unchanged)
```

`commitLocked` writes the CSV to a temp file and renames it, then swaps the in-memory rows, then rewrites the note. A CSV failure leaves memory and disk agreeing on the old state. A note failure is logged and does not fail the write; the next commit repairs it. `closed` is a terminal state and every mutating method rejects it.

## 8. Price history

The row set keeps the latest five offers per item. Price history is keyed by `item + URL` and is rebuilt from those rows.

Known limitation: a URL that falls out of the latest five loses its previous price, so it can return as `new`. This is documented in the README. A sidecar history file would fix it, but that adds a second source of truth and a migration. It is deliberately not part of this change.

## 9. Trustpilot entry

```text
absent ──lookup ok──► fresh ──7 days──► stale ──lookup ok──► fresh
   ▲                     │                 │
   └──── lookup fails ───┴─────────────────┘ (stays absent/stale)
```

A failed lookup is not cached, so the next run retries. A missing profile is unknown, not bad, and never fails a search. The cache is a single JSON file, so one server process owns it.

## 10. Notification

```text
disabled ──token+chat set──► ready ──success──► cooling (6h) ──elapsed──► ready
                               │                    │
                               └── failure ─────────┘ (stays ready, retries next run)
```

The cooldown starts only after a successful send. A failed automatic send therefore retries on the next completed run instead of going silent for six hours. A manual `POST /notify` ignores the window and, on success, becomes the last send, so an automatic send seconds later is suppressed. `/status` exposes `enabled`, `last_sent_at`, and `last_error`.

## 11. Scheduler

```text
disabled (no times) | waiting(next) ──timer──► fired ──run busy?──► skipped
                                             └────────────────────► started
```

`next_run_at` is computed from the configured times, so a client can see when the next run is due without reading logs. A skipped run is a log line; the active run and the following slot are untouched.

## 12. Obsidian block

```text
absent ──append──► closed (start + end) ──rewrite span──► closed
                       │
                       └── end deleted by hand ──► open ──rewrite to EOF──► closed
```

`findNoteBlock` returns the span. A block with a start marker and no end marker is replaced to EOF instead of appended, so a hand-edited file cannot accumulate duplicate blocks. A file with no markers gets one block appended and the rest of the note is preserved.

## 13. CSV schema

`loadRows` accepts the current header, an older header with optional columns missing, an empty file, and a corrupt file. Missing optional columns default empty and are rewritten on the next save. A missing required column is a hard error, because guessing its meaning would corrupt the output.

## 14. Service process

`start.sh` and `stop.sh` model the PID file as three states: absent (stopped), live and matching the binary (running), or stale/foreign (remove and report). A stale PID is never signaled. This is the shell-level counterpart to the Go run machine.

## Smaller machines worth naming

| Machine | States | Decision |
| --- | --- | --- |
| Agent child process | `starting`, `streaming`, `exited`, `timed out` | One context deadline per pass. A timeout is a run error; a follow-up failure is not. |
| Item list | `missing`, `empty`, `readable` | An empty list rejects a run. A missing list is logged at startup but the store still opens. |
| Web log | `disabled`, `appending` | Append-only. An empty path disables it; there is no state to reconcile. |
| Startup mode | `serve`, `backfill` | The backfill branch runs before the HTTP listener and exits. |

## What stayed implicit, on purpose

- The search prompts are text, not code. Their ordering and quality are an agent concern, not a process state.
- Region normalization is a pure function with a fallback, not a lifecycle.
- Sorting is a pure ordering over rows, not a machine.
- The `flexFloat` and `flexString` wrappers model "absent" versus "present" for one JSON field. That is a parser rule, not a lifecycle.

## Verification

`go test ./...`, `go test -race ./...`, and `go vet ./...` cover the tracker transitions, the status shape, notification retry, the unclosed note block, and the golden CSV and note output. The live check starts the binary and watches `/status` cross `idle → running → idle` while `/search` runs.
