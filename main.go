package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxOffersPerItem = 5

var csvHeader = []string{
	"recorded_at",
	"item",
	"product",
	"store",
	"store_region",
	"url",
	"price_nok",
	"shipping_nok",
	"total_nok",
	"ordinary_price_nok",
	"sale_pct",
	"history_low_nok",
	"price_trend_nok",
	"deal_check",
	"stock",
	"stock_detail",
	"rating",
	"rating_count",
	"trustpilot_rating",
	"trustpilot_reviews",
	"status",
	"price_change_nok",
	"price_change_pct",
	"change_direction",
	"note",
}

type flexFloat struct {
	set bool
	val float64
}

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = flexFloat{}
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			*f = flexFloat{}
			return nil
		}
		v, err := parseNumeric(str)
		if err != nil {
			*f = flexFloat{}
			return nil
		}
		*f = flexFloat{set: true, val: v}
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		*f = flexFloat{}
		return nil
	}
	*f = flexFloat{set: true, val: v}
	return nil
}

func parseNumeric(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\u00a0", "")
	s = strings.ReplaceAll(s, " ", "")
	for _, prefix := range []string{"NOK", "nok", "kr"} {
		s = strings.TrimPrefix(s, prefix)
	}
	for _, suffix := range []string{"NOK", "nok", "kr", ",-"} {
		s = strings.TrimSuffix(s, suffix)
	}
	if strings.Contains(s, ",") && strings.Contains(s, ".") {
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	} else {
		s = strings.ReplaceAll(s, ",", ".")
	}
	return strconv.ParseFloat(s, 64)
}

type flexString string

// UnmarshalJSON accepts a string or a bare number, so a model that writes
// `"stock": 12` or `"stock_detail": 3` does not poison the whole offer line.
func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			*f = ""
			return nil
		}
		*f = flexString(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		*f = ""
		return nil
	}
	*f = flexString(n.String())
	return nil
}

type rawOffer struct {
	Product       string     `json:"product"`
	Store         string     `json:"store"`
	StoreRegion   string     `json:"store_region"`
	URL           string     `json:"url"`
	Price         flexFloat  `json:"price_nok"`
	Shipping      flexFloat  `json:"shipping_nok"`
	Ordinary      flexFloat  `json:"ordinary_price_nok"`
	HistoryLow    flexFloat  `json:"history_low_nok"`
	PriceTrend    flexFloat  `json:"price_trend_nok"`
	DealCheck     string     `json:"deal_check"`
	Stock         flexString `json:"stock"`
	StockDetail   flexString `json:"stock_detail"`
	StockQuantity flexFloat  `json:"stock_quantity"`
	Rating        flexFloat  `json:"rating"`
	RatingCount   flexFloat  `json:"rating_count"`
	TrustRating   flexFloat  `json:"trustpilot_rating"`
	TrustCount    flexFloat  `json:"trustpilot_reviews"`
}

type offer struct {
	Product     string
	Store       string
	StoreRegion string
	URL         string
	Price       float64
	Shipping    *float64
	Ordinary    *float64
	HistoryLow  *float64
	PriceTrend  *float64
	DealCheck   string
	Stock       string
	StockDetail string
	Rating      *float64
	RatingCount *int
	TrustRating *float64
	TrustCount  *int
	prisjakt    bool
}

func toOffer(r rawOffer) (offer, bool) {
	store := strings.TrimSpace(r.Store)
	if store == "" {
		return offer{}, false
	}
	rawURL := strings.TrimSpace(r.URL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return offer{}, false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return offer{}, false
	}
	if !r.Price.set || !finite(r.Price.val) || r.Price.val <= 0 {
		return offer{}, false
	}
	for _, f := range []flexFloat{r.Shipping, r.Ordinary, r.HistoryLow, r.PriceTrend} {
		if f.set && (!finite(f.val) || f.val < 0) {
			return offer{}, false
		}
	}
	if r.Rating.set && (!finite(r.Rating.val) || r.Rating.val < 0 || r.Rating.val > 5) {
		return offer{}, false
	}
	if r.RatingCount.set && (!finite(r.RatingCount.val) || r.RatingCount.val < 0) {
		return offer{}, false
	}
	if r.TrustRating.set && (!finite(r.TrustRating.val) || r.TrustRating.val < 0 || r.TrustRating.val > 5) {
		return offer{}, false
	}
	if r.TrustCount.set && (!finite(r.TrustCount.val) || r.TrustCount.val < 0) {
		return offer{}, false
	}
	o := offer{
		Product:     strings.Join(strings.Fields(r.Product), " "),
		Store:       store,
		StoreRegion: storeRegion(rawURL, r.StoreRegion),
		URL:         rawURL,
		Price:       r.Price.val,
		DealCheck:   normalizeEnum(r.DealCheck, dealChecks),
		prisjakt:    strings.Contains(strings.ToLower(u.Hostname()), "prisjakt."),
	}
	o.Stock, o.StockDetail = splitStock(string(r.Stock))
	if extra := cleanStockDetail(string(r.StockDetail)); extra != "" {
		o.StockDetail = extra
	} else if o.StockDetail == "" && r.StockQuantity.set && finite(r.StockQuantity.val) && r.StockQuantity.val >= 0 {
		n := int(math.Round(r.StockQuantity.val))
		o.StockDetail = strconv.Itoa(n)
		if o.Stock == "unknown" {
			if n == 0 {
				o.Stock = "out_of_stock"
			} else {
				o.Stock = "in_stock"
			}
		}
	}
	o.Shipping = floatPtr(r.Shipping)
	o.Ordinary = floatPtr(r.Ordinary)
	o.HistoryLow = floatPtr(r.HistoryLow)
	o.PriceTrend = floatPtr(r.PriceTrend)
	o.Rating = floatPtr(r.Rating)
	if r.RatingCount.set {
		n := int(math.Round(r.RatingCount.val))
		o.RatingCount = &n
	}
	o.TrustRating = floatPtr(r.TrustRating)
	if r.TrustCount.set {
		n := int(math.Round(r.TrustCount.val))
		o.TrustCount = &n
	}
	return o, true
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func floatPtr(f flexFloat) *float64 {
	if !f.set {
		return nil
	}
	v := f.val
	return &v
}

var dealChecks = map[string]bool{
	"at_low":      true,
	"below_trend": true,
	"regular":     true,
	"unknown":     true,
	"none":        true,
}

func normalizeEnum(v string, allowed map[string]bool) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	if allowed[s] {
		return s
	}
	return "unknown"
}

