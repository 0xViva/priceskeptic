package main

import "testing"

func TestParseTrustpilotNorwegianTitle(t *testing.T) {
	text := `Title: Mekster.no vurderes som «Enestående» med 4,4/5 på Trustpilot

[![Image 6: TrustScore 4.5 av 5](https://cdn.trustpilot.net/stars-4.5.svg)
4,4

# Mekster.no Anmeldelser

578

•`
	entry, ok := parseTrustpilot(text)
	if !ok {
		t.Fatal("parseTrustpilot returned false")
	}
	if entry.Rating != 4.4 {
		t.Fatalf("rating = %v, want 4.4", entry.Rating)
	}
	if entry.Reviews != 578 {
		t.Fatalf("reviews = %d, want 578", entry.Reviews)
	}
}

func TestParseTrustpilotEnglishTitle(t *testing.T) {
	text := `Title: Example is rated "Excellent" with 4.7 / 5 on Trustpilot

Example Reviews

1,234`
	entry, ok := parseTrustpilot(text)
	if !ok {
		t.Fatal("parseTrustpilot returned false")
	}
	if entry.Rating != 4.7 {
		t.Fatalf("rating = %v, want 4.7", entry.Rating)
	}
	if entry.Reviews != 1234 {
		t.Fatalf("reviews = %d, want 1234", entry.Reviews)
	}
}

func TestParseTrustpilotScoreLine(t *testing.T) {
	text := `AutoHobby.no Anmeldelser

866

![Image 2: TrustScore 4.5 av 5](https://cdn.trustpilot.net/stars-4.5.svg)
4,5`
	entry, ok := parseTrustpilot(text)
	if !ok {
		t.Fatal("parseTrustpilot returned false")
	}
	if entry.Rating != 4.5 {
		t.Fatalf("rating = %v, want 4.5", entry.Rating)
	}
	if entry.Reviews != 866 {
		t.Fatalf("reviews = %d, want 866", entry.Reviews)
	}
}

func TestParseTrustpilotAggregateJSON(t *testing.T) {
	text := `<html><head><title>Anmeldelser av Elkjøp</title>
<script type="application/ld+json">{"@type":"Organization","aggregateRating":{"@type":"AggregateRating","bestRating":"5","worstRating":"1","ratingValue":"1.3","reviewCount":"2354"}}</script>
</head></html>`
	entry, ok := parseTrustpilot(text)
	if !ok {
		t.Fatal("parseTrustpilot returned false")
	}
	if entry.Rating != 1.3 {
		t.Fatalf("rating = %v, want 1.3", entry.Rating)
	}
	if entry.Reviews != 2354 {
		t.Fatalf("reviews = %d, want 2354", entry.Reviews)
	}
}

func TestParseTrustpilotRatingOnly(t *testing.T) {
	entry, ok := parseTrustpilot(`rated with 3.9 / 5 on Trustpilot`)
	if !ok {
		t.Fatal("parseTrustpilot returned false")
	}
	if entry.Rating != 3.9 || entry.Reviews != 0 {
		t.Fatalf("entry = %+v, want rating 3.9 with no reviews", entry)
	}
}

func TestParseTrustpilotRejectsMissingScore(t *testing.T) {
	if _, ok := parseTrustpilot(`no score here`); ok {
		t.Fatal("parseTrustpilot accepted text without a score")
	}
}

func TestParseTrustCount(t *testing.T) {
	cases := map[string]int{
		"578":        578,
		"1 234":      1234,
		"1,234":      1234,
		"1.234":      1234,
		"3,5k":       3500,
		"12k":        12000,
		"4\u00a0866": 4866,
	}
	for in, want := range cases {
		got, err := parseTrustCount(in)
		if err != nil {
			t.Fatalf("parseTrustCount(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseTrustCount(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTrustDomain(t *testing.T) {
	cases := map[string]string{
		"https://www.mekster.no/product":          "mekster.no",
		"https://mekster.no/product":              "mekster.no",
		"https://www.prisjakt.no/product.php?p=1": "",
		"https://www.amazon.de/dp/B0":             "",
		"not a url":                               "",
	}
	for in, want := range cases {
		if got := trustDomain(in); got != want {
			t.Errorf("trustDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterByTrust(t *testing.T) {
	low := 2.9
	high := 4.6
	offers := []offer{
		{Store: "Low", TrustRating: &low},
		{Store: "High", TrustRating: &high},
		{Store: "Unknown"},
	}
	kept, dropped := filterByTrust(offers, 3.5)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(kept) != 2 || kept[0].Store != "High" || kept[1].Store != "Unknown" {
		t.Fatalf("kept = %+v", kept)
	}
}

func TestIsDeal(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		o    offer
		want bool
	}{
		{"sale against before-price", offer{Price: 299, Ordinary: f(399)}, true},
		{"full price", offer{Price: 299, Ordinary: f(299)}, false},
		{"at historical low", offer{Price: 229, HistoryLow: f(229)}, true},
		{"above historical low", offer{Price: 229, HistoryLow: f(200)}, false},
		{"below trend", offer{Price: 280, PriceTrend: f(300)}, true},
		{"near trend", offer{Price: 295, PriceTrend: f(300)}, false},
		{"no evidence", offer{Price: 299}, false},
	}
	for _, tc := range cases {
		if got := isDeal(tc.o); got != tc.want {
			t.Errorf("%s: isDeal = %v, want %v", tc.name, got, tc.want)
		}
	}
}
