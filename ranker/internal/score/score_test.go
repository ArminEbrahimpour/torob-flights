package score

import "testing"

func TestGroupNormalizesWithinTheRoute(t *testing.T) {
	got := Group([]Offer{
		{PriceToman: 200, DurationMin: 300, Stops: 1, BaggageKG: 20},
		{PriceToman: 100, DurationMin: 100, Stops: 0, BaggageKG: 30},
		{PriceToman: 0, DurationMin: 0, Stops: 2, BaggageKG: -1},
	})
	if got[1].Price != 1 || got[0].Price != 0 || got[2].Price != 0 {
		t.Fatalf("price=%v", got)
	}
	if got[1].Duration != 1 || got[0].Duration != 0 || got[2].Duration != 0 {
		t.Fatalf("duration=%v", got)
	}
	if got[1].Stops != 1 || got[2].Stops != 0 {
		t.Fatalf("stops=%v", got)
	}
	if got[1].Baggage != 1 || got[0].Baggage != 0 || got[2].Baggage != 0 {
		t.Fatalf("baggage=%v", got)
	}
}

func TestEqualValuesStayBest(t *testing.T) {
	got := Group([]Offer{{PriceToman: 100, DurationMin: 90, Stops: 0, BaggageKG: 20}, {PriceToman: 100, DurationMin: 90, Stops: 0, BaggageKG: 20}})
	for _, c := range got {
		if c.Price != 1 || c.Duration != 1 || c.Stops != 1 || c.Baggage != 1 {
			t.Fatalf("equal=%v", c)
		}
	}
}

func TestModeWeights(t *testing.T) {
	cheap := Components{Price: 1, Duration: 0, Stops: 0, Baggage: 0}
	fast := Components{Price: 0, Duration: 1, Stops: 1, Baggage: 0}
	if cheap.Total(ForMode("cheapest"), 0) != 1 {
		t.Fatal("cheapest")
	}
	if fast.Total(ForMode("cheapest"), 0) != 0 {
		t.Fatal("fast is not cheapest")
	}
	if fast.Total(ForMode("fastest"), 0) != 1 {
		t.Fatal("fastest")
	}
	best := Components{Price: 1, Duration: 1, Stops: 1, Baggage: 1}
	if best.Total(BestValue(), 0) != 1 {
		t.Fatal("best value")
	}
}

func TestDepartScorePrefersTheRequestedHour(t *testing.T) {
	if DepartScore(8*60, 8*60) != 1 {
		t.Fatal("exact")
	}
	if DepartScore(8*60, 20*60) != 0 {
		t.Fatal("opposite")
	}
	if DepartScore(23*60, 60) <= DepartScore(12*60, 60) {
		t.Fatal("midnight is closer to 01:00 than noon")
	}
}

func (c Components) Total(w Weights, depart float64) float64 { return w.Total(c, depart) }