// stockAliases accepts the enum plus the wording a Norwegian store page
// usually shows, so a model that copies the page text still produces a usable
// stock value.
var stockAliases = map[string]string{
	"in stock":           "in_stock",
	"instock":            "in_stock",
	"in_stock":           "in_stock",
	"på lager":           "in_stock",
	"pa lager":           "in_stock",
	"out of stock":       "out_of_stock",
	"outofstock":         "out_of_stock",
	"out_of_stock":       "out_of_stock",
	"ikke på lager":      "out_of_stock",
	"ikke pa lager":      "out_of_stock",
	"utsolgt":            "out_of_stock",
	"solgt ut":           "out_of_stock",
	"preorder":           "preorder",
	"pre order":          "preorder",
	"pre-order":          "preorder",
	"forhåndsbestilling": "preorder",
	"forhandsbestilling": "preorder",
	"forhåndssalg":       "preorder",
	"unknown":            "unknown",
	"ukjent":             "unknown",
}

// splitStock separates the canonical stock state from the page's own quantity
// or low-stock text. Model output such as "in_stock (3 left)", "på lager, 12
// stk", or a bare count all become a known enum plus a display detail.
func splitStock(v string) (string, string) {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return "unknown", ""
	}
	if n, err := strconv.Atoi(raw); err == nil {
		if n <= 0 {
			return "out_of_stock", "0"
		}
		return "in_stock", strconv.Itoa(n)
	}
	lower := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	if enum, ok := stockEnum(lower); ok {
		return enum, ""
	}
	for _, sep := range []string{"(", "[", ":", ",", ";", " - ", " – ", " — "} {
		if i := strings.Index(lower, sep); i > 0 {
			if enum, ok := stockEnum(strings.TrimSpace(lower[:i])); ok {
				return enum, cleanStockDetail(raw[i:])
			}
		}
	}
	words := strings.Fields(lower)
	for n := 1; n <= 3 && n < len(words); n++ {
		if enum, ok := stockEnum(strings.Join(words[:n], " ")); ok {
			return enum, cleanStockDetail(strings.Join(strings.Fields(raw)[n:], " "))
		}
	}
	return "unknown", cleanStockDetail(raw)
}

func stockEnum(v string) (string, bool) {
	s := strings.ToLower(strings.Join(strings.Fields(v), " "))
	if enum, ok := stockAliases[s]; ok {
		return enum, true
	}
	s = strings.ReplaceAll(s, "_", " ")
	if enum, ok := stockAliases[s]; ok {
		return enum, true
	}
	return "", false
}

// cleanStockDetail keeps the page's wording but removes wrappers and caps the
// length so one verbose model cannot widen the Obsidian table.
func cleanStockDetail(v string) string {
	s := strings.TrimSpace(v)
	s = strings.Trim(s, "()[]{}:;,.-–—")
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, 48)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func parseOffers(text string) []rawOffer {
	var clean []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		clean = append(clean, line)
	}
	if arr, ok := decodeOfferArray(strings.Join(clean, "\n")); ok {
		return arr
	}
	var out []rawOffer
	for _, line := range clean {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if arr, ok := decodeOfferArray(line); ok {
			out = append(out, arr...)
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		for {
			var r rawOffer
			if err := dec.Decode(&r); err != nil {
				break
			}
			out = append(out, r)
		}
	}
	return out
}

func decodeOfferArray(s string) ([]rawOffer, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	var arr []rawOffer
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	return arr, true
}

func normalizeOffers(item string, raw []rawOffer, requireDeal bool) (kept []offer, dropped, capped int) {
	candidates := make([]offer, 0, len(raw))
	for _, r := range raw {
		o, ok := toOffer(r)
		if !ok {
			dropped++
			continue
		}
		if o.prisjakt && o.DealCheck != "at_low" && o.DealCheck != "below_trend" {
			dropped++
			log.Printf("item %q: drop prisjakt offer %s (deal_check=%q)", item, o.URL, o.DealCheck)
			continue
		}
		candidates = append(candidates, o)
	}

	// A sale must never outrank a cheaper regular price. Pin the cheapest
	// in-stock offer so the deal gate cannot remove it; every other slot still
	// needs genuine deal evidence.
	cheapest, hasCheapest := cheapestOffer(candidates)

	seen := make(map[string]bool, len(candidates))
	for _, o := range candidates {
		if seen[o.URL] {
			dropped++
			continue
		}
		pinned := hasCheapest && o.URL == cheapest.URL
		if requireDeal && !pinned && !isDeal(o) {
			dropped++
			log.Printf("item %q: drop %s (%s): no verified sale or low price", item, o.URL, o.Store)
			continue
		}
		seen[o.URL] = true
		kept = append(kept, o)
	}
	if len(kept) > maxOffersPerItem {
		capped = len(kept) - maxOffersPerItem
		// The cap must never cut the one offer the gate promised to keep.
		if hasCheapest && !containsOfferURL(kept[:maxOffersPerItem], cheapest.URL) {
			for _, o := range kept[maxOffersPerItem:] {
				if o.URL == cheapest.URL {
					kept[maxOffersPerItem-1] = o
					break
				}
			}
		}
		kept = kept[:maxOffersPerItem]
	}
	return kept, dropped, capped
}

func containsOfferURL(offers []offer, want string) bool {
	for _, o := range offers {
		if o.URL == want {
			return true
		}
	}
	return false
}

