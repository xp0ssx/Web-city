package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"web-city/api/internal/store"
)

type InfrastructureHandler struct {
	store *store.Store
}

type infrastructureFeatureProps struct {
	ID               int64    `json:"id"`
	Source           string   `json:"source"`
	SourceDatasetID  int      `json:"source_dataset_id"`
	SourceObjectID   int64    `json:"source_object_id"`
	SourcePointIndex *int     `json:"source_point_index,omitempty"`
	Category         string   `json:"category"`
	Subcategory      string   `json:"subcategory"`
	ObjectType       string   `json:"object_type"`
	Name             string   `json:"name"`
	AreaM2           *float64 `json:"area_m2,omitempty"`
}

type infrastructureGeoJSONFeature struct {
	Type       string                     `json:"type"`
	Geometry   json.RawMessage            `json:"geometry"`
	Properties infrastructureFeatureProps `json:"properties"`
}

type infrastructureGeoJSONFeatureCollection struct {
	Type     string                         `json:"type"`
	Features []infrastructureGeoJSONFeature `json:"features"`
}

func NewInfrastructureHandler(store *store.Store) *InfrastructureHandler {
	return &InfrastructureHandler{store: store}
}

func (h *InfrastructureHandler) Facets(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListInfrastructureFacets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load infrastructure facets")
		return
	}

	writeJSON(w, http.StatusOK, items)
}

func (h *InfrastructureHandler) Objects(w http.ResponseWriter, r *http.Request) {
	filter, ok := infrastructureFilterFromRequest(w, r, 5000)
	if !ok {
		return
	}

	items, err := h.store.ListInfrastructureObjects(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load infrastructure objects")
		return
	}

	writeJSON(w, http.StatusOK, infrastructureFeatureCollection(items))
}

func (h *InfrastructureHandler) Areas(w http.ResponseWriter, r *http.Request) {
	filter, ok := infrastructureFilterFromRequest(w, r, 1000)
	if !ok {
		return
	}

	items, err := h.store.ListInfrastructureAreas(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load infrastructure areas")
		return
	}

	writeJSON(w, http.StatusOK, infrastructureFeatureCollection(items))
}

func infrastructureFilterFromRequest(w http.ResponseWriter, r *http.Request, defaultLimit int) (store.InfrastructureFilter, bool) {
	limit := defaultLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit query parameter")
			return store.InfrastructureFilter{}, false
		}
		limit = parsedLimit
	}

	return store.InfrastructureFilter{
		Source:      strings.TrimSpace(r.URL.Query().Get("source")),
		Category:    strings.TrimSpace(r.URL.Query().Get("category")),
		Subcategory: strings.TrimSpace(r.URL.Query().Get("subcategory")),
		ObjectType:  strings.TrimSpace(r.URL.Query().Get("object_type")),
		Limit:       limit,
	}, true
}

func infrastructureFeatureCollection(items []store.InfrastructureFeatureRow) infrastructureGeoJSONFeatureCollection {
	features := make([]infrastructureGeoJSONFeature, 0, len(items))
	for _, item := range items {
		features = append(features, infrastructureGeoJSONFeature{
			Type:     "Feature",
			Geometry: json.RawMessage(item.GeometryJSON),
			Properties: infrastructureFeatureProps{
				ID:               item.ID,
				Source:           item.Source,
				SourceDatasetID:  item.SourceDatasetID,
				SourceObjectID:   item.SourceObjectID,
				SourcePointIndex: item.SourcePointIndex,
				Category:         item.Category,
				Subcategory:      item.Subcategory,
				ObjectType:       item.ObjectType,
				Name:             item.Name,
				AreaM2:           item.AreaM2,
			},
		})
	}

	return infrastructureGeoJSONFeatureCollection{
		Type:     "FeatureCollection",
		Features: features,
	}
}
