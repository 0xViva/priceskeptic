package priceskeptic

import "testing"

func TestRegionFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.elkjop.no/product/x":         "no",
		"https://autohobby.no/products/x":         "no",
		"https://cdon.se/product/x":               "se",
		"https://www.amazon.se/dp/B0":             "se",
		"https://www.amazon.de/dp/B0":             "eu",
		"https://www.amazon.co.uk/dp/B0":          "eu",
		"https://www.amazon.com/dp/B0":            "us",
		"https://www.bhphotovideo.com/c/x":        "us",
		"https://www.prisjakt.no/product.php?p=1": "other",
		"https://example.com/x":                   "other",
	}
	for in, want := range cases {
		if got := regionFromURL(in); got != want {
			t.Errorf("regionFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRegion(t *testing.T) {
	cases := map[string]string{
		"Norway": "no", "Norge": "no", "SE": "se", "Sweden": "se",
		"Europe": "eu", "EU": "eu", "UK": "eu", "USA": "us",
		"global": "other", "": "", "moon": "",
	}
	for in, want := range cases {
		if got := normalizeRegion(in); got != want {
			t.Errorf("normalizeRegion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreRegionPrefersModelValue(t *testing.T) {
	if got := storeRegion("https://www.amazon.de/dp/B0", "us"); got != "us" {
		t.Fatalf("storeRegion = %q, want the supplied us", got)
	}
	if got := storeRegion("https://www.amazon.de/dp/B0", ""); got != "eu" {
		t.Fatalf("storeRegion = %q, want derived eu", got)
	}
}

func TestSortedByTotalPrefersNordicOnTie(t *testing.T) {
	rows := []storedRow{
		{store: "EU", storeRegion: "eu", total: "100.00", price: "100.00"},
		{store: "NO", storeRegion: "no", total: "100.00", price: "100.00"},
	}
	got := sortedByTotal(rows)
	if got[0].store != "NO" {
		t.Fatalf("first = %q, want NO on equal total", got[0].store)
	}
	rows[0].total = "90.00"
	rows[0].price = "90.00"
	got = sortedByTotal(rows)
	if got[0].store != "EU" {
		t.Fatalf("first = %q, want the cheaper EU offer", got[0].store)
	}
}