// cheapestOffer is the lowest landed-cost offer that is still buyable. It is
// the one offer exempt from the deal gate, because "cheapest" and "on sale"
// answer different questions. Out-of-stock offers are skipped, and a foreign
// offer without a landed cost is not comparable. An unknown domestic rate
// falls back to the item price and stays visible as NaN.
func cheapestOffer(offers []offer) (offer, bool) {
	var best offer
	bestCost := math.Inf(1)
	found := false
	for _, o := range offers {
		if !buyable(o) {
			continue
		}
		if c := offerCost(o); c < bestCost {
			best, bestCost, found = o, c, true
		}
	}
	return best, found
}

func buyable(o offer) bool {
	if o.Stock == "out_of_stock" {
		return false
	}
	return o.Shipping != nil || regionTier(o.StoreRegion) <= 1
}

// offerCost is the comparison cost used for pinning and notes. A known
// shipping rate is part of it; an unknown domestic rate falls back to the item
// price so a Norwegian offer without a published rate is not silently
// discarded.
func offerCost(o offer) float64 {
	if o.Shipping != nil {
		return o.Price + *o.Shipping
	}
	return o.Price
}

// cheapestNote explains a regular-price winner in the table: it names the
// best genuine sale and how much more that sale costs. The note is attached
// only to the cheapest row, so a sale never hides the cheaper option.
func cheapestNote(offers []offer) (string, string) {
	cheapest, ok := cheapestOffer(offers)
	if !ok || isDeal(cheapest) {
		return "", ""
	}
	note := "Cheapest overall, regular price."
	if cheapest.Shipping == nil {
		note = "Cheapest listed price, regular; shipping unknown."
	}
	sale, ok := bestSaleOffer(offers, cheapest.URL)
	if !ok {
		return cheapest.URL, note
	}
	note += fmt.Sprintf(" Best sale: %s %s kr", sale.Store, compactNOK(money(sale.Price)))
	if pct := salePct(sale.Price, sale.Ordinary); pct != "" {
		note += " (" + pct + "% off)"
	} else {
		note += " (" + sale.DealCheck + ")"
	}
	if diff := offerCost(sale) - offerCost(cheapest); diff > 0.005 {
		note += fmt.Sprintf(", %s kr more", compactNOK(money(diff)))
		if cheapest.Shipping == nil || sale.Shipping == nil {
			note += " before shipping"
		}
	}
	return cheapest.URL, note + "."
}

// bestSaleOffer is the cheapest offer that still has genuine deal evidence,
// excluding the regular-price winner itself.
func bestSaleOffer(offers []offer, excludeURL string) (offer, bool) {
	var best offer
	bestCost := math.Inf(1)
	found := false
	for _, o := range offers {
		if o.URL == excludeURL || !buyable(o) || !isDeal(o) {
			continue
		}
		if c := offerCost(o); c < bestCost {
			best, bestCost, found = o, c, true
		}
	}
	return best, found
}

// isDeal reports whether an offer has genuine discount evidence. It is the
// gate for slots 2-5, not for the cheapest offer: a regular price that is
// lower than every sale is still the best place to buy.
func isDeal(o offer) bool {
	if o.Ordinary != nil && *o.Ordinary > o.Price {
		return true
	}
	if o.HistoryLow != nil && o.Price <= *o.HistoryLow*1.05 {
		return true
	}
	return o.PriceTrend != nil && o.Price < *o.PriceTrend*0.98
}

type storedRow struct {
	recordedAt  string
	item        string
	product     string
	store       string
	storeRegion string
	url         string
	price       string
	shipping    string
	total       string
	ordinary    string
	salePct     string
	historyLow  string
	priceTrend  string
	dealCheck   string
	stock       string
	stockDetail string
	rating      string
	ratingCount string
	trustRating string
	trustCount  string
	status      string
	changeNOK   string
	changePct   string
	direction   string
	note        string
}

func (r storedRow) csv() []string {
	return []string{
		r.recordedAt, r.item, r.product, r.store, r.storeRegion, r.url, r.price, r.shipping, r.total,
		r.ordinary, r.salePct, r.historyLow, r.priceTrend, r.dealCheck,
		r.stock, r.stockDetail, r.rating, r.ratingCount, r.trustRating, r.trustCount,
		r.status, r.changeNOK, r.changePct, r.direction,
		r.note,
	}
}

type resultStore struct {
	mu       sync.Mutex
	rows     map[string][]storedRow
	history  map[string]float64
	csvPath  string
	notePath string
	closed   bool
}

var errStoreClosed = errors.New("result store is closed")

