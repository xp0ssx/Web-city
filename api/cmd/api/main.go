package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"web-city/api/internal/assessment"
	"web-city/api/internal/handlers"
	"web-city/api/internal/store"
)

type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Database  string `json:"database"`
}

func main() {
	port := getEnv("HTTP_PORT", "8080")
	dsn := getEnv("DATABASE_DSN", "postgres://webcity:webcity@localhost:5432/webcity?sslmode=disable")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("db pool init error: %v", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatalf("db ping error: %v", err)
	}

	dbStore := store.New(db)
	tagsHandler := handlers.NewTagsHandler(dbStore)
	infrastructureHandler := handlers.NewInfrastructureHandler(dbStore)
	tileHandler := handlers.NewTileHandler(dbStore, getEnv("TILE_CACHE_DIR", ""))
	assessmentService, err := assessment.NewService(
		dbStore,
		getEnv("ASSESSMENT_CONFIG_FILE", "config/assessment_indicators.json"),
		getEnv("ASSESSMENT_WEIGHTS_FILE", "config/assessment_weights.json"),
	)
	if err != nil {
		log.Fatalf("assessment service init error: %v", err)
	}
	assessmentHandler := handlers.NewAssessmentHandler(assessmentService)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5, "application/json", "image/svg+xml"))
	r.Use(corsMiddleware)

	r.Get("/healthz", healthHandler(db))

	r.Route("/api/v1/tags", func(r chi.Router) {
		r.Get("/keys", tagsHandler.Keys)
		r.Get("/values", tagsHandler.Values)
	})

	r.Get("/api/v1/features", tagsHandler.Features)

	r.Route("/api/v1/infrastructure", func(r chi.Router) {
		r.Get("/facets", infrastructureHandler.Facets)
		r.Get("/objects", infrastructureHandler.Objects)
		r.Get("/areas", infrastructureHandler.Areas)
	})

	r.Route("/api/v1/assessments", func(r chi.Router) {
		r.Get("/config", assessmentHandler.Config)
		r.Post("/evaluate", assessmentHandler.Evaluate)
	})

	r.Get("/api/v1/tiles/base/{z}/{x}/{y}", tileHandler.BaseSVG)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("api listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func healthHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		resp := healthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Database:  "ok",
		}

		if err := db.Ping(ctx); err != nil {
			resp.Status = "degraded"
			resp.Database = "unavailable"
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
