package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"web-city/api/internal/assessment"
)

type AssessmentHandler struct {
	service *assessment.Service
}

func NewAssessmentHandler(service *assessment.Service) *AssessmentHandler {
	return &AssessmentHandler{service: service}
}

func (h *AssessmentHandler) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.service.ConfigResponse())
}

func (h *AssessmentHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var req assessment.EvaluateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}

	result, err := h.service.Evaluate(r.Context(), req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		var requestErr assessment.RequestError
		if errors.As(err, &requestErr) {
			writeError(w, http.StatusBadRequest, requestErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to evaluate assessment")
		return
	}

	writeJSON(w, http.StatusOK, result)
}