func openResultStore(csvPath, notePath string) (*resultStore, error) {
	noteDir := filepath.Dir(notePath)
	if info, err := os.Stat(noteDir); err != nil {
		return nil, fmt.Errorf("obsidian note directory %s: %w", noteDir, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("obsidian note directory %s is not a directory", noteDir)
	}
	if dir := filepath.Dir(csvPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	rows := make(map[string][]storedRow)
	data, err := os.ReadFile(csvPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	case len(bytes.TrimSpace(data)) == 0:
	default:
		rows, err = loadRows(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", csvPath, err)
		}
	}

	trimmed := 0
	for item, list := range rows {
		for i := range list {
			if list[i].storeRegion == "" && list[i].url != "" {
				list[i].storeRegion = regionFromURL(list[i].url)
			}
			if list[i].price != "" && list[i].shipping == "" {
				list[i].shipping = "NaN"
			}
			if list[i].shipping == "NaN" && list[i].total == "" {
				list[i].total = "NaN"
			}
		}
		rows[item] = list
		if len(list) > maxOffersPerItem {
			rows[item] = append([]storedRow(nil), list[len(list)-maxOffersPerItem:]...)
			trimmed++
		}
	}
	if trimmed > 0 {
		log.Printf("results CSV: trimmed %d item(s) to the latest %d rows", trimmed, maxOffersPerItem)
	}

	store := &resultStore{
		rows:     rows,
		history:  rebuildHistory(rows),
		csvPath:  csvPath,
		notePath: notePath,
	}
	if err := writeCSV(csvPath, rows); err != nil {
		return nil, err
	}
	if err := writeNote(notePath, rows); err != nil {
		log.Printf("obsidian note %s: %v", notePath, err)
	}
	return store, nil
}

func loadRows(data []byte) (map[string][]storedRow, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	index := make(map[string]int, len(header))
	for i, h := range header {
		h = strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")
		index[h] = i
	}
	for _, col := range csvHeader {
		if _, ok := index[col]; !ok {
			if optionalColumn(col) {
				index[col] = -1
				continue
			}
			return nil, fmt.Errorf("incompatible header: missing column %q", col)
		}
	}
	rows := make(map[string][]storedRow)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		row := storedRow{
			recordedAt:  field(rec, index["recorded_at"]),
			item:        field(rec, index["item"]),
			product:     field(rec, index["product"]),
			store:       field(rec, index["store"]),
			storeRegion: field(rec, index["store_region"]),
			url:         field(rec, index["url"]),
			price:       field(rec, index["price_nok"]),
			shipping:    field(rec, index["shipping_nok"]),
			total:       field(rec, index["total_nok"]),
			ordinary:    field(rec, index["ordinary_price_nok"]),
			salePct:     field(rec, index["sale_pct"]),
			historyLow:  field(rec, index["history_low_nok"]),
			priceTrend:  field(rec, index["price_trend_nok"]),
			dealCheck:   field(rec, index["deal_check"]),
			stock:       field(rec, index["stock"]),
			stockDetail: field(rec, index["stock_detail"]),
			rating:      field(rec, index["rating"]),
			ratingCount: field(rec, index["rating_count"]),
			trustRating: field(rec, index["trustpilot_rating"]),
			trustCount:  field(rec, index["trustpilot_reviews"]),
			status:      field(rec, index["status"]),
			changeNOK:   field(rec, index["price_change_nok"]),
			changePct:   field(rec, index["price_change_pct"]),
			direction:   field(rec, index["change_direction"]),
			note:        field(rec, index["note"]),
		}
		if row.item == "" {
			continue
		}
		rows[row.item] = append(rows[row.item], row)
	}
}

func field(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// optionalColumn lets an older CSV load without these columns. They are
// rewritten with the current header on the next save.
func optionalColumn(col string) bool {
	switch col {
	case "product", "trustpilot_rating", "trustpilot_reviews", "note", "store_region", "stock_detail":
		return true
	}
	return false
}

func rebuildHistory(rows map[string][]storedRow) map[string]float64 {
	history := make(map[string]float64)
	for item, list := range rows {
		for _, row := range list {
			price, err := strconv.ParseFloat(strings.TrimSpace(row.price), 64)
			if err != nil || !finite(price) {
				continue
			}
			history[item+"\x00"+row.url] = price
		}
	}
	return history
}

func (st *resultStore) Replace(item string, offers []offer) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return 0, errStoreClosed
	}

	recordedAt := time.Now().Format(time.RFC3339)
	replacement := make([]storedRow, 0, len(offers))
	for _, o := range offers {
		prev, hasPrev := st.history[item+"\x00"+o.URL]
		replacement = append(replacement, newStoredRow(item, recordedAt, o, prev, hasPrev))
	}
	if url, note := cheapestNote(offers); note != "" {
		for i := range replacement {
			if replacement[i].url == url {
				replacement[i].note = note
				break
			}
		}
	}
	if len(replacement) == 0 {
		replacement = []storedRow{noOfferRow(item, recordedAt, "No offer found that meets the criteria: in stock and genuinely discounted, at a verified low, clearly below trend, or the cheapest verified offer.", nil)}
	}

	next := make(map[string][]storedRow, len(st.rows)+1)
	for key, list := range st.rows {
		next[key] = list
	}
	next[item] = replacement
	if err := st.commitLocked(next); err != nil {
		return 0, err
	}
	return len(offers), nil
}

// NoOffer records a miss row for an item. A later successful search replaces
// it through Replace.
func (st *resultStore) NoOffer(item, reason string, candidate *offer) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStoreClosed
	}
	next := make(map[string][]storedRow, len(st.rows)+1)
	for key, list := range st.rows {
		next[key] = list
	}
	next[item] = []storedRow{noOfferRow(item, time.Now().Format(time.RFC3339), reason, candidate)}
	return st.commitLocked(next)
}

func (st *resultStore) HasRows(item string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.rows[item]) > 0
}

// commitLocked persists a new row map. The caller holds st.mu.
func (st *resultStore) commitLocked(next map[string][]storedRow) error {
	if err := writeCSV(st.csvPath, next); err != nil {
		return err
	}
	st.rows = next
	st.history = rebuildHistory(next)
	if err := writeNote(st.notePath, next); err != nil {
		log.Printf("obsidian note %s: %v", st.notePath, err)
	}
	return nil
}

func noOfferRow(item, recordedAt, reason string, candidate *offer) storedRow {
	row := storedRow{
		recordedAt: recordedAt,
		item:       item,
		product:    "No offer found",
		dealCheck:  "none",
		stock:      "unknown",
		status:     "none",
		note:       reason,
	}
	if candidate != nil {
		row.store = candidate.Store
		row.storeRegion = candidate.StoreRegion
		row.url = candidate.URL
	}
	return row
}

func (st *resultStore) Close() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
	return nil
}

func newStoredRow(item, recordedAt string, o offer, prev float64, hasPrev bool) storedRow {
	status, changeNOK, changePct, direction := changeFields(prev, hasPrev, o.Price)
	return storedRow{
		recordedAt:  recordedAt,
		item:        item,
		product:     o.Product,
		store:       o.Store,
		storeRegion: o.StoreRegion,
		url:         o.URL,
		price:       money(o.Price),
		shipping:    shippingMoney(o.Shipping),
		total:       totalMoney(o.Price, o.Shipping),
		ordinary:    optionalMoney(o.Ordinary),
		salePct:     salePct(o.Price, o.Ordinary),
		historyLow:  optionalMoney(o.HistoryLow),
		priceTrend:  optionalMoney(o.PriceTrend),
		dealCheck:   o.DealCheck,
		stock:       o.Stock,
		stockDetail: o.StockDetail,
		rating:      optionalRating(o.Rating),
		ratingCount: optionalCount(o.RatingCount),
		trustRating: optionalRating(o.TrustRating),
		trustCount:  optionalCount(o.TrustCount),
		status:      status,
		changeNOK:   changeNOK,
		changePct:   changePct,
		direction:   direction,
	}
}

