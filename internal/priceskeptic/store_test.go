package prisjeger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplaceWithoutOffersRecordsMiss(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "results.csv")
	notePath := filepath.Join(dir, "note.md")

	st, err := openResultStore(csvPath, notePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	written, err := st.Replace("widget", nil)
	if err != nil {
		t.Fatal(err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0 offers", written)
	}

	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := loadRows(data)
	if err != nil {
		t.Fatal(err)
	}
	list := rows["widget"]
	if len(list) != 1 {
		t.Fatalf("rows = %d, want 1 miss row", len(list))
	}
	row := list[0]
	if row.product != "No offer found" {
		t.Errorf("product = %q", row.product)
	}
	if row.note == "" || !strings.Contains(strings.ToLower(row.note), "criteria") {
		t.Errorf("note = %q, want the criteria message", row.note)
	}
	if row.dealCheck != "none" || row.status != "none" {
		t.Errorf("deal_check/status = %q/%q, want none/none", row.dealCheck, row.status)
	}
	if row.price != "" || row.url != "" {
		t.Errorf("miss row has price/url: %q/%q", row.price, row.url)
	}

	note, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "No offer found") || !strings.Contains(string(note), "criteria") {
		t.Errorf("note does not show the miss:\n%s", note)
	}
}

func goldenRows() map[string][]storedRow {
	return map[string][]storedRow{
		"filter": {
			{
				item: "filter", product: "PURFLUX AHC736-2", store: "Mekster", storeRegion: "no",
				url: "https://mekster.no/p", price: "147.00", shipping: "NaN", total: "NaN",
				stock: "in_stock", stockDetail: "2 igjen", trustRating: "4.4", trustCount: "578",
				rating: "4.8", ratingCount: "218", status: "same", dealCheck: "regular",
				note: "Cheapest overall, regular price.",
			},
			{
				item: "filter", product: "Kupéfilter", store: "Autohobby", storeRegion: "no",
				url: "https://autohobby.no/p", price: "299.00", shipping: "0.00", total: "299.00",
				ordinary: "399.00", salePct: "25.1", historyLow: "279.00", priceTrend: "265.00",
				stock: "in_stock", trustRating: "4.5", trustCount: "866", status: "new",
				dealCheck: "at_low",
			},
		},
		"widget": {
			{
				item: "widget", product: "Widget", store: "Store", storeRegion: "se",
				url: "https://example.se/p", price: "100.00", shipping: "0.00", total: "100.00",
				stock: "out_of_stock", status: "change", direction: "down",
				changeNOK: "-10.00", changePct: "-9.1", dealCheck: "below_trend",
			},
		},
	}
}

