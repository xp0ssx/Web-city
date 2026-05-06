package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"web-city/api/internal/store"
)

type PresetHandler struct {
	store *store.Store
}

type presetRequest struct {
	Name    string          `json:"name"`
	Profile string          `json:"profile"`
	Weights json.RawMessage `json:"weights"`
}

func NewPresetHandler(store *store.Store) *PresetHandler {
	return &PresetHandler{store: store}
}

func (h *PresetHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := CurrentUser(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	items, err := h.store.ListWeightPresets(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load weight presets")
		return
	}
	writeJSON(w, http.StatusOK, map[string][]store.WeightPreset{
		"items": items,
	})
}

func (h *PresetHandler) Save(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	user, ok := CurrentUser(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req presetRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}

	item, err := h.store.CreateWeightPreset(r.Context(), user.ID, req.Name, req.Profile, req.Weights)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *PresetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := CurrentUser(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	presetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || presetID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid preset id")
		return
	}

	if err := h.store.DeleteWeightPreset(r.Context(), user.ID, presetID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "weight preset not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete weight preset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