func writeCSV(path string, rows map[string][]storedRow) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prisjeger-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	w := csv.NewWriter(tmp)
	if err := w.Write(csvHeader); err != nil {
		tmp.Close()
		return err
	}
	for _, item := range sortedItems(rows) {
		for _, row := range sortedByTotal(rows[item]) {
			if err := w.Write(row.csv()); err != nil {
				tmp.Close()
				return err
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func sortedItems(rows map[string][]storedRow) []string {
	items := make([]string, 0, len(rows))
	for item, list := range rows {
		if len(list) > 0 {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ti, oki := cheapestTotal(rows[items[i]])
		tj, okj := cheapestTotal(rows[items[j]])
		switch {
		case oki != okj:
			return oki
		case oki && ti != tj:
			return ti < tj
		default:
			return items[i] < items[j]
		}
	})
	return items
}

func cheapestTotal(rows []storedRow) (float64, bool) {
	best := math.Inf(1)
	found := false
	for _, row := range rows {
		if v, ok := rowTotal(row); ok && v < best {
			best = v
			found = true
		}
	}
	return best, found
}

func rowTotal(row storedRow) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(row.total), 64)
	if err != nil || !finite(v) {
		return 0, false
	}
	return v, true
}

func rowPrice(row storedRow) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(row.price), 64)
	if err != nil || !finite(v) {
		return math.Inf(1)
	}
	return v
}

func sortedByTotal(rows []storedRow) []storedRow {
	out := append([]storedRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		ti, oki := rowTotal(out[i])
		tj, okj := rowTotal(out[j])
		if oki != okj {
			return oki
		}
		if oki && ti != tj {
			return ti < tj
		}
		regI, regJ := regionTier(out[i].storeRegion), regionTier(out[j].storeRegion)
		if regI != regJ {
			return regI < regJ
		}
		trustI, hasTrustI := rowTrust(out[i])
		trustJ, hasTrustJ := rowTrust(out[j])
		if hasTrustI != hasTrustJ {
			return hasTrustI
		}
		if hasTrustI && trustI != trustJ {
			return trustI > trustJ
		}
		pi, pj := rowPrice(out[i]), rowPrice(out[j])
		if pi != pj {
			return pi < pj
		}
		if out[i].store != out[j].store {
			return out[i].store < out[j].store
		}
		return out[i].url < out[j].url
	})
	return out
}

// rowTrust returns the Trustpilot score. It is the tie-breaker after the
// landed cost and the region tier, so a better-rated store wins when two
// otherwise comparable offers cost the same.
func rowTrust(row storedRow) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(row.trustRating), 64)
	if err != nil || !finite(v) || v <= 0 {
		return 0, false
	}
	return v, true
}

const (
	noteStartMarker = "<!-- prisjeger:start -->"
	noteEndMarker   = "<!-- prisjeger:end -->"
)

func writeNote(path string, rows map[string][]storedRow) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(existing)
	block := noteBlock(rows, time.Now())

	if start := strings.Index(content, noteStartMarker); start >= 0 {
		if rel := strings.Index(content[start+len(noteStartMarker):], noteEndMarker); rel >= 0 {
			end := start + len(noteStartMarker) + rel + len(noteEndMarker)
			content = content[:start] + block + content[end:]
			return writeFileAtomic(path, []byte(content))
		}
	}
	if content == "" {
		content = block + "\n"
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
	}
	return writeFileAtomic(path, []byte(content))
}

func noteBlock(rows map[string][]storedRow, now time.Time) string {
	var b strings.Builder
	b.WriteString(noteStartMarker)
	b.WriteString("\n")
	if len(sortedItems(rows)) == 0 {
		b.WriteString("_No offers yet._\n")
		b.WriteString(noteEndMarker)
		return b.String()
	}
	fmt.Fprintf(&b, "_Updated %s. Source: results.csv._\n\n", now.Format("2006-01-02 15:04"))
	b.WriteString("| Item | Product | Price | Ship | Total | Ordinary | Sale | Low | Stock | Trustpilot | Rating | Status | Change | Deal | Region | Store | Link | Note |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, item := range sortedItems(rows) {
		for _, row := range sortedByTotal(rows[item]) {
			link := ""
			if row.url != "" {
				link = "[open](<" + row.url + ">)"
			}
			cells := []string{
				row.item,
				row.product,
				compactNOK(row.price),
				compactNOK(row.shipping),
				compactNOK(row.total),
				compactNOK(row.ordinary),
				saleCell(row.salePct),
				compactNOK(row.historyLow),
				stockCell(row.stock, row.stockDetail),
				ratingCell(row.trustRating, row.trustCount),
				ratingCell(row.rating, row.ratingCount),
				row.status,
				changeCell(row),
				row.dealCheck,
				strings.ToUpper(row.storeRegion),
				row.store,
				link,
				row.note,
			}
			for i := range cells {
				cells[i] = escapeCell(cells[i])
			}
			b.WriteString("| ")
			b.WriteString(strings.Join(cells, " | "))
			b.WriteString(" |\n")
		}
	}
	b.WriteString(noteEndMarker)
	return b.String()
}

func compactNOK(v string) string {
	return strings.TrimSuffix(v, ".00")
}

func saleCell(v string) string {
	if v == "" {
		return ""
	}
	return v + "%"
}

func ratingCell(rating, count string) string {
	switch {
	case rating == "":
		return ""
	case count == "":
		return rating
	default:
		return rating + " (" + count + ")"
	}
}

// stockCell puts the page's quantity or low-stock wording next to the state,
// so "in_stock (2 igjen)" says both whether it is buyable and whether to hurry.
func stockCell(stock, detail string) string {
	if detail == "" {
		return stock
	}
	if stock == "" || stock == "unknown" {
		return detail
	}
	return stock + " (" + detail + ")"
}

func changeCell(row storedRow) string {
	switch row.status {
	case "change":
		return row.direction + " " + row.changeNOK + " (" + row.changePct + "%)"
	case "same":
		return "same"
	default:
		return ""
	}
}

