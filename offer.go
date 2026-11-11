package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"strconv"
	"strings"
)

const maxOffersPerItem = 5

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

// maxStockDetailRunes keeps one verbose model from widening the Obsidian table.
const maxStockDetailRunes = 48

// cleanStockDetail keeps the page's wording and removes wrappers.
func cleanStockDetail(v string) string {
	s := strings.TrimSpace(v)
	s = strings.Trim(s, "()[]{}:;,.-–—")
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, maxStockDetailRunes)
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
