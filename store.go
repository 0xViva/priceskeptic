package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) > 0 {
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

	next := maps.Clone(st.rows)
	next[item] = replacement
	if err := st.commitLocked(next); err != nil {
		return 0, err
	}
	return len(offers), nil
}

// A later successful search replaces the miss row through Replace.
func (st *resultStore) NoOffer(item, reason string, candidate *offer) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStoreClosed
	}
	next := maps.Clone(st.rows)
	next[item] = []storedRow{noOfferRow(item, time.Now().Format(time.RFC3339), reason, candidate)}
	return st.commitLocked(next)
}

func (st *resultStore) HasRows(item string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.rows[item]) > 0
}

// An empty list is ignored so a missing or empty list cannot wipe the output.
func (st *resultStore) PruneItems(items []string) error {
	if len(items) == 0 {
		return nil
	}
	keep := make(map[string]struct{}, len(items))
	for _, item := range items {
		keep[item] = struct{}{}
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStoreClosed
	}

	next := maps.Clone(st.rows)
	removed := 0
	for item := range next {
		if _, ok := keep[item]; ok {
			continue
		}
		delete(next, item)
		removed++
	}
	if removed == 0 {
		return nil
	}
	log.Printf("results CSV: dropped %d item(s) no longer on the shopping list", removed)
	return st.commitLocked(next)
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
		status:     string(priceNone),
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
	change := diffPrice(prev, hasPrev, o.Price)
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
		status:      string(change.Status),
		changeNOK:   change.NOK,
		changePct:   change.Pct,
		direction:   string(change.Direction),
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

type priceStatus string

const (
	priceNew     priceStatus = "new"
	priceChanged priceStatus = "change"
	priceSame    priceStatus = "same"
	priceNone    priceStatus = "none"
)

type changeDirection string

const (
	directionUp   changeDirection = "up"
	directionDown changeDirection = "down"
	directionSame changeDirection = "same"
)

type priceChange struct {
	Status    priceStatus
	NOK       string
	Pct       string
	Direction changeDirection
}

func diffPrice(prev float64, hasPrev bool, current float64) priceChange {
	if !hasPrev {
		return priceChange{Status: priceNew}
	}
	diff := current - prev
	if math.Abs(diff) <= 0.005 {
		return priceChange{Status: priceSame, NOK: "0.00", Pct: "0.0", Direction: directionSame}
	}
	direction := directionUp
	if diff < 0 {
		direction = directionDown
	}
	return priceChange{
		Status:    priceChanged,
		NOK:       strconv.FormatFloat(diff, 'f', 2, 64),
		Pct:       strconv.FormatFloat(diff/prev*100, 'f', 1, 64),
		Direction: direction,
	}
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
