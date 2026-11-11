package prisjeger

import (
	"net/url"
	"strings"
)

// store_region is the source tier the buyer cares about: a Norwegian or
// Swedish shop is ideal, another EU/EEA shop is second, and a US shop is only
// worth it when the landed cost survives shipping and import.
var euTLDs = map[string]bool{
	"at": true, "be": true, "bg": true, "ch": true, "cy": true, "cz": true,
	"de": true, "dk": true, "ee": true, "es": true, "fi": true, "fr": true,
	"gr": true, "hr": true, "hu": true, "ie": true, "is": true, "it": true,
	"li": true, "lt": true, "lu": true, "lv": true, "mt": true, "nl": true,
	"pl": true, "pt": true, "ro": true, "si": true, "sk": true,
}

var usDomains = map[string]bool{
	"adorama.com": true, "amazon.com": true, "bestbuy.com": true,
	"bhphotovideo.com": true, "costco.com": true, "ebay.com": true,
	"focuscamera.com": true, "microcenter.com": true, "newegg.com": true,
	"officedepot.com": true, "samsclub.com": true, "target.com": true,
	"walmart.com": true,
}

func normalizeRegion(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "_", "")
	switch s {
	case "no", "norway", "norge", "noreg":
		return "no"
	case "se", "sweden", "sverige":
		return "se"
	case "eu", "europe", "eea", "europa", "uk", "gb", "unitedkingdom":
		return "eu"
	case "us", "usa", "unitedstates", "unitedstatesofamerica":
		return "us"
	case "other", "global", "world":
		return "other"
	}
	return ""
}

func storeRegion(rawURL, given string) string {
	if r := normalizeRegion(given); r != "" {
		return r
	}
	return regionFromURL(rawURL)
}

// regionFromURL is the fallback when the model does not supply a region. It
// reads the TLD and a short list of US retailers. A comparison-site URL is
// unknown, because the seller is not the site.
func regionFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "other"
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" || strings.Contains(host, "prisjakt.") {
		return "other"
	}
	switch {
	case strings.HasSuffix(host, ".no"):
		return "no"
	case strings.HasSuffix(host, ".se"):
		return "se"
	case strings.HasSuffix(host, ".co.uk"), strings.HasSuffix(host, ".uk"):
		return "eu"
	}
	for domain := range usDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return "us"
		}
	}
	if i := strings.LastIndex(host, "."); i >= 0 && euTLDs[host[i+1:]] {
		return "eu"
	}
	return "other"
}

// regionTier orders equal-priced offers: NO/SE first, then EU, then US, then
// everything else. It is a tie-breaker, never a reason to overpay.
func regionTier(region string) int {
	switch region {
	case "no", "se":
		return 1
	case "eu":
		return 2
	case "us":
		return 3
	default:
		return 4
	}
}
