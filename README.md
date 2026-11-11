# prisjeger

**Your shopping list, hunted while you sleep.**

[![Go 1.27.3](https://img.shields.io/badge/Go-1.27.3-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![test](https://img.shields.io/badge/test-go%20test-00ADD8)](docs/STATE-MACHINES.md#verification)
[![platform](https://img.shields.io/badge/platform-Linux-FCC624?logo=linux&logoColor=black)](#quick-start)
[![license](https://img.shields.io/badge/license-not%20declared-lightgrey)](#license-and-contact)

## At a glance

prisjeger is a local Go service that turns `handleliste.txt` into a ranked list of verified offers. It runs the `shopping-expert` agent twice per item, checks the landed cost to Norway, drops fake discounts, and writes `results.csv` plus an Obsidian table.

> **Core value.** Every kept row is a verified landed cost, not a search snippet. The cheapest buyable offer is always shown, even when it is not on sale.

## Core features

| | Capability | What it does |
| --- | --- | --- |
| 🔎 | Two-pass agent search | Pass one finds offers; pass two asks for better sales in the same OpenCode session. A failed follow-up still keeps pass one. |
| 🇳🇴 | Real landed cost | Shipping, VAT, import, and handling to postal code 6773. Unknown shipping is `NaN`, not a fake zero. |
| 🛡️ | Deal gate and store trust | Drops regular prices without evidence, except the cheapest buyable offer. Trustpilot is a tie-breaker and an optional hard filter. |
| 🔔 | Runs on a clock | Startup run, then 10:00 and 22:00 local. One run at a time. Telegram digest after each completed run. |

## Tech stack

![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)
![OpenCode](https://img.shields.io/badge/OpenCode-agent-111111)
![Playwright](https://img.shields.io/badge/Playwright-2EAD33?logo=playwright&logoColor=white)
![Telegram](https://img.shields.io/badge/Telegram-Bot%20API-26A5E4?logo=telegram&logoColor=white)
![Obsidian](https://img.shields.io/badge/Obsidian-note-7C3AED?logo=obsidian&logoColor=white)
![CSV](https://img.shields.io/badge/CSV-results-217346)

## Layout

| Path | What lives there |
| --- | --- |
| `cmd/prisjeger/` | The entrypoint. `main` calls `prisjeger.Main()`. |
| `internal/prisjeger/` | The service code: HTTP server, run tracker, search, offers, store, note, trust, notifications, and the web log. |
| `docs/` | [State machines](docs/STATE-MACHINES.md) and [todos](docs/TODOS.md). |
| `scripts/` | `start.sh` and `stop.sh`. |
| `tools/` | Node and Playwright helpers for the search, including the Amazon delivery check. |
| `.opencode/` | The `shopping-expert` skill. |

## Quick start

```sh
./scripts/start.sh                             # build and run detached
curl -X POST http://127.0.0.1:8080/search       # start a run
curl -s http://127.0.0.1:8080/status            # idle / running, counts, next run
```

> Needs `opencode` on `PATH` and the skill file present. Amazon checks need Node and Playwright (`npm install --prefix tools`).

## State machines

The service has 14 named state machines: the run lifecycle, item search, offer gates, stock, deal evidence, price status, persistence, price history, the Trustpilot cache, notifications, the scheduler, the Obsidian block, the CSV schema, and the service process. The run lifecycle carries the hard invariant: at most one run, and `total = active + done + failed`.

| Machine | States | Where |
| --- | --- | --- |
| Search run | `idle` → `running` → `idle` | [details](docs/STATE-MACHINES.md#1-search-run) |
| Notification | `disabled`, `ready`, `cooling`, `failed` | [details](docs/STATE-MACHINES.md#10-notification) |
| Obsidian block | `absent`, `closed`, `open` | [details](docs/STATE-MACHINES.md#12-obsidian-block) |

The full inventory, transitions, invariants, and the deliberately deferred machines are in [docs/STATE-MACHINES.md](docs/STATE-MACHINES.md).

---

## Start and stop

```sh
./scripts/start.sh
./scripts/stop.sh
```

`scripts/start.sh` builds `.run/prisjeger`, starts it detached, writes `.run/prisjeger.pid`, and appends to `.run/prisjeger.log`. It passes flags through to the server, so this works too.

```sh
./scripts/start.sh -addr 127.0.0.1:8080 -list handleliste.txt
```

`scripts/stop.sh` sends SIGTERM, waits up to 10 seconds, then escalates only if needed. `.run/` is gitignored.

## Start manually

```sh
go run ./cmd/prisjeger
curl -X POST http://127.0.0.1:8080/search
curl http://127.0.0.1:8080/status
curl http://127.0.0.1:8080/results
```

## Schedule

The service runs the full list once immediately after startup, then at 10:00 and 22:00 local time. At most one run is active at a time. A startup, scheduled, or manual start during a run is a no-op and returns the active run. Change the times with `-schedule` or `PRISJEGER_SCHEDULE`, for example `-schedule "09:30,21:30"`. An empty schedule disables timed runs but keeps the startup run.

## Search passes

1. Pass one runs `opencode run --format json --file <shopping-expert skill> <prompt>` for the item.
2. The server captures the OpenCode session ID. Pass two runs `opencode run --format json --session <id> <better-sales prompt>` in the same session.
3. Pass two asks, in spirit, "are there any better current sales than these offers I found?" It returns JSONL in the same schema.
4. The server puts pass two first, then pass one, deduplicates by URL, keeps the pass two version of a duplicate, and caps at 5.
5. If pass two fails, the item still succeeds with pass one.

If the first pass has no session ID, pass two starts a new session, attaches the skill, and includes the first offers in the prompt.

## Amazon delivery check

Amazon needs a real browser to set the delivery country to Norway and read the basket total. The checker is `tools/amazon-no.mjs`.

Install it once:

```sh
npm install --prefix tools
(cd tools && npx playwright install chromium)
```

Run it with the product URL and postal code:

```sh
node tools/amazon-no.mjs "https://www.amazon.de/dp/..." 6773
```

It prints JSON with `price_nok`, `shipping_nok`, `total_nok`, stock, seller, and rating. The skill tells the search agent to run it for every Amazon listing. If the script fails, the agent excludes the Amazon offer.

## Telegram notifications

The service can push the current results to a free Telegram bot after each full search run. Telegram accounts, bots, and the Bot API cost nothing.

1. Create a bot with [BotFather](https://t.me/BotFather) and copy the token it prints.
2. Start a chat with the new bot and send it any message.
3. Open [@userinfobot](https://t.me/userinfobot) and copy your chat ID.
4. Set both values in the environment of the service.

```sh
export PRISJEGER_TELEGRAM_TOKEN="123456:ABC-DEF"
export PRISJEGER_TELEGRAM_CHAT="123456789"
```

Pass `-telegram-token` and `-telegram-chat` instead when you start the binary with flags. Restart the service so it reads the values. The log shows `telegram: notifications enabled` when both are set. With either value empty, the notifier stays off and everything else works as before.

`scripts/start.sh` also loads `.run/telegram.env` when that file exists. Put the two exports there to keep the values across restarts without repeating them in every shell.

Test the setup against the running service:

```sh
curl -X POST http://127.0.0.1:8080/notify
```

The service sends the results after every search run finishes, including the startup run and the runs at 10:00 and 22:00. Automatic sends within 6 hours of the last successful one are suppressed, so a manual rerun close to a scheduled run does not send twice. A failed automatic send does not start the window, so the next completed run retries. `POST /notify` always sends and ignores the window; a successful manual send becomes the last send, so an automatic send seconds later is suppressed.

The message is plain text with one block per item and one line per offer, plus the product URL. It uses the Bot API's [sendMessage](https://core.telegram.org/bots/api#sendmessage) method with link previews disabled. [telegram.org](https://telegram.org/) describes the client apps.

## Output

One file, `results.csv`, with one header row and at most 5 rows per item. Each successful item search replaces that item's previous rows. A failed search leaves the old rows alone. Rows are sorted by `total_nok` ascending, cheapest first, and rows with an unknown total go last. A tie goes to the closer source region, then to the better Trustpilot score. Item groups are ordered by their cheapest total.

| Column | Meaning |
| --- | --- |
| `recorded_at` | Time of the item's latest search, RFC3339 local time. |
| `item` | Shopping-list item. |
| `product` | Brand and exact product name, for example `LG Kombiskap GBB-SJ20ESW Hvit`. Empty for rows written before this column existed. |
| `store` | Retailer name. |
| `store_region` | `no`, `se`, `eu`, `us`, or `other` for the store's home market. Derived from the URL when the search leaves it out. |
| `url` | Direct product page. |
| `price_nok` | Current price in NOK. |
| `shipping_nok` | Extra cost to Norway, including known shipping, VAT, import, and handling. `0` means free. `NaN` means unknown. |
| `total_nok` | `price_nok + shipping_nok` when shipping is known, otherwise `NaN`. |
| `ordinary_price_nok` | Store's genuine before-price or list price. Empty when unknown. |
| `sale_pct` | Percent off the ordinary price, one decimal. Empty when ordinary is unknown. |
| `history_low_nok` | Verified historical low. Empty when unknown. |
| `price_trend_nok` | Recent typical price from the price history. Empty when unknown. |
| `deal_check` | `at_low`, `below_trend`, `regular`, `unknown`, or `none` for a miss row. |
| `stock` | `in_stock`, `out_of_stock`, `preorder`, or `unknown`. |
| `stock_detail` | The store's own quantity or low-stock wording, for example `3 igjen` or `12 på lager`. Empty when the page shows none. |
| `rating` | Average product rating, 0 to 5, one decimal. Empty when absent. |
| `rating_count` | Number of ratings. Empty when absent. |
| `trustpilot_rating` | Store Trustpilot score, 0 to 5, one decimal. Empty when unknown. |
| `trustpilot_reviews` | Number of Trustpilot reviews. Empty when unknown. |
| `status` | `new`, `change`, or `same` against the previous row for the same item and URL; `none` for a miss row. |
| `price_change_nok` | Current price minus previous price, signed. Empty for `new`. |
| `price_change_pct` | Signed percent change, one decimal. Empty for `new`. |
| `change_direction` | `up`, `down`, `same`, or empty for `new`. |
| `note` | Human-readable note: the reason for a miss row, or the cheapest/sale comparison on a regular-price cheapest row. Empty otherwise. |

Money columns use two decimals. Percentages use one decimal. Empty cells mean unknown.

Status rules:

- `new` means the item and URL had no previous row.
- `same` means the price equals the previous row within 0.005 NOK.
- `change` means the price differs. The NOK and percent columns are signed, and `change_direction` says `up` or `down`.

Only the latest 5 rows per item are kept. A URL that drops out of the latest 5 loses its previous price, so if it returns later it can appear as `new`.

Prisjakt rows are kept only when the current price is at the historical low or below the recent trend. The server enforces this when the URL host contains `prisjakt.`.

The deal gate applies to slots 2-5. The cheapest in-stock offer is always kept, even when it has no before-price, low, or trend: a cheaper regular price must never lose to a more expensive sale. That one offer is shown with `deal_check` `regular` and a note such as `Cheapest overall, regular price. Best sale: Autohobby 299 kr (25.1% off), 152 kr more.` When the shipping rate is missing the note says `Cheapest listed price, regular; shipping unknown.` and qualifies the difference with `before shipping`. The other kept rows still need genuine discount evidence. An out-of-stock offer is not the cheapest buyable one, so the pin skips it, and a foreign offer without a landed cost is not compared.

## Obsidian note

The results table is written to:

```
/home/ajg/obsidian/August/prisjeger.md
```

The note is created if it does not exist. The server updates only the managed block, so other note content is preserved.

```markdown
<!-- prisjeger:start -->
...
<!-- prisjeger:end -->
```

The table has these columns: Item, Product, Price, Ship, Total, Ordinary, Sale, Low, Stock, Trustpilot, Rating, Status, Change, Deal, Region, Store, Link, Note. The store sits next to the link, and the cheapest total is first. The link opens the product page. A stock cell shows the state plus the page's own quantity or low-stock text, for example `in_stock (2 igjen)`, so a thin shelf is visible without opening the page. An item with no qualifying offer gets a `No offer found` row whose Note says why and, when known, the cheapest rejected price. The note is regenerated at startup and after each item search. Change the path with `-obsidian` or `PRISJEGER_OBSIDIAN`.

## Shopping expert skill

`.opencode/skills/shopping-expert/SKILL.md` defines the search strategy, the Norwegian, Swedish, EU, and US store tiers, the legitimacy screen, verification rules, the Prisjakt rule, the store trust rule, and the JSONL output schema. Pass one attaches it with `--file`. Pass two inherits it from the OpenCode session. The server re-checks the Trustpilot fields before it writes a row.

## Store trust

Every offer gets its store's Trustpilot score and review count. The domain comes from the product URL, so the seller named in `store` and the Trustpilot profile line up for normal shops. Marketplaces (Amazon, eBay, Finn) have no seller-level profile, so both fields stay empty there. Trustpilot blocks plain HTTP clients, so the lookup goes through the reader proxy `r.jina.ai`. A rating is cached for seven days in `.run/trustpilot.json`; a failed lookup leaves both fields empty and never fails the search.

The score is shown as a table column and used as the tie-breaker when two offers have the same landed cost: the better-rated store wins. `-min-trust 3.5` turns it into a hard filter. A missing profile is treated as unknown, not bad.

To refresh existing rows without a search:

```sh
./.run/prisjeger -backfill-trust
```

That rewrites `results.csv` and the Obsidian table with the current scores.

## Source priority

The skill searches in tiers: Norwegian and Swedish stores first, other EU/EEA stores second, and US/global stores only when the landed cost still beats the best closer offer. A US price is not a deal until shipping, import VAT, and handling survive.

`store_region` records the tier. It is taken from the search, or derived from the store's domain when missing. On equal landed cost the sort prefers `no` or `se`, then `eu`, then `us`, then `other`. Price always wins first, so the tier never turns a cheap offer into a worse one.

The skill also screens legitimacy: company identity and VAT/org number, a real return address, card/Vipps/Klarna payment, and a plausible Trustpilot history. A no-name store with a price far below the market, no address, or prepayment only is excluded rather than ranked.

## Shipping lookup

The search reads the store's shipping estimator with the buyer's postal code (6773) and uses the cheapest **home-delivery** option, not a pickup point. Shopify stores expose the rate through the cart API; Magento stores often only show it at checkout. When the rate is genuinely unavailable, `shipping_nok` and `total_nok` are written as `NaN` rather than left blank, so an unknown cost is obvious and sorts last.

## Web URL log

`websearch.log` gets one URL per line, appended live as opencode works: the pages it fetches, the result URLs from its web searches, and URLs inside its shell commands (it often uses `curl` instead of the fetch tool). Search queries have no URL, so the log shows the sites the search found rather than the query text. Change the path with `-web-log` or `PRISJEGER_WEB_LOG`; an empty path disables it.

## Stock changes

Every scheduled run searches each item again. A `No offer found` row is replaced when an offer appears, and an offer's stock, price, and Trustpilot score are refreshed. A `No offer found` row links to the cheapest rejected offer when there is one, so you can check that page again later.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/search` | Start fresh searches for every list item. Returns `202` immediately with `started`, `run_id`, `state`, and `items`. Returns `started:false` when a run is already active. |
| `POST` | `/notify` | Send the current results to Telegram now. Returns `400` when Telegram is not configured. |
| `GET` | `/status` | Run state, current counts, next scheduled run, notification state, CSV path. |
| `GET` | `/results` | The accumulated CSV file. |
| `GET` | `/healthz` | `ok`. |
| `GET` | `/` | Short usage text. |

`GET /status` keeps the old top-level `active`, `done`, `failed`, and `last_error` fields and adds `state`, `run`, `next_run_at`, and `notify`.

```json
{
  "state": "running",
  "run": {
    "id": 3,
    "source": "search",
    "state": "running",
    "started_at": "2026-11-11T10:00:00+01:00",
    "total": 12,
    "active": 3,
    "done": 9,
    "failed": 0,
    "last_error": ""
  },
  "next_run_at": "2026-11-11T22:00:00+01:00",
  "notify": {"enabled": true, "last_sent_at": "2026-11-11T10:04:00+01:00"},
  "active": 3,
  "done": 9,
  "failed": 0,
  "last_error": "",
  "csv_file": "results.csv"
}
```

## Configuration

Flags, with environment fallbacks.

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `-addr` | `PRISJEGER_ADDR` | `127.0.0.1:8080` | Listen address. |
| `-list` | `PRISJEGER_LIST` | `handleliste.txt` | Shopping list file. |
| `-csv` | `PRISJEGER_CSV` | `results.csv` | The one output CSV file. |
| `-obsidian` | `PRISJEGER_OBSIDIAN` | `/home/ajg/obsidian/August/prisjeger.md` | Obsidian note with the managed table. |
| `-schedule` | `PRISJEGER_SCHEDULE` | `10:00,22:00` | Local times for the daily runs. Empty disables them. |
| `-skill` | `PRISJEGER_SKILL` | `.opencode/skills/shopping-expert/SKILL.md` | Skill attached to pass one. |
| `-opencode` | `PRISJEGER_OPENCODE` | `opencode` | OpenCode binary. |
| `-model` | `PRISJEGER_MODEL` | host default | Passed as `--model` when set. |
| `-timeout` | `PRISJEGER_TIMEOUT` | `20m` | Timeout per pass. The first search and the follow-up each get this much. |
| `-parallel` | `PRISJEGER_PARALLEL` | `3` | Max concurrent OpenCode processes. |
| `-trust-cache` | `PRISJEGER_TRUST_CACHE` | `.run/trustpilot.json` | Trustpilot rating cache. |
| `-min-trust` | `PRISJEGER_MIN_TRUST` | `0` | Drop stores rated below this Trustpilot score. `0` keeps every store. |
| `-require-deal` | `PRISJEGER_REQUIRE_DEAL` | `true` | Keep only genuine deals in slots 2-5. The cheapest in-stock offer is always shown. |
| `-web-log` | `PRISJEGER_WEB_LOG` | `websearch.log` | Append every fetched and searched URL here. Empty disables it. |
| `-postal-code` | `PRISJEGER_POSTAL_CODE` | `6773` | Buyer postal code used for every shipping estimate. |
| `-telegram-token` | `PRISJEGER_TELEGRAM_TOKEN` | empty | Telegram bot token. Empty disables notifications. |
| `-telegram-chat` | `PRISJEGER_TELEGRAM_CHAT` | empty | Telegram chat ID that receives the results. |

Prices are normalized to NOK. Run one server instance at a time.

The old `results/*.csv` files from the first version are legacy. They are not imported. `docs/TODOS.md` tracks the remaining work.

## License and contact

No license is declared in this repository. Treat it as all rights reserved until one is added.

Maintainer: [@0xViva](https://github.com/0xViva).