func escapeCell(v string) string {
	return strings.ReplaceAll(v, "|", `\|`)
}

func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prisjeger-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func changeFields(prev float64, hasPrev bool, current float64) (status, nok, pct, direction string) {
	if !hasPrev {
		return "new", "", "", ""
	}
	diff := current - prev
	if math.Abs(diff) <= 0.005 {
		return "same", "0.00", "0.0", "same"
	}
	direction = "up"
	if diff < 0 {
		direction = "down"
	}
	return "change",
		strconv.FormatFloat(diff, 'f', 2, 64),
		strconv.FormatFloat(diff/prev*100, 'f', 1, 64),
		direction
}

func money(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func optionalMoney(v *float64) string {
	if v == nil {
		return ""
	}
	return money(*v)
}

func totalMoney(price float64, shipping *float64) string {
	if shipping == nil {
		return "NaN"
	}
	return money(price + *shipping)
}

// shippingMoney makes a missing rate explicit instead of leaving the cell
// blank. The table and CSV then show NaN and the row sorts as unknown.
func shippingMoney(v *float64) string {
	if v == nil {
		return "NaN"
	}
	return money(*v)
}

func salePct(price float64, ordinary *float64) string {
	if ordinary == nil || *ordinary <= 0 {
		return ""
	}
	return strconv.FormatFloat((*ordinary-price)/(*ordinary)*100, 'f', 1, 64)
}

func optionalRating(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}

func optionalCount(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

type config struct {
	addr        string
	list        string
	csvPath     string
	obsidian    string
	skill       string
	opencode    string
	model       string
	schedule    string
	times       []int
	timeout     time.Duration
	parallel    int
	trust       *trustpilotCache
	minTrust    float64
	requireDeal bool
	webLog      *webLog
	postalCode  string
}

type status struct {
	Active    int    `json:"active"`
	Done      int    `json:"done"`
	Failed    int    `json:"failed"`
	LastError string `json:"last_error"`
	CSVFile   string `json:"csv_file"`
}

type server struct {
	cfg     config
	results *resultStore
	sem     chan struct{}
	root    context.Context

	mu      sync.Mutex
	active  int
	done    int
	failed  int
	lastErr string
}

func main() {
	cfg := config{
		addr:     envOr("PRISJEGER_ADDR", "127.0.0.1:8080"),
		list:     envOr("PRISJEGER_LIST", "handleliste.txt"),
		csvPath:  envOr("PRISJEGER_CSV", "results.csv"),
		obsidian: envOr("PRISJEGER_OBSIDIAN", "/home/ajg/obsidian/August/prisjeger.md"),
		skill:    envOr("PRISJEGER_SKILL", ".opencode/skills/shopping-expert/SKILL.md"),
		opencode: envOr("PRISJEGER_OPENCODE", "opencode"),
		model:    envOr("PRISJEGER_MODEL", ""),
		schedule: envOr("PRISJEGER_SCHEDULE", "10:00,22:00"),
	}
	timeoutFlag := envOr("PRISJEGER_TIMEOUT", "20m")
	parallelFlag := envOr("PRISJEGER_PARALLEL", "3")
	trustCache := envOr("PRISJEGER_TRUST_CACHE", ".run/trustpilot.json")
	minTrustFlag := envOr("PRISJEGER_MIN_TRUST", "0")
	webLogPath := envOr("PRISJEGER_WEB_LOG", "websearch.log")
	postalCode := envOr("PRISJEGER_POSTAL_CODE", "6773")
	requireDealDefault, err := strconv.ParseBool(envOr("PRISJEGER_REQUIRE_DEAL", "true"))
	if err != nil {
		log.Fatalf("invalid PRISJEGER_REQUIRE_DEAL: %v", err)
	}
	requireDeal := flag.Bool("require-deal", requireDealDefault, "drop offers without a verified sale or low price")
	backfillTrust := flag.Bool("backfill-trust", false, "refresh Trustpilot store ratings for existing rows, then exit")

	flag.StringVar(&cfg.addr, "addr", cfg.addr, "listen address")
	flag.StringVar(&cfg.list, "list", cfg.list, "shopping list file")
	flag.StringVar(&cfg.csvPath, "csv", cfg.csvPath, "results CSV file, latest 5 rows per item")
	flag.StringVar(&cfg.obsidian, "obsidian", cfg.obsidian, "Obsidian note regenerated with the results table")
	flag.StringVar(&cfg.skill, "skill", cfg.skill, "shopping-expert skill passed to opencode")
	flag.StringVar(&cfg.opencode, "opencode", cfg.opencode, "opencode executable")
	flag.StringVar(&cfg.model, "model", cfg.model, "model passed to opencode, empty for its default")
	flag.StringVar(&cfg.schedule, "schedule", cfg.schedule, "comma-separated HH:MM local times for full runs, empty disables")
	flag.StringVar(&timeoutFlag, "timeout", timeoutFlag, "per-item search timeout")
	flag.StringVar(&parallelFlag, "parallel", parallelFlag, "maximum concurrent searches")
	flag.StringVar(&trustCache, "trust-cache", trustCache, "Trustpilot rating cache file")
	flag.StringVar(&minTrustFlag, "min-trust", minTrustFlag, "drop offers from stores rated below this Trustpilot score, 0 disables")
	flag.StringVar(&webLogPath, "web-log", webLogPath, "append every fetched and searched URL to this file")
	flag.StringVar(&postalCode, "postal-code", postalCode, "buyer postal code for shipping estimates")
	flag.Parse()

	times, err := parseSchedule(cfg.schedule)
	if err != nil {
		log.Fatalf("invalid -schedule %q: %v", cfg.schedule, err)
	}
	cfg.times = times

	timeout, err := time.ParseDuration(timeoutFlag)
	if err != nil || timeout <= 0 {
		log.Fatalf("invalid -timeout %q", timeoutFlag)
	}
	cfg.timeout = timeout

	parallel, err := strconv.Atoi(parallelFlag)
	if err != nil || parallel < 1 {
		log.Fatalf("invalid -parallel %q", parallelFlag)
	}
	cfg.parallel = parallel

	minTrust, err := strconv.ParseFloat(minTrustFlag, 64)
	if err != nil || minTrust < 0 || minTrust > 5 {
		log.Fatalf("invalid -min-trust %q: want 0-5", minTrustFlag)
	}
	cfg.minTrust = minTrust
	cfg.requireDeal = *requireDeal
	cfg.trust = newTrustpilotCache(trustCache)
	cfg.webLog = newWebLog(webLogPath)
	cfg.postalCode = strings.TrimSpace(postalCode)

	absSkill, err := filepath.Abs(cfg.skill)
	if err != nil {
		log.Fatalf("resolve skill path: %v", err)
	}
	if info, err := os.Stat(absSkill); err != nil || info.IsDir() {
		log.Fatalf("skill file not found: %s", absSkill)
	}
	cfg.skill = absSkill

	store, err := openResultStore(cfg.csvPath, cfg.obsidian)
	if err != nil {
		log.Fatalf("open result store: %v", err)
	}

	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *backfillTrust {
		filled, total := store.BackfillTrust(root, cfg.trust)
		if err := store.Close(); err != nil {
			log.Fatalf("close result store: %v", err)
		}
		log.Printf("trustpilot: updated %d of %d rows", filled, total)
		return
	}

	s := &server{
		cfg:     cfg,
		results: store,
		sem:     make(chan struct{}, cfg.parallel),
		root:    root,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /results", s.handleResults)
	mux.HandleFunc("POST /search", s.handleSearch)

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-root.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	go func() {
		select {
		case <-time.After(2 * time.Second):
			s.startRun("startup", true)
		case <-root.Done():
		}
	}()
	if len(cfg.times) > 0 {
		go s.runScheduler()
	}

	log.Printf("prisjeger: listening on %s, list %s, csv %s, obsidian %s, schedule %q", cfg.addr, cfg.list, cfg.csvPath, cfg.obsidian, cfg.schedule)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		store.Close()
		log.Fatal(err)
	}
	log.Print("prisjeger: stopped")
	store.Close()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, `prisjeger: price hunter

POST /search    start a search run for every item in %s
GET  /status    counters and the current CSV file
GET  /results   download the current CSV file
GET  /healthz   ok

flags: -addr -list -csv -obsidian -skill -opencode -model -timeout -parallel -schedule
env:   PRISJEGER_ADDR PRISJEGER_LIST PRISJEGER_CSV PRISJEGER_OBSIDIAN PRISJEGER_SKILL
       PRISJEGER_OPENCODE PRISJEGER_MODEL PRISJEGER_TIMEOUT PRISJEGER_PARALLEL
       PRISJEGER_SCHEDULE
`, s.cfg.list)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snap := status{
		Active:    s.active,
		Done:      s.done,
		Failed:    s.failed,
		LastError: s.lastErr,
		CSVFile:   s.cfg.csvPath,
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) handleResults(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.cfg.csvPath)
}

var errEmptyList = errors.New("shopping list is empty")

// startRun reads the list and launches one search per item. Scheduled and
// startup runs skip when a search is already active.
func (s *server) startRun(source string, skipIfActive bool) (int, error) {
	items, err := readList(s.cfg.list)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, errEmptyList
	}

	if skipIfActive {
		s.mu.Lock()
		active := s.active > 0
		s.mu.Unlock()
		if active {
			log.Printf("%s run skipped: a search is already active", source)
			return 0, nil
		}
	}
	for _, item := range items {
		// The request context dies with the response, so runs use the server root.
		go s.search(item)
	}
	log.Printf("%s run started: %d items", source, len(items))
	return len(items), nil
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	n, err := s.startRun("search", false)
	switch {
	case errors.Is(err, errEmptyList):
		http.Error(w, "shopping list is empty: "+s.cfg.list, http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "read list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"items":    n,
		"csv_file": s.cfg.csvPath,
	})
}

