package offerurl

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Keys providers use when an offer includes its own booking page.
var offerLinkKeys = []string{
	"offerUrl", "offerURL", "bookUrl", "bookingUrl", "deepLink", "deeplink",
	"redirectUrl", "redirectURL", "flightUrl", "reserveUrl", "purchaseUrl",
	"detailUrl", "webUrl", "href", "link", "url",
}

// FromJSON returns the booking page embedded in a provider offer, if the
// payload contains one. It does not invent a search URL.
func FromJSON(raw []byte) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return FromValue(value)
}

func FromValue(value any) string {
	switch node := value.(type) {
	case map[string]any:
		for _, key := range offerLinkKeys {
			for name, child := range node {
				if strings.EqualFold(name, key) {
					if link := HTTPLink(stringValue(child)); link != "" {
						return link
					}
				}
			}
		}
		for _, child := range node {
			if link := FromValue(child); link != "" {
				return link
			}
		}
	case []any:
		for _, child := range node {
			if link := FromValue(child); link != "" {
				return link
			}
		}
	}
	return ""
}

func HTTPLink(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	path := strings.ToLower(parsed.Path)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".svg", ".webp", ".gif", ".css", ".js"} {
		if strings.HasSuffix(path, ext) {
			return ""
		}
	}
	return parsed.String()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
