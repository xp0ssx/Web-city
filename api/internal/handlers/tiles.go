package handlers

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"web-city/api/internal/store"
)

type TileHandler struct {
	store    *store.Store
	cacheDir string
}

func NewTileHandler(store *store.Store, cacheDir string) *TileHandler {
	return &TileHandler{
		store:    store,
		cacheDir: strings.TrimSpace(cacheDir),
	}
}

func (h *TileHandler) BaseSVG(w http.ResponseWriter, r *http.Request) {
	z, ok := tileParam(w, r, "z")
	if !ok {
		return
	}
	x, ok := tileParam(w, r, "x")
	if !ok {
		return
	}
	y, ok := tileParam(w, r, "y")
	if !ok {
		return
	}

	cachePath := h.cachePath(z, x, y)
	if cachePath != "" {
		if data, err := os.ReadFile(cachePath); err == nil {
			writeSVGBytes(w, data)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	paths, err := h.store.BaseTilePaths(ctx, z, x, y)
	if err != nil {
		if r.Context().Err() == context.Canceled {
			return
		}
		if ctx.Err() == context.DeadlineExceeded {
			w.Header().Set("X-Tile-Warning", "tile query timeout")
			writeSVGBytes(w, baseSVG(nil))
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	data := baseSVG(paths)
	if cachePath != "" {
		if err := writeTileCache(cachePath, data); err != nil {
			log.Printf("tile cache write failed: %v", err)
		}
	}
	writeSVGBytes(w, data)
}

func (h *TileHandler) cachePath(z, x, y int) string {
	if h.cacheDir == "" {
		return ""
	}
	return filepath.Join(h.cacheDir, "base", strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".svg")
}

func writeSVGBytes(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func baseSVG(paths []store.TilePath) []byte {
	var b strings.Builder
	b.Grow(2048 + len(paths)*96)

	_, _ = fmt.Fprint(&b, `<?xml version="1.0" encoding="UTF-8"?>`)
	_, _ = fmt.Fprint(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" width="256" height="256">`)
	_, _ = fmt.Fprint(&b, `<rect width="256" height="256" fill="#eef3f8"/>`)
	_, _ = fmt.Fprint(&b, `<style>
		.water{fill:#b9ddf3;stroke:#8ac6e6;stroke-width:.45}
		.green{fill:#cfe8cf;stroke:#a8d0a8;stroke-width:.35}
		.building{fill:#d9dde4;stroke:#c7ced8;stroke-width:.25}
		.road_motorway,.road_primary,.road_secondary,.road_tertiary,.road_local,.road_service,.road_path,.road_other{fill:none;stroke-linecap:round;stroke-linejoin:round}
		.road_motorway{stroke:#e49f39;stroke-width:2.8}
		.road_primary{stroke:#f1b85f;stroke-width:2.35}
		.road_secondary{stroke:#ffffff;stroke-width:2}
		.road_tertiary{stroke:#ffffff;stroke-width:1.7}
		.road_local{stroke:#ffffff;stroke-width:1.25}
		.road_service{stroke:#ffffff;stroke-width:.95}
		.road_path{stroke:#ffffff;stroke-width:.75;stroke-dasharray:2 2}
		.road_other{stroke:#ffffff;stroke-width:.8}
		.municipality_boundary{fill:none;stroke:#7a8798;stroke-width:.65;stroke-dasharray:3 2}
		.admin_boundary{fill:none;stroke:#334155;stroke-width:1.15}
	</style>`)

	for _, path := range paths {
		_, _ = fmt.Fprintf(&b, `<path class="%s" d="%s"/>`, html.EscapeString(path.Kind), html.EscapeString(path.Path))
	}

	_, _ = fmt.Fprint(&b, `</svg>`)
	return []byte(b.String())
}

func writeTileCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func tileParam(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := chi.URLParam(r, name)
	value, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tile "+name)
		return 0, false
	}
	return value, true
}