func parseSchedule(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	var times []int
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		t, err := time.Parse("15:04", part)
		if err != nil || len(part) != 5 {
			return nil, fmt.Errorf("%q is not HH:MM", part)
		}
		times = append(times, t.Hour()*60+t.Minute())
	}
	sort.Ints(times)
	return times, nil
}

func nextRun(now time.Time, times []int) time.Time {
	if len(times) == 0 {
		return time.Time{}
	}
	y, mo, d := now.Date()
	for _, m := range times {
		if at := time.Date(y, mo, d, m/60, m%60, 0, 0, now.Location()); at.After(now) {
			return at
		}
	}
	m := times[0]
	return time.Date(y, mo, d+1, m/60, m%60, 0, 0, now.Location())
}

func (s *server) runScheduler() {
	for next := nextRun(time.Now(), s.cfg.times); !next.IsZero(); next = nextRun(time.Now(), s.cfg.times) {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.root.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.startRun("scheduled", true)
	}
}

func shorten(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) <= n {
		return v
	}
	return v[:n-3] + "..."
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *server) search(item string) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	s.mu.Lock()
	s.active++
	s.mu.Unlock()

	err := s.runItem(s.root, item)

	if err != nil && !s.results.HasRows(item) {
		reason := "Search failed: " + shorten(err.Error(), 180)
		if noteErr := s.results.NoOffer(item, reason, nil); noteErr != nil {
			log.Printf("record failed search for %q: %v", item, noteErr)
		}
	}

	s.mu.Lock()
	s.active--
	if err != nil {
		s.failed++
		s.lastErr = item + ": " + err.Error()
	} else {
		s.done++
	}
	s.mu.Unlock()
	if err != nil {
		log.Printf("search %q: %v", item, err)
	}
}

