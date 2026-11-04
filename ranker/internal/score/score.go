package score

// Offer is the part of a flight that can be compared with the other flights
// on the same route and date. Zero price, zero duration, and negative baggage
// mean the provider did not report that attribute.
type Offer struct {
	PriceToman  int64
	DurationMin int
	Stops       int
	BaggageKG   int
}

// Components are comparable 0–1 scores. 1 is best inside the comparison set.
type Components struct {
	Price    float64 `json:"price"`
	Duration float64 `json:"duration"`
	Stops    float64 `json:"stops"`
	Baggage  float64 `json:"baggage"`
}

// Weights select which attributes decide the order. They are normalized to
// sum to 1, so a user can raise one attribute without rescaling the others.
type Weights struct {
	Price    float64
	Duration float64
	Stops    float64
	Baggage  float64
	Depart   float64
}

func BestValue() Weights { return Weights{Price: 0.4, Duration: 0.3, Stops: 0.2, Baggage: 0.1} }

func ForMode(mode string) Weights {
	switch mode {
	case "cheapest":
		return Weights{Price: 1}
	case "fastest":
		return Weights{Duration: 0.7, Stops: 0.3}
	case "baggage", "best-baggage":
		return Weights{Baggage: 0.7, Price: 0.2, Stops: 0.1}
	default:
		return BestValue()
	}
}

func (w Weights) Total(c Components, departScore float64) float64 {
	sum := w.Price + w.Duration + w.Stops + w.Baggage + w.Depart
	if sum <= 0 {
		w = BestValue()
		sum = 1
	}
	return (c.Price*w.Price + c.Duration*w.Duration + c.Stops*w.Stops + c.Baggage*w.Baggage + departScore*w.Depart) / sum
}

// Group scores every offer against the others in the same slice.
func Group(offers []Offer) []Components {
	out := make([]Components, len(offers))
	price := make([]float64, len(offers))
	duration := make([]float64, len(offers))
	stops := make([]float64, len(offers))
	baggage := make([]float64, len(offers))
	priceKnown := make([]bool, len(offers))
	durationKnown := make([]bool, len(offers))
	baggageKnown := make([]bool, len(offers))
	for i, offer := range offers {
		if offer.PriceToman > 0 {
			price[i] = float64(offer.PriceToman)
			priceKnown[i] = true
		}
		if offer.DurationMin > 0 {
			duration[i] = float64(offer.DurationMin)
			durationKnown[i] = true
		}
		stops[i] = float64(offer.Stops)
		if offer.BaggageKG >= 0 {
			baggage[i] = float64(offer.BaggageKG)
			baggageKnown[i] = true
		}
	}
	priceScore := lowerIsBetter(price, priceKnown)
	durationScore := lowerIsBetter(duration, durationKnown)
	stopsScore := lowerIsBetter(stops, allKnown(len(offers)))
	baggageScore := higherIsBetter(baggage, baggageKnown)
	for i := range out {
		out[i] = Components{Price: priceScore[i], Duration: durationScore[i], Stops: stopsScore[i], Baggage: baggageScore[i]}
	}
	return out
}

// DepartScore is 1 at the preferred minute and 0 twelve hours away.
// Clock time has no global "earlier is better" direction.
func DepartScore(departMinute, preferredMinute int) float64 {
	if departMinute < 0 || preferredMinute < 0 || preferredMinute >= 1440 {
		return 0
	}
	departMinute = ((departMinute % 1440) + 1440) % 1440
	distance := abs(departMinute - preferredMinute)
	if other := 1440 - distance; other < distance {
		distance = other
	}
	return 1 - float64(distance)/720
}

func lowerIsBetter(values []float64, known []bool) []float64 {
	return spread(values, known, false)
}

func higherIsBetter(values []float64, known []bool) []float64 {
	return spread(values, known, true)
}

func spread(values []float64, known []bool, higher bool) []float64 {
	out := make([]float64, len(values))
	min, max := 0.0, 0.0
	seen := false
	for i, value := range values {
		if !known[i] {
			continue
		}
		if !seen || value < min {
			min = value
		}
		if !seen || value > max {
			max = value
		}
		seen = true
	}
	for i := range values {
		if !known[i] {
			continue
		}
		if !seen || max == min {
			out[i] = 1
			continue
		}
		if higher {
			out[i] = (values[i] - min) / (max - min)
			continue
		}
		out[i] = (max - values[i]) / (max - min)
	}
	return out
}

func allKnown(n int) []bool {
	known := make([]bool, n)
	for i := range known {
		known[i] = true
	}
	return known
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
