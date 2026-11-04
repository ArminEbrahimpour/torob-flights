package flights

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	flightdomain "github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/httpx"
	"github.com/airbar/flight-service/shared/pkg/jalali"

	"github.com/airbar/flight-service/services/api/internal/flights"
	"github.com/airbar/flight-service/services/api/internal/http/middleware"
)

type Handler struct {
	service *flights.Service
	maxBody int64
}

func NewHandler(service *flights.Service, maxBody int64) *Handler {
	return &Handler{service: service, maxBody: maxBody}
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := flightdomain.SearchQuery{
		Origin:       placeCode(r.URL.Query().Get("origin")),
		Destination:  placeCode(r.URL.Query().Get("destination")),
		Date:         strings.TrimSpace(r.URL.Query().Get("date")),
		Source:       strings.TrimSpace(r.URL.Query().Get("source")),
		Airline:      strings.TrimSpace(r.URL.Query().Get("airline")),
		FlightNumber: strings.TrimSpace(r.URL.Query().Get("flight_number")),
		Page:         queryInt(r, "page", 1),
		PageSize:     queryInt(r, "page_size", 50),
	}
	if v := r.URL.Query().Get("min_price_toman"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.MinPriceToman = &n
		}
	}
	if v := r.URL.Query().Get("max_price_toman"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.MaxPriceToman = &n
		}
	}
	if v := r.URL.Query().Get("charter"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			q.Charter = &b
		}
	}
	if v := r.URL.Query().Get("max_stops"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.MaxStops = &n
		}
	}
	if q.Source == "" {
		q.Source = "all"
	}

	result, err := h.service.Search(r.Context(), q)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, result)
}

// MarketplaceSearch turns a Persian marketplace query into structured flight
// filters and delegates to the regular service search (Elasticsearch first,
// PostgreSQL fallback). Elasticsearch never becomes browser-facing.
func (h *Handler) MarketplaceSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "q is required")
		return
	}
	intent := marketplaceIntent(query)
	if routePhrase(query) && (intent.Origin == "" || intent.Destination == "") {
		writeData(w, r, http.StatusOK, flightdomain.MarketplaceSearchResult{Query: query, Intent: intent, Clarification: "مبدأ یا مقصد را تشخیص ندادیم؛ لطفاً نام هر دو شهر را وارد کنید."})
		return
	}
	result, err := h.service.Search(r.Context(), flightdomain.SearchQuery{Origin: intent.Origin, Destination: intent.Destination, Date: strings.TrimSpace(r.URL.Query().Get("date")), Airline: intent.Airline, Source: "all", Page: queryInt(r, "page", 1), PageSize: queryInt(r, "page_size", 30)})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "marketplace_search_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, flightdomain.MarketplaceSearchResult{Query: query, Intent: intent, Result: result})
}

func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	var req flightdomain.ValidateRequest
	if err := decodeJSON(w, r, h.maxBody, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.service.Validate(r.Context(), req)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "validate_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (h *Handler) Sources(w http.ResponseWriter, r *http.Request) {
	sources, err := h.service.ListSources(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "sources_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"sources": sources})
}

func (h *Handler) AddRoute(w http.ResponseWriter, r *http.Request) {
	var req flights.AddRouteRequest
	if err := decodeJSON(w, r, h.maxBody, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Origin == "" || req.Destination == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "origin and destination are required")
		return
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	if req.RefreshMinutes == 0 {
		req.RefreshMinutes = 360
	}
	if req.WindowDays == 0 {
		req.WindowDays = 30
	}
	if err := h.service.AddRoute(r.Context(), req.Origin, req.Destination, req.Priority, req.RefreshMinutes, req.WindowDays); err != nil {
		writeError(w, r, http.StatusInternalServerError, "add_route_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) UniqFlightsPerFlightNumNorm(w http.ResponseWriter, r *http.Request) {
	q := flightdomain.UniqueFutureFlightsQuery{
		FlightNumber: strings.TrimSpace(r.URL.Query().Get("flight_number")),
		Page:         queryInt(r, "page", 1),
		PageSize:     queryInt(r, "page_size", 50),
	}
	result, err := h.service.ListUniqueFutureFlights(r.Context(), q)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "unique_flights_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (h *Handler) ValidateByDate(w http.ResponseWriter, r *http.Request) {
	q := flightdomain.FlightByDateQuery{
		FlightNumber: strings.TrimSpace(r.URL.Query().Get("flight_number")),
		Date:         strings.TrimSpace(r.URL.Query().Get("date")),
	}
	if q.FlightNumber == "" || q.Date == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "flight_number and date are required")
		return
	}
	if _, err := jalali.Parse(q.Date); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid date")
		return
	}
	result, err := h.service.ValidateByDate(r.Context(), q)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "flight_by_date_failed", err.Error())
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBody int64, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return errors.New("request body must contain exactly one JSON object")
}

func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	httpx.WriteData(w, status, middleware.RequestID(r.Context()), data)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpx.WriteError(w, status, code, message, middleware.RequestID(r.Context()), nil)
}
