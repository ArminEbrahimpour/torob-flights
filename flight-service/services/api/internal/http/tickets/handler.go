package tickets

import (
	"net/http"

	"github.com/airbar/flight-service/shared/pkg/httpx"
	"github.com/airbar/flight-service/services/api/internal/http/middleware"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) NotImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "not_implemented", "tickets service is not implemented yet", middleware.RequestID(r.Context()), nil)
}
