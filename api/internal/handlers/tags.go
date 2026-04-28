package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"web-city/api/internal/store"
)

type TagsHandler struct {
	store *store.Store
}

type geoJSONFeatureCollection struct {
	Type     string           `json:"type"`
	Features []geoJSONFeature `json:"features"`
}

type geoJSONFeature struct {
	Type       string              `json:"type"`
	Geometry   json.RawMessage     `json:"geometry"`
	Properties geoJSONFeatureProps `json:"properties"`
}

type geoJSONFeatureProps struct {
	OSMID       int64           `json:"osm_id"`
	Name        string          `json:"name,omitempty"`
	SourceLayer string          `json:"source_layer"`
	Tags        json.RawMessage `json:"tags,omitempty"`
}

func NewTagsHandler(store *store.Store) *TagsHandler {
	return &TagsHandler{store: store}
}

func (h *TagsHandler) Keys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	items, err := h.store.ListTagKeys(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load tag keys")
		return
	}

	writeJSON(w, http.StatusOK, items)
}

func (h *TagsHandler) Values(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing key query parameter")
		return
	}

	ctx := r.Context()

	items, err := h.store.ListTagValues(ctx, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load tag values")
		return
	}

	writeJSON(w, http.StatusOK, items)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}

func (h *TagsHandler) Features(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing key query parameter")
		return
	}

	value := strings.TrimSpace(r.URL.Query().Get("value"))

	geometry := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("geometry")))
	if geometry == "" {
		geometry = "all"
	}
	switch geometry {
	case "point", "line", "polygon", "all":
	default:
		writeError(w, http.StatusBadRequest, "invalid geometry query parameter")
		return
	}

	limit := 500
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
		limit = parsedLimit
	}

	items, err := h.store.ListFeatures(r.Context(), key, value, geometry, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load features")
		return
	}

	features := make([]geoJSONFeature, 0, len(items))
	for _, item := range items {
		features = append(features, geoJSONFeature{
			Type:     "Feature",
			Geometry: json.RawMessage(item.GeometryJSON),
			Properties: geoJSONFeatureProps{
				OSMID:       item.OSMID,
				Name:        item.Name,
				SourceLayer: item.SourceLayer,
				Tags:        json.RawMessage(item.TagsJSON),
			},
		})
	}

	writeJSON(w, http.StatusOK, geoJSONFeatureCollection{
		Type:     "FeatureCollection",
		Features: features,
	})
}
