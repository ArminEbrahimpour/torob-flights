package pnr

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/airbar/flight-service/shared/pkg/httpx"

	pnrsvc "github.com/airbar/flight-service/services/api/internal/pnr"
	"github.com/airbar/flight-service/services/api/internal/http/middleware"
	"github.com/airbar/flight-service/services/api/internal/pnr/checkers/mahan"
)

var (
	pnrPattern      = regexp.MustCompile(`^[A-Za-z0-9]{4,12}$`)
	lastNamePattern = regexp.MustCompile(`^[\p{L}][\p{L}\p{M}\-' ]{0,79}$`)
)

type Handler struct {
	service *pnrsvc.Service
	maxBody int64
}

func NewHandler(service *pnrsvc.Service, maxBody int64) *Handler {
	return &Handler{service: service, maxBody: maxBody}
}

func (h *Handler) Airlines(w http.ResponseWriter, r *http.Request) {
	writeData(w, r, http.StatusOK, map[string]any{"airlines": h.service.Airlines()})
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	var input pnrsvc.VerifyRequest
	if err := decodeJSON(w, r, h.maxBody, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if details := validate(input); len(details) > 0 {
		writeErrorDetails(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", details)
		return
	}

	result, err := h.service.Verify(r.Context(), input)
	if err != nil {
		switch {
		case pnrsvc.IsUnsupportedAirline(err):
			writeErrorDetails(w, r, http.StatusNotFound, "unsupported_airline", "no checker is registered for this airline", map[string]any{"airline": input.Airline})
		case mahan.IsUpstreamError(err):
			writeError(w, r, http.StatusBadGateway, "checker_unavailable", "the airline checker is temporarily unavailable")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred")
		}
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func validate(input pnrsvc.VerifyRequest) map[string]any {
	details := make(map[string]any)
	input.Airline = strings.ToLower(strings.TrimSpace(input.Airline))
	input.PNR = strings.ToUpper(strings.TrimSpace(input.PNR))
	input.LastName = strings.TrimSpace(input.LastName)
	input.FlightDate = strings.TrimSpace(input.FlightDate)

	if input.Airline == "" || len(input.Airline) > 40 {
		details["airline"] = "must be a non-empty airline code with at most 40 characters"
	}
	if !pnrPattern.MatchString(input.PNR) {
		details["pnr"] = "must contain 4 to 12 letters or digits"
	}
	if !lastNamePattern.MatchString(input.LastName) {
		details["lastName"] = "must be a valid passenger last name"
	}
	if _, err := time.Parse("2006-01-02", input.FlightDate); err != nil {
		details["flightDate"] = "must use YYYY-MM-DD"
	}
	return details
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

func writeErrorDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	httpx.WriteError(w, status, code, message, middleware.RequestID(r.Context()), details)
}
