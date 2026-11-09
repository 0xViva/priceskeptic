package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Trustpilot blocks plain HTTP clients, so the reader proxy is the transport
// that works from this host. Ratings are cached for a week: a store's
// TrustScore moves slowly and each lookup costs an external request.
const (
	trustpilotTTL   = 7 * 24 * time.Hour
	trustpilotLimit = 3
)

type trustpilotEntry struct {
	Rating    float64   `json:"rating"`
	Reviews   int       `json:"reviews"`
	FetchedAt time.Time `json:"fetched_at"`
}

type trustpilotCache struct {
	path    string
	client  *http.Client
	mu      sync.Mutex
	saveMu  sync.Mutex
	entries map[string]trustpilotEntry
}

func newTrustpilotCache(path string) *trustpilotCache {
	t := &trustpilotCache{
		path:    path,
		client:  &http.Client{Timeout: 20 * time.Second},
		entries: make(map[string]trustpilotEntry),
	}
	if path == "" {
		return t
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("trustpilot cache dir %s: %v", dir, err)
		}
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var entries map[string]trustpilotEntry
		if err := json.Unmarshal(data, &entries); err == nil {
			t.entries = entries
		} else {
			log.Printf("trustpilot cache %s: %v", path, err)
		}
	case !os.IsNotExist(err):
		log.Printf("trustpilot cache %s: %v", path, err)
	}
	return t
}

// Enrich fills each offer's store level fields from Trustpilot. It never fails
// a search: a missing lookup leaves the columns empty.
func (t *trustpilotCache) Enrich(ctx context.Context, offers []offer) {
	if t == nil || len(offers) == 0 {
		return
	}
	domains := make([]string, 0, len(offers))
	for _, o := range offers {
		domains = append(domains, trustDomain(o.URL))
	}
	found := t.lookupMany(ctx, domains)
	for i := range offers {
		entry, ok := found[trustDomain(offers[i].URL)]
		if !ok {
			continue
		}
		rating, reviews := entry.Rating, entry.Reviews
		offers[i].TrustRating = &rating
		if entry.Reviews > 0 {
			offers[i].TrustCount = &reviews
		}
	}
}

func (t *trustpilotCache) lookupMany(ctx context.Context, domains []string) map[string]trustpilotEntry {
	out := make(map[string]trustpilotEntry)
	unique := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		unique = append(unique, d)
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, trustpilotLimit)
	)
	for _, d := range unique {
		wg.Add(1)
		go func(domain string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			entry, ok := t.lookup(ctx, domain)
			if !ok {
				return
			}
			mu.Lock()
			out[domain] = entry
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	return out
}

func (t *trustpilotCache) lookup(ctx context.Context, domain string) (trustpilotEntry, bool) {
	t.mu.Lock()
	entry, ok := t.entries[domain]
	t.mu.Unlock()
	if ok && time.Since(entry.FetchedAt) < trustpilotTTL {
		return entry, true
	}

	entry, ok = t.fetch(ctx, domain)
	if !ok {
		return trustpilotEntry{}, false
	}
	entry.FetchedAt = time.Now()

	t.mu.Lock()
	t.entries[domain] = entry
	t.mu.Unlock()
	t.save()
	return entry, true
}

func (t *trustpilotCache) fetch(ctx context.Context, domain string) (trustpilotEntry, bool) {
	paths := []string{
		"https://no.trustpilot.com/review/" + domain,
		"https://www.trustpilot.com/review/" + domain,
	}
	for _, target := range paths {
		body, err := t.get(ctx, "https://r.jina.ai/"+target)
		if err != nil {
			continue
		}
		if entry, ok := parseTrustpilot(string(body)); ok {
			return entry, true
		}
	}
	return trustpilotEntry{}, false
}

func (t *trustpilotCache) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	// The HTML view keeps the JSON-LD aggregateRating block, which carries the
	// exact score and review count. The markdown view drops the page header on
	// some large profiles.
	req.Header.Set("X-Return-Format", "html")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", target, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (t *trustpilotCache) save() {
	if t.path == "" {
		return
	}
	t.saveMu.Lock()
	defer t.saveMu.Unlock()

	t.mu.Lock()
	data, err := json.MarshalIndent(t.entries, "", "  ")
	t.mu.Unlock()
	if err != nil {
		return
	}
	if err := writeFileAtomic(t.path, append(data, '\n')); err != nil {
		log.Printf("trustpilot cache %s: %v", t.path, err)
	}
}