func (s *server) runItem(parent context.Context, item string) error {
	firstCtx, cancel := context.WithTimeout(parent, s.cfg.timeout)
	firstText, sessionID, err := s.runChild(firstCtx, item)
	cancel()
	if err != nil {
		return err
	}
	firstRaw := parseOffers(firstText)

	followCtx, cancel := context.WithTimeout(parent, s.cfg.timeout)
	followText, followErr := s.followUp(followCtx, item, sessionID, firstText)
	cancel()
	if followErr != nil {
		log.Printf("item %q: follow-up failed: %v", item, followErr)
	}
	followRaw := parseOffers(followText)

	merged := append(followRaw, firstRaw...)
	offers, dropped, capped := normalizeOffers(item, merged, s.cfg.requireDeal)
	trustCtx, cancelTrust := context.WithTimeout(parent, 30*time.Second)
	s.cfg.trust.Enrich(trustCtx, offers)
	cancelTrust()
	kept, lowTrust := filterByTrust(offers, s.cfg.minTrust)
	written := 0
	if len(kept) == 0 {
		reason := "No offer found that meets the criteria: in stock and genuinely discounted, at a verified low, clearly below trend, or the cheapest verified offer."
		var candidate *offer
		if len(offers) > 0 {
			reason = "Offers were found but dropped by the store trust filter."
		} else if cheap, ok := cheapestCandidate(merged); ok {
			total := cheap.Price
			if cheap.Shipping != nil {
				total += *cheap.Shipping
			}
			reason += fmt.Sprintf(" Cheapest seen: %s %.0f kr (not discounted).", cheap.Store, total)
			c := cheap
			candidate = &c
		}
		if err := s.results.NoOffer(item, reason, candidate); err != nil {
			return err
		}
	} else {
		var err error
		written, err = s.results.Replace(item, kept)
		if err != nil {
			return err
		}
	}
	log.Printf("item %q: first=%d follow=%d written=%d dropped=%d capped=%d low_trust=%d", item, len(firstRaw), len(followRaw), written, dropped, capped, lowTrust)
	return nil
}

// cheapestCandidate is the cheapest offer the search saw, including offers the
// deal gate rejected. It makes a miss message concrete instead of generic.
func cheapestCandidate(raw []rawOffer) (offer, bool) {
	var best offer
	bestTotal := math.Inf(1)
	found := false
	for _, r := range raw {
		o, ok := toOffer(r)
		if !ok || o.Stock == "out_of_stock" {
			continue
		}
		total := o.Price
		if o.Shipping != nil {
			total += *o.Shipping
		}
		if total < bestTotal {
			best, bestTotal, found = o, total, true
		}
	}
	return best, found
}

type agentEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Text  string `json:"text"`
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
			Input  struct {
				URL     string `json:"url"`
				Query   string `json:"query"`
				Command string `json:"command"`
			} `json:"input"`
			Output string `json:"output"`
		} `json:"state"`
	} `json:"part"`
}

func (s *server) runChild(ctx context.Context, item string) (string, string, error) {
	return s.execChild(ctx, "--file", s.cfg.skill, searchPrompt(item, time.Now().Format("2006-01-02"), s.cfg.postalCode))
}

func (s *server) followUp(ctx context.Context, item, sessionID, firstText string) (string, error) {
	if sessionID != "" {
		text, _, err := s.execChild(ctx, "--session", sessionID, followUpPrompt(item, "", s.cfg.postalCode))
		return text, err
	}
	text, _, err := s.execChild(ctx, "--file", s.cfg.skill, followUpPrompt(item, firstText, s.cfg.postalCode))
	return text, err
}

func (s *server) execChild(ctx context.Context, extra ...string) (string, string, error) {
	args := []string{"run", "--format", "json"}
	if s.cfg.model != "" {
		args = append(args, "--model", s.cfg.model)
	}
	args = append(args, extra...)

	cmd := exec.CommandContext(ctx, s.cfg.opencode, args...)
	var stdout, stderr bytes.Buffer
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	reader := bufio.NewReader(pipe)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			stdout.Write(line)
			s.cfg.webLog.Write(webURLs(line))
		}
		if readErr != nil {
			break
		}
	}
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", "", err
	}
	text, sessionID := extractText(stdout.Bytes())
	return text, sessionID, nil
}

// extractText reads opencode's JSON event stream. A child that writes offer
// JSONL directly has no text events, so its raw stdout is the payload.
func extractText(raw []byte) (string, string) {
	var parts []string
	var sessionID string
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var ev agentEvent
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if sessionID == "" {
			sessionID = ev.SessionID
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			parts = append(parts, ev.Part.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n"), sessionID
	}
	return string(raw), sessionID
}

func readList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		items = append(items, line)
	}
	return items, nil
}

func searchPrompt(item, today, postalCode string) string {
	return fmt.Sprintf(`Read the attached shopping-expert skill and follow it exactly.

Today is %s.

The buyer's Norwegian postal code is %s. Use it for every shipping estimate and prefer home delivery over a pickup point.

Find current salg, tilbud, kampanje, and black offers in Norway, or from stores that ship to Norway, for: %s

Treat any size, capacity, or maximum price limit in the item as a hard filter. Omit offers that do not meet it.

Use the websearch tool and open the product pages to verify price, shipping, stock, rating, and price history. Do not trust search snippets.

Return only JSONL in the skill's schema, one object per line, at most 5 offers for this item, ranked by total landed cost. No prose, no markdown, no code fences. Omit any offer you cannot verify. If there are no valid offers, return nothing.`, today, postalCode, item)
}

func followUpPrompt(item, firstText, postalCode string) string {
	prompt := fmt.Sprintf(`Are there any better current salg, tilbud, kampanje, or black offers than these I found?

The buyer's Norwegian postal code is %s. Use it for every shipping estimate and prefer home delivery over a pickup point.

Re-check salg, tilbud, kampanje, and black offers in Norway, or from stores that ship to Norway, for: %s

Treat any size, capacity, or maximum price limit in the item as a hard filter. Omit offers that do not meet it.

Return only JSONL in the shopping-expert skill's schema, one object per line, at most 5 offers for this item, including the previous offers that are still valid plus any better ones, ranked by total landed cost. No prose, no markdown, no code fences. If there are no valid offers, return nothing.`, postalCode, item)
	if firstText = strings.TrimSpace(firstText); firstText != "" {
		prompt += "\n\nPrevious offers:\n" + firstText
	}
	return prompt
}
