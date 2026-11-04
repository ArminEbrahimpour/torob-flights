package offerurl

import "testing"

func TestFromJSONReadsNestedBookingLink(t *testing.T) {
	raw := []byte(`{"airline":{"url":"https://cdn.example/logo.png"},"prices":[{"bookUrl":"https://www.alibaba.ir/flights/offer/abc"}]}`)
	if got := FromJSON(raw); got != "https://www.alibaba.ir/flights/offer/abc" {
		t.Fatalf("got %s", got)
	}
}

func TestFromJSONIgnoresImagesAndMissingLinks(t *testing.T) {
	if got := FromJSON([]byte(`{"url":"https://cdn.example/logo.svg"}`)); got != "" {
		t.Fatalf("image %s", got)
	}
	if got := FromJSON([]byte(`{"proposalId":"abc"}`)); got != "" {
		t.Fatalf("id %s", got)
	}
}