func TestNoteBlockGolden(t *testing.T) {
	got := noteBlock(goldenRows(), time.Date(2026, 11, 10, 12, 34, 0, 0, time.UTC))
	want := `<!-- prisjeger:start -->
_Updated 2026-11-10 12:34. Source: results.csv._

| Item | Product | Price | Ship | Total | Ordinary | Sale | Low | Stock | Trustpilot | Rating | Status | Change | Deal | Region | Store | Link | Note |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| widget | Widget | 100 | 0 | 100 |  |  |  | out_of_stock |  |  | change | down -10.00 (-9.1%) | below_trend | SE | Store | [open](<https://example.se/p>) |  |
| filter | Kupéfilter | 299 | 0 | 299 | 399 | 25.1% | 279 | in_stock | 4.5 (866) |  | new |  | at_low | NO | Autohobby | [open](<https://autohobby.no/p>) |  |
| filter | PURFLUX AHC736-2 | 147 | NaN | NaN |  |  |  | in_stock (2 igjen) | 4.4 (578) | 4.8 (218) | same | same | regular | NO | Mekster | [open](<https://mekster.no/p>) | Cheapest overall, regular price. |
<!-- prisjeger:end -->`
	if got != want {
		t.Fatalf("note block changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCSVGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	if err := writeCSV(path, goldenRows()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `recorded_at,item,product,store,store_region,url,price_nok,shipping_nok,total_nok,ordinary_price_nok,sale_pct,history_low_nok,price_trend_nok,deal_check,stock,stock_detail,rating,rating_count,trustpilot_rating,trustpilot_reviews,status,price_change_nok,price_change_pct,change_direction,note
,widget,Widget,Store,se,https://example.se/p,100.00,0.00,100.00,,,,,below_trend,out_of_stock,,,,,,change,-10.00,-9.1,down,
,filter,Kupéfilter,Autohobby,no,https://autohobby.no/p,299.00,0.00,299.00,399.00,25.1,279.00,265.00,at_low,in_stock,,,,4.5,866,new,,,,
,filter,PURFLUX AHC736-2,Mekster,no,https://mekster.no/p,147.00,NaN,NaN,,,,,regular,in_stock,2 igjen,4.8,218,4.4,578,same,,,,"Cheapest overall, regular price."
`
	if string(got) != want {
		t.Fatalf("CSV changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDiffPrice(t *testing.T) {
	cases := []struct {
		name    string
		prev    float64
		hasPrev bool
		current float64
		want    priceChange
	}{
		{"new", 0, false, 100, priceChange{Status: priceNew}},
		{"same", 100, true, 100.004, priceChange{Status: priceSame, NOK: "0.00", Pct: "0.0", Direction: directionSame}},
		{"up", 100, true, 110, priceChange{Status: priceChanged, NOK: "10.00", Pct: "10.0", Direction: directionUp}},
		{"down", 100, true, 90, priceChange{Status: priceChanged, NOK: "-10.00", Pct: "-10.0", Direction: directionDown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffPrice(tc.prev, tc.hasPrev, tc.current); got != tc.want {
				t.Fatalf("diffPrice(%v, %v, %v) = %+v, want %+v", tc.prev, tc.hasPrev, tc.current, got, tc.want)
			}
		})
	}
}

func TestPruneItemsDropsItemsRemovedFromList(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "results.csv")
	notePath := filepath.Join(dir, "note.md")

	st, err := openResultStore(csvPath, notePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, item := range []string{"keep", "drop"} {
		_, err := st.Replace(item, []offer{{
			Product: item + " product", Store: "Store", StoreRegion: "no",
			URL: "https://example.no/" + item, Price: 100, Stock: "in_stock",
		}})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := st.PruneItems([]string{"keep"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := loadRows(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["drop"]; ok {
		t.Fatal("pruned item is still in the CSV")
	}
	if len(rows["keep"]) != 1 {
		t.Fatalf("kept rows = %d, want 1", len(rows["keep"]))
	}

	note, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(note), "drop product") {
		t.Fatalf("pruned item is still in the note:\n%s", note)
	}

	if err := st.PruneItems(nil); err != nil {
		t.Fatal(err)
	}
	if !st.HasRows("keep") {
		t.Fatal("empty list should not prune the remaining rows")
	}
}

func TestLoadRowsAcceptsOldHeader(t *testing.T) {
	old := "recorded_at,item,product,store,url,price_nok,shipping_nok,total_nok,ordinary_price_nok,sale_pct,history_low_nok,price_trend_nok,deal_check,stock,rating,rating_count,status,price_change_nok,price_change_pct,change_direction\n" +
		"2026-01-01T00:00:00+01:00,widget,Widget,Store,https://example.no/p,100.00,0.00,100.00,200.00,50.0,,,at_low,in_stock,4.5,10,new,,,\n"
	rows, err := loadRows([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	list := rows["widget"]
	if len(list) != 1 {
		t.Fatalf("rows = %d, want 1", len(list))
	}
	if list[0].trustRating != "" || list[0].trustCount != "" || list[0].note != "" {
		t.Errorf("new columns should default empty: %+v", list[0])
	}
}

func TestCheapestCandidateSkipsOutOfStock(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "A", URL: "https://a.no/p", Price: price(300), Stock: "in_stock"},
		{Store: "B", URL: "https://b.no/p", Price: price(200), Stock: "out_of_stock"},
		{Store: "C", URL: "https://c.no/p", Price: price(250), Shipping: price(10), Stock: "in_stock"},
		{Store: "Bad", URL: "not-a-url", Price: price(1), Stock: "in_stock"},
	}
	got, ok := cheapestCandidate(raw)
	if !ok {
		t.Fatal("cheapestCandidate found nothing")
	}
	if got.Store != "C" {
		t.Fatalf("store = %q, want C (lowest landed cost)", got.Store)
	}
}

func TestCheapestCandidateRequiresLandedCost(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Foreign", URL: "https://example.com/p", Price: price(100), Stock: "in_stock"},
		{Store: "Domestic", URL: "https://example.no/p", Price: price(200), Shipping: price(50), Stock: "in_stock"},
	}
	got, ok := cheapestCandidate(raw)
	if !ok {
		t.Fatal("cheapestCandidate found nothing")
	}
	if got.Store != "Domestic" {
		t.Fatalf("store = %q, want Domestic (foreign offer has no landed cost)", got.Store)
	}
	if total := offerCost(got); total != 250 {
		t.Fatalf("landed cost = %.2f, want 250", total)
	}
}

func TestCheapestCandidateAcceptsForeignOfferWithLandedCost(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Foreign", URL: "https://example.com/p", Price: price(100), Shipping: price(50), Stock: "in_stock"},
		{Store: "Domestic", URL: "https://example.no/p", Price: price(200), Shipping: price(0), Stock: "in_stock"},
	}
	got, ok := cheapestCandidate(raw)
	if !ok {
		t.Fatal("cheapestCandidate found nothing")
	}
	if got.Store != "Foreign" {
		t.Fatalf("store = %q, want Foreign (known landed cost 150)", got.Store)
	}
	if total := offerCost(got); total != 150 {
		t.Fatalf("landed cost = %.2f, want 150", total)
	}
}

func TestMissRowUsesBuyableCandidate(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Foreign", URL: "https://example.com/p", Price: price(100), Stock: "in_stock"},
		{Store: "Domestic", URL: "https://example.no/p", Price: price(200), Shipping: price(50), Stock: "in_stock"},
	}
	cheap, ok := cheapestCandidate(raw)
	if !ok {
		t.Fatal("cheapestCandidate found nothing")
	}
	row := noOfferRow("item", "2026-01-01T00:00:00+01:00", "Cheapest seen", &cheap)
	if row.url != "https://example.no/p" {
		t.Fatalf("miss row URL = %q, want the buyable domestic offer", row.url)
	}
}

func TestNoOfferRowCarriesCandidateURL(t *testing.T) {
	candidate := offer{
		Store:       "Autohobby",
		StoreRegion: "no",
		URL:         "https://www.autohobby.no/products/x",
	}
	row := noOfferRow("item", "2026-01-01T00:00:00+01:00", "no discount", &candidate)
	if row.product != "No offer found" {
		t.Fatalf("product = %q", row.product)
	}
	if row.store != candidate.Store || row.storeRegion != candidate.StoreRegion || row.url != candidate.URL {
		t.Fatalf("candidate fields not copied: %+v", row)
	}
	if row.price != "" || row.total != "" {
		t.Fatalf("miss row should not carry a price: %+v", row)
	}
}

func TestUnknownShippingIsNaN(t *testing.T) {
	row := newStoredRow("item", "2026-01-01T00:00:00+01:00", offer{
		Product: "X", Store: "S", URL: "https://example.no/p", Price: 100,
	}, 0, false)
	if row.shipping != "NaN" || row.total != "NaN" {
		t.Fatalf("shipping/total = %q/%q, want NaN/NaN", row.shipping, row.total)
	}
	if _, ok := rowTotal(row); ok {
		t.Fatal("rowTotal treated NaN as a known total")
	}
}

func TestKnownShippingTotals(t *testing.T) {
	shipping := 67.0
	row := newStoredRow("item", "2026-01-01T00:00:00+01:00", offer{
		Product: "X", Store: "S", URL: "https://example.no/p", Price: 299, Shipping: &shipping,
	}, 0, false)
	if row.shipping != "67.00" || row.total != "366.00" {
		t.Fatalf("shipping/total = %q/%q, want 67.00/366.00", row.shipping, row.total)
	}
}

func TestCheapestRegularBeatsMoreExpensiveSale(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Autohobby", URL: "https://autohobby.no/p", Price: price(299), Ordinary: price(399), Stock: "in_stock"},
		{Store: "Mekster", URL: "https://mekster.no/p", Price: price(147), Stock: "in_stock"},
	}
	kept, _, _ := normalizeOffers("filter", raw, true)
	if !hasURL(kept, "https://mekster.no/p") {
		t.Fatalf("cheapest regular offer was dropped: %+v", kept)
	}
	if !hasURL(kept, "https://autohobby.no/p") {
		t.Fatalf("the genuine sale was dropped too: %+v", kept)
	}
}

func TestDealGateStillDropsOtherRegulars(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Cheap", URL: "https://cheap.no/p", Price: price(147), Stock: "in_stock"},
		{Store: "Middle", URL: "https://middle.no/p", Price: price(250), Stock: "in_stock"},
		{Store: "Sale", URL: "https://sale.no/p", Price: price(299), Ordinary: price(399), Stock: "in_stock"},
	}
	kept, _, _ := normalizeOffers("filter", raw, true)
	if !hasURL(kept, "https://cheap.no/p") || !hasURL(kept, "https://sale.no/p") {
		t.Fatalf("kept = %+v", kept)
	}
	if hasURL(kept, "https://middle.no/p") {
		t.Fatalf("a regular offer above the cheapest deal should stay out: %+v", kept)
	}
}

func TestCheapestPinSkipsOutOfStock(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "Gone", URL: "https://gone.no/p", Price: price(50), Stock: "out_of_stock"},
		{Store: "Buyable", URL: "https://buyable.no/p", Price: price(180), Stock: "in_stock"},
		{Store: "Sale", URL: "https://sale.no/p", Price: price(299), Ordinary: price(399), Stock: "in_stock"},
	}
	kept, _, _ := normalizeOffers("filter", raw, true)
	if hasURL(kept, "https://gone.no/p") {
		t.Fatalf("out-of-stock offer should not be pinned: %+v", kept)
	}
	if !hasURL(kept, "https://buyable.no/p") {
		t.Fatalf("cheapest in-stock offer was dropped: %+v", kept)
	}
}

func TestCheapestPinRespectsLandingCost(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	raw := []rawOffer{
		{Store: "CheapItem", URL: "https://a.no/p", Price: price(100), Shipping: price(300), Stock: "in_stock"},
		{Store: "Total", URL: "https://b.no/p", Price: price(320), Shipping: price(0), Stock: "in_stock"},
		{Store: "Sale", URL: "https://c.no/p", Price: price(500), Ordinary: price(700), Stock: "in_stock"},
	}
	kept, _, _ := normalizeOffers("filter", raw, true)
	if hasURL(kept, "https://a.no/p") {
		t.Fatalf("pinned an offer that loses on landed cost: %+v", kept)
	}
	if !hasURL(kept, "https://b.no/p") {
		t.Fatalf("cheapest landed total was dropped: %+v", kept)
	}
}

func TestCheapestPinSurvivesCap(t *testing.T) {
	price := func(v float64) flexFloat { return flexFloat{set: true, val: v} }
	var raw []rawOffer
	for i := 0; i < maxOffersPerItem; i++ {
		raw = append(raw, rawOffer{
			Store: fmt.Sprintf("Sale%d", i), URL: fmt.Sprintf("https://sale%d.no/p", i),
			Price: price(float64(300 + i)), Ordinary: price(float64(400 + i)), Stock: "in_stock",
		})
	}
	raw = append(raw, rawOffer{Store: "Cheap", URL: "https://cheap.no/p", Price: price(99), Stock: "in_stock"})

	kept, _, capped := normalizeOffers("filter", raw, true)
	if len(kept) != maxOffersPerItem {
		t.Fatalf("kept = %d offers, want %d", len(kept), maxOffersPerItem)
	}
	if capped != 1 {
		t.Fatalf("capped = %d, want 1", capped)
	}
	if !hasURL(kept, "https://cheap.no/p") {
		t.Fatalf("the cap cut the pinned cheapest offer: %+v", kept)
	}
}

func hasURL(offers []offer, want string) bool {
	for _, o := range offers {
		if o.URL == want {
			return true
		}
	}
	return false
}

func TestSplitStock(t *testing.T) {
	cases := []struct {
		in     string
		stock  string
		detail string
	}{
		{"in_stock (3 left)", "in_stock", "3 left"},
		{"på lager, 12 stk", "in_stock", "12 stk"},
		{"in stock - kun 2 igjen", "in_stock", "kun 2 igjen"},
		{"utsolgt", "out_of_stock", ""},
		{"preorder", "preorder", ""},
		{"12", "in_stock", "12"},
		{"0", "out_of_stock", "0"},
		{"", "unknown", ""},
		{"3 left", "unknown", "3 left"},
	}
	for _, tc := range cases {
		stock, detail := splitStock(tc.in)
		if stock != tc.stock || detail != tc.detail {
			t.Errorf("splitStock(%q) = %q/%q, want %q/%q", tc.in, stock, detail, tc.stock, tc.detail)
		}
	}
}

func TestStockCell(t *testing.T) {
	cases := []struct {
		stock, detail, want string
	}{
		{"in_stock", "2 igjen", "in_stock (2 igjen)"},
		{"in_stock", "", "in_stock"},
		{"unknown", "3 left", "3 left"},
		{"unknown", "", "unknown"},
	}
	for _, tc := range cases {
		if got := stockCell(tc.stock, tc.detail); got != tc.want {
			t.Errorf("stockCell(%q, %q) = %q, want %q", tc.stock, tc.detail, got, tc.want)
		}
	}
}

func TestCheapestNoteNamesBestSale(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cheap := offer{Store: "Mekster", StoreRegion: "no", URL: "https://mekster.no/p", Price: 147, Stock: "in_stock"}
	sale := offer{Store: "Autohobby", StoreRegion: "no", URL: "https://autohobby.no/p", Price: 299, Ordinary: f(399), Stock: "in_stock"}

	url, note := cheapestNote([]offer{sale, cheap})
	if url != cheap.URL {
		t.Fatalf("note attached to %q, want %q", url, cheap.URL)
	}
	for _, want := range []string{"Cheapest listed price", "Best sale: Autohobby 299", "25.1% off", "152 kr more", "before shipping"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q is missing %q", note, want)
		}
	}

	zero := 0.0
	cheap.Shipping = &zero
	sale.Shipping = &zero
	_, note = cheapestNote([]offer{sale, cheap})
	for _, want := range []string{"Cheapest overall, regular price", "Best sale: Autohobby 299"} {
		if !strings.Contains(note, want) {
			t.Errorf("known-shipping note %q is missing %q", note, want)
		}
	}
	if strings.Contains(note, "before shipping") {
		t.Errorf("known-shipping note should not qualify the comparison: %q", note)
	}

	if url, note := cheapestNote([]offer{sale}); url != "" || note != "" {
		t.Fatalf("a discounted cheapest row should get no note, got %q/%q", url, note)
	}
	if _, note := cheapestNote([]offer{cheap}); !strings.Contains(note, "Cheapest overall, regular price") {
		t.Fatalf("note without a sale = %q", note)
	}
}

func TestStoreRoundTripsStockDetail(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "results.csv")
	notePath := filepath.Join(dir, "note.md")

	st, err := openResultStore(csvPath, notePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ordinary := 399.0
	_, err = st.Replace("filter", []offer{
		{
			Product: "PURFLUX AHC736-2", Store: "Mekster", StoreRegion: "no",
			URL: "https://mekster.no/p", Price: 147, Stock: "in_stock", StockDetail: "2 igjen",
		},
		{
			Product: "Kupéfilter", Store: "Autohobby", StoreRegion: "no",
			URL: "https://autohobby.no/p", Price: 299, Ordinary: &ordinary, Stock: "in_stock",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := loadRows(data)
	if err != nil {
		t.Fatal(err)
	}
	row := rows["filter"][0]
	if row.stock != "in_stock" || row.stockDetail != "2 igjen" {
		t.Fatalf("stock/detail = %q/%q", row.stock, row.stockDetail)
	}
	if !strings.Contains(row.note, "Best sale: Autohobby") {
		t.Fatalf("cheapest row note = %q", row.note)
	}

	note, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(note), "in_stock (2 igjen)") {
		t.Fatalf("note does not show the stock detail:\n%s", note)
	}
}

func TestParseOffersAcceptsNumericStock(t *testing.T) {
	raw := parseOffers(`{"store":"S","url":"https://example.no/p","price_nok":100,"stock":12}`)
	if len(raw) != 1 {
		t.Fatalf("parsed %d offers, want 1", len(raw))
	}
	o, ok := toOffer(raw[0])
	if !ok {
		t.Fatal("toOffer rejected the offer")
	}
	if o.Stock != "in_stock" || o.StockDetail != "12" {
		t.Fatalf("stock/detail = %q/%q", o.Stock, o.StockDetail)
	}

	raw = parseOffers(`{"store":"S","url":"https://example.no/p","price_nok":100,"stock":"in_stock","stock_detail":3}`)
	o, ok = toOffer(raw[0])
	if !ok || o.Stock != "in_stock" || o.StockDetail != "3" {
		t.Fatalf("stock/detail = %q/%q, ok=%v", o.Stock, o.StockDetail, ok)
	}
}

func TestToOfferStockFields(t *testing.T) {
	price := flexFloat{set: true, val: 100}
	raw := rawOffer{
		Store: "S", URL: "https://example.no/p", Price: price,
		Stock: "på lager, 7 stk", StockDetail: "kun 7 igjen",
	}
	o, ok := toOffer(raw)
	if !ok {
		t.Fatal("toOffer rejected the offer")
	}
	if o.Stock != "in_stock" || o.StockDetail != "kun 7 igjen" {
		t.Fatalf("stock/detail = %q/%q", o.Stock, o.StockDetail)
	}

	raw.Stock = "in_stock"
	raw.StockDetail = ""
	raw.StockQuantity = flexFloat{set: true, val: 4}
	o, ok = toOffer(raw)
	if !ok || o.StockDetail != "4" {
		t.Fatalf("quantity detail = %q, ok=%v", o.StockDetail, ok)
	}
}
