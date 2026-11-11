# TODOS

## Done

- [x] One `results.csv` with the latest 5 rows per item.
- [x] `new`, `change`, and `same` status against the previous row for the same item and URL.
- [x] Brand and exact product name as a `product` column.
- [x] Sort the CSV and Obsidian table by total price, cheapest first, with the store next to the link.
- [x] Item name, store, URL, stock, rating, rating count, shipping, total, ordinary price, sale percent, historical low, price trend, and deal check as normal CSV columns.
- [x] Signed price change in NOK and percent, plus `up` or `down`.
- [x] Max 5 offers per shopping-list item per run.
- [x] Prisjakt filter. Keep only an offer at the historical low or below the recent trend.
- [x] `shopping-expert` skill with Norwegian and international stores that ship to Norway.
- [x] Markdown table in the top-level Obsidian note `priceskeptic.md`, with a managed block.
- [x] Two-pass search. The follow-up asks whether there are better sales and reuses the first OpenCode session.
- [x] Immediate run on startup, then scheduled runs at 10:00 and 22:00 local time.
- [x] `scripts/start.sh` and `scripts/stop.sh` service scripts with a PID file and log.
- [x] Trustpilot store score and review count in the CSV and Obsidian table, cached for seven days.
- [x] Store trust as a ranking tie-breaker, plus `-min-trust` as an optional hard filter.
- [x] Server-side deal gate: an offer without a real before-price or verified low is dropped.
- [x] `-backfill-trust` to refresh store scores without a search.
- [x] Unit tests for Trustpilot parsing, domain mapping, the deal gate, and the trust filter.
- [x] `No offer found` rows with a reason and the cheapest rejected price, so every wishlist item appears in the output.
- [x] Source-region tags (NO/SE, EU, US, other), a closeness tie-breaker, and a US landed-cost gate in the skill.
- [x] Legitimacy screen in the skill: identity, returns, payment, and Trustpilot history.
- [x] `websearch.log` with one URL per fetched page or search result, and a `-web-log` flag.
- [x] Miss rows link to the cheapest rejected offer when one exists, so stock can be rechecked.
- [x] Cheapest verified offer is always shown, even without a discount; the deal gate fills the remaining slots.
- [x] `stock_detail` column and Obsidian stock cell, so quantity or low-stock wording is visible.
- [x] Real home-delivery shipping rates filled from store carts; unknown rates written as `NaN` instead of blank.
- [x] Buyer postal code 6773 passed to every search and used for shipping estimates.

## Next

- [ ] Add a Prisjakt product rating column, and its count, read from Prisjakt when the page shows one. Keep it separate from the store Trustpilot score and from the store page's own product rating.
- [ ] Import legacy `results/*.csv` rows. They have only URL and price, so the item, store, and status would be empty or inferred from the URL.
- [ ] Add a delivery estimate column and, for marketplaces, the actual seller.
- [ ] Keep the original currency and the exchange rate used for the NOK conversion.
- [ ] Add unit tests for the CSV store, status transitions, scheduler times, merge order, and the Obsidian block.
- [ ] Add a small review page that groups offers by item and sorts by `total_nok`.
- [ ] Support more than one server instance with file locking. The current in-memory rows assume one process.
- [ ] Make the Obsidian table columns configurable.

## Later

- [ ] Flatten the table in the note or split it per item when it gets too wide.
- [ ] Flag a store that has raised its price since the previous run, even when another store is cheaper.
- [ ] Price history chart per item and URL.
- [ ] Retry a failed scheduled run once after a short delay.
