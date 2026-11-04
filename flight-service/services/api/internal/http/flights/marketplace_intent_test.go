package flights

import "testing"

func TestMarketplaceIntentTehranIstanbul(t *testing.T) {
	got := marketplaceIntent("تهران به استانبول")
	if got.Origin != "IKA" || got.Destination != "IST" {
		t.Fatalf("intent=%+v", got)
	}
}

func TestMarketplaceIntentDomesticAndAirline(t *testing.T) {
	got := marketplaceIntent("پرواز تهران به مشهد ماهان")
	if got.Origin != "IKA" || got.Destination != "MHD" || got.Airline != "W5" {
		t.Fatalf("intent=%+v", got)
	}
}

func TestPlaceCodeAcceptsCityName(t *testing.T) {
	if got := placeCode("تهران"); got != "IKA" {
		t.Fatalf("تهران=%s", got)
	}
	if got := placeCode("استانبول"); got != "IST" {
		t.Fatalf("استانبول=%s", got)
	}
	if got := placeCode("ika"); got != "IKA" {
		t.Fatalf("ika=%s", got)
	}
}

func TestMarketplaceIntentImamKhomeini(t *testing.T) {
	got := marketplaceIntent("از فرودگاه امام خمینی به دبی")
	if got.Origin != "IKA" || got.Destination != "DXB" {
		t.Fatalf("intent=%+v", got)
	}
}