// BackfillTrust refreshes the store columns on rows that were written before
// the Trustpilot fields existed, or whose rating is older than the TTL.
func (st *resultStore) BackfillTrust(ctx context.Context, tp *trustpilotCache) (filled, total int) {
	if tp == nil {
		return 0, 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	domains := make([]string, 0)
	for _, list := range st.rows {
		for _, row := range list {
			total++
			domains = append(domains, trustDomain(row.url))
		}
	}
	found := tp.lookupMany(ctx, domains)

	for item, list := range st.rows {
		for i := range list {
			entry, ok := found[trustDomain(list[i].url)]
			if !ok {
				continue
			}
			list[i].trustRating = strconv.FormatFloat(entry.Rating, 'f', 1, 64)
			list[i].trustCount = ""
			if entry.Reviews > 0 {
				list[i].trustCount = strconv.Itoa(entry.Reviews)
			}
			filled++
		}
		st.rows[item] = list
	}
	if err := writeCSV(st.csvPath, st.rows); err != nil {
		log.Printf("trustpilot backfill CSV: %v", err)
	}
	if err := writeNote(st.notePath, st.rows); err != nil {
		log.Printf("trustpilot backfill note: %v", err)
	}
	return filled, total
}

var (
	tpAggregate     = regexp.MustCompile(`(?s)"aggregateRating"\s*:\s*\{(.*?)\}`)
	tpJSONRating    = regexp.MustCompile(`"ratingValue"\s*:\s*"?([0-9]+(?:[.,][0-9]+)?)"?`)
	tpJSONCount     = regexp.MustCompile(`"reviewCount"\s*:\s*"?([0-9][0-9.,]*k?)"?`)
	tpRatingTitleNO = regexp.MustCompile(`(?i)\bmed\s+([0-9]+[.,][0-9]+)\s*/\s*5`)
	tpRatingTitleEN = regexp.MustCompile(`(?i)\bwith\s+([0-9]+[.,][0-9]+)\s*/\s*5`)
	tpRatingLine    = regexp.MustCompile(`(?is)TrustScore[^\n<]*\n+\s*([0-9]+[.,][0-9]+)\s*\n`)
	tpRatingAny     = regexp.MustCompile(`(?i)TrustScore\s+([0-9]+[.,][0-9]+)`)
	tpCountLine     = regexp.MustCompile(`(?is)(?:Anmeldelser|Reviews)[ \t]*\n+[ \t]*([0-9][0-9.,\s\x{00a0}]*[kK]?)[ \t]*(?:\n|$)`)
)

func parseTrustpilot(text string) (trustpilotEntry, bool) {
	if entry, ok := parseTrustAggregate(text); ok {
		return entry, true
	}
	rating, ok := parseTrustRating(text)
	if !ok {
		return trustpilotEntry{}, false
	}
	entry := trustpilotEntry{Rating: rating}
	if reviews, ok := parseTrustReviews(text); ok {
		entry.Reviews = reviews
	}
	return entry, true
}

// parseTrustAggregate reads the JSON-LD block. It is the authoritative number:
// the markdown view can drop the header, and the star image rounds to halves.
func parseTrustAggregate(text string) (trustpilotEntry, bool) {
	block := tpAggregate.FindStringSubmatch(text)
	if len(block) != 2 {
		return trustpilotEntry{}, false
	}
	ratingMatch := tpJSONRating.FindStringSubmatch(block[1])
	if len(ratingMatch) != 2 {
		return trustpilotEntry{}, false
	}
	rating, err := parseTrustRatingNumber(ratingMatch[1])
	if err != nil || rating <= 0 || rating > 5 {
		return trustpilotEntry{}, false
	}
	entry := trustpilotEntry{Rating: rating}
	if countMatch := tpJSONCount.FindStringSubmatch(block[1]); len(countMatch) == 2 {
		if reviews, err := parseTrustCount(countMatch[1]); err == nil && reviews >= 0 {
			entry.Reviews = reviews
		}
	}
	return entry, true
}

func parseTrustRating(text string) (float64, bool) {
	for _, re := range []*regexp.Regexp{tpRatingTitleNO, tpRatingTitleEN, tpRatingLine, tpRatingAny} {
		m := re.FindStringSubmatch(text)
		if len(m) != 2 {
			continue
		}
		v, err := parseTrustRatingNumber(m[1])
		if err == nil && v > 0 && v <= 5 {
			return v, true
		}
	}
	return 0, false
}

func parseTrustReviews(text string) (int, bool) {
	m := tpCountLine.FindStringSubmatch(text)
	if len(m) != 2 {
		return 0, false
	}
	v, err := parseTrustCount(m[1])
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func parseTrustRatingNumber(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\u00a0", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}

// parseTrustCount handles "578", "1 234", "1,234" and "3,5k".
func parseTrustCount(raw string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "\u00a0", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.HasSuffix(s, "k") {
		s = strings.TrimSuffix(s, "k")
		s = strings.ReplaceAll(s, ",", ".")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		return int(math.Round(v * 1000)), nil
	}
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0, fmt.Errorf("no digits in %q", raw)
	}
	return strconv.Atoi(digits.String())
}

// trustDomain maps a product URL to the domain Trustpilot profiles stores
// under. Comparison sites and marketplaces are skipped: the seller is not the
// domain, so its score would describe the wrong business.
func trustDomain(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return ""
	}
	for _, skip := range []string{"prisjakt.", "amazon.", "ebay.", "finn.no", "aliexpress.", "temu.", "wish.com"} {
		if strings.Contains(host, skip) {
			return ""
		}
	}
	return host
}

// filterByTrust drops offers from stores with a known score below min. A
// missing Trustpilot profile is unknown, not bad, so it is kept.
func filterByTrust(offers []offer, min float64) ([]offer, int) {
	if min <= 0 {
		return offers, 0
	}
	kept := offers[:0]
	dropped := 0
	for _, o := range offers {
		if o.TrustRating != nil && *o.TrustRating < min {
			dropped++
			log.Printf("drop %s: Trustpilot %.1f below minimum %.1f", o.Store, *o.TrustRating, min)
			continue
		}
		kept = append(kept, o)
	}
	return kept, dropped
}
