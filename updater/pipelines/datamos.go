package pipelines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DataMosConfig struct {
	Datasets []DataMosDatasetConfig `json:"datasets"`
}

type DataMosDatasetConfig struct {
	DatasetID    int      `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Subcategory  string   `json:"subcategory"`
	GeometryMode string   `json:"geometry_mode"`
	Projection   []string `json:"projection"`
	NameField    string   `json:"name_field"`
}

type FeatureCollection struct {
	Features []Feature `json:"features"`
}

type Feature struct {
	Geometry   Geometry   `json:"geometry"`
	Properties Properties `json:"properties"`
}

type Geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type Properties struct {
	DatasetID     int                        `json:"datasetId"`
	ReleaseNumber int                        `json:"releaseNumber"`
	VersionNumber int                        `json:"versionNumber"`
	Attributes    map[string]json.RawMessage `json:"attributes"`
}

type ExtractedObject struct {
	Source         string
	DatasetID      int
	SourceGlobalID int64
	PointIndex     int
	Category       string
	Subcategory    string
	Name           string
	Lon            float64
	Lat            float64
}

type ExtractedArea struct {
	Source         string
	DatasetID      int
	SourceGlobalID int64
	Category       string
	Subcategory    string
	Name           string
	GeometryType   string
	GeometryJSON   string
}

type extractionStats struct {
	MissingGlobalID     int
	BadGlobalID         int
	MissingName         int
	MissingGeometry     int
	UnsupportedGeometry int
	BadGeometry         int
	SwappedCoordinates  int
}

func (s *extractionStats) Add(other extractionStats) {
	s.MissingGlobalID += other.MissingGlobalID
	s.BadGlobalID += other.BadGlobalID
	s.MissingName += other.MissingName
	s.MissingGeometry += other.MissingGeometry
	s.UnsupportedGeometry += other.UnsupportedGeometry
	s.BadGeometry += other.BadGeometry
	s.SwappedCoordinates += other.SwappedCoordinates
}

type geoPoint struct {
	Lon float64
	Lat float64
}

const (
	dataMosMaxAttempts       = 4
	dataMosInitialRetryDelay = 2 * time.Second
	dataMosMaxRetryDelay     = 15 * time.Second

	dataMosGeometryModePoint = "point"
	dataMosGeometryModeArea  = "area"
)

func RunDataMos() error {
	apiKey := strings.TrimSpace(os.Getenv("DATAMOS_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("DATAMOS_API_KEY is required")
	}

	baseURL := strings.TrimSpace(os.Getenv("DATAMOS_BASE_URL"))
	if baseURL == "" {
		return fmt.Errorf("DATAMOS_BASE_URL is required")
	}

	configPath := getStringEnv("DATAMOS_CONFIG_FILE", "config/datamos_datasets.json")
	cfg, err := loadDataMosConfig(configPath)
	if err != nil {
		return err
	}

	datasets, err := selectedDatasets(cfg, strings.TrimSpace(os.Getenv("DATAMOS_DATASET_ID")))
	if err != nil {
		return err
	}

	limit := getIntEnv("DATAMOS_PAGE_LIMIT", 100)
	if limit <= 0 {
		limit = 100
	}

	offset := getIntEnv("DATAMOS_PAGE_OFFSET", 1)
	if offset < 1 {
		offset = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db, err := pgxpool.New(ctx, databaseDSN())
	if err != nil {
		return fmt.Errorf("db pool init failed: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}

	totalSaved := 0
	for _, dataset := range datasets {
		saved, err := importDataMosDataset(ctx, db, client, baseURL, apiKey, dataset, limit, offset)
		if err != nil {
			return err
		}
		totalSaved += saved
	}

	log.Printf("DataMos import completed: datasets=%d, saved_rows=%d", len(datasets), totalSaved)
	return nil
}

func importDataMosDataset(
	ctx context.Context,
	db *pgxpool.Pool,
	client *http.Client,
	baseURL string,
	apiKey string,
	cfg DataMosDatasetConfig,
	limit int,
	offset int,
) (int, error) {
	requestBody, err := json.Marshal(cfg.Projection)
	if err != nil {
		return 0, fmt.Errorf("projection encode failed for dataset %d: %w", cfg.DatasetID, err)
	}

	endpoint := fmt.Sprintf("%s/datasets/%d/features", strings.TrimRight(baseURL, "/"), cfg.DatasetID)

	currentOffset := offset
	totalFeatures := 0
	allObjects := make([]ExtractedObject, 0)
	allAreas := make([]ExtractedArea, 0)
	var totalStats extractionStats

	for {
		collection, err := fetchDataMosPage(ctx, client, endpoint, apiKey, requestBody, cfg.DatasetID, limit, currentOffset)
		if err != nil {
			return 0, err
		}

		pageFeatures := len(collection.Features)
		totalFeatures += pageFeatures

		extractedItems := 0
		if cfg.geometryMode() == dataMosGeometryModeArea {
			areas, stats := extractAreas(collection, cfg)
			allAreas = append(allAreas, areas...)
			totalStats.Add(stats)
			extractedItems = len(areas)
		} else {
			objects, stats := extractObjects(collection, cfg)
			allObjects = append(allObjects, objects...)
			totalStats.Add(stats)
			extractedItems = len(objects)
		}

		log.Printf(
			"DataMos page: dataset=%d, mode=%s, skip=%d, top=%d, features=%d, extracted_items=%d",
			cfg.DatasetID,
			cfg.geometryMode(),
			currentOffset,
			limit,
			pageFeatures,
			extractedItems,
		)

		if pageFeatures < limit {
			break
		}

		currentOffset += limit
	}

	if cfg.geometryMode() == dataMosGeometryModeArea {
		logDataMosStats(cfg, totalFeatures, len(allAreas), totalStats)
		for i, area := range firstAreas(allAreas, 5) {
			log.Printf(
				"[%d] global_id=%d, category=%s, subcategory=%s, name=%s, geom_type=%s",
				i,
				area.SourceGlobalID,
				area.Category,
				area.Subcategory,
				area.Name,
				area.GeometryType,
			)
		}

		if err := saveAreas(ctx, db, allAreas); err != nil {
			return 0, err
		}

		log.Printf("DataMos saved: dataset=%d, table=infrastructure_areas, rows=%d", cfg.DatasetID, len(allAreas))
		return len(allAreas), nil
	}

	logDataMosStats(cfg, totalFeatures, len(allObjects), totalStats)

	for i, obj := range firstObjects(allObjects, 5) {
		log.Printf(
			"[%d] global_id=%d, category=%s, subcategory=%s, name=%s, lon=%.9f, lat=%.9f, point_index=%d",
			i,
			obj.SourceGlobalID,
			obj.Category,
			obj.Subcategory,
			obj.Name,
			obj.Lon,
			obj.Lat,
			obj.PointIndex,
		)
	}

	if err := saveObjects(ctx, db, allObjects); err != nil {
		return 0, err
	}

	log.Printf("DataMos saved: dataset=%d, table=infrastructure_objects, rows=%d", cfg.DatasetID, len(allObjects))
	return len(allObjects), nil
}

func logDataMosStats(cfg DataMosDatasetConfig, totalFeatures int, extractedItems int, stats extractionStats) {
	log.Printf(
		"DataMos dataset %d (%s): fetched %d features, extracted %d items, mode=%s, category=%s, subcategory=%s",
		cfg.DatasetID,
		cfg.Name,
		totalFeatures,
		extractedItems,
		cfg.geometryMode(),
		cfg.Category,
		cfg.Subcategory,
	)
	log.Printf(
		"DataMos summary: missing_global_id=%d, bad_global_id=%d, missing_name=%d, missing_geometry=%d, unsupported_geometry=%d, bad_geometry=%d, swapped_coordinates=%d",
		stats.MissingGlobalID,
		stats.BadGlobalID,
		stats.MissingName,
		stats.MissingGeometry,
		stats.UnsupportedGeometry,
		stats.BadGeometry,
		stats.SwappedCoordinates,
	)
}

func loadDataMosConfig(path string) (DataMosConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return DataMosConfig{}, fmt.Errorf("open DataMos config failed: %w", err)
	}
	defer file.Close()

	var cfg DataMosConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return DataMosConfig{}, fmt.Errorf("decode DataMos config failed: %w", err)
	}

	if len(cfg.Datasets) == 0 {
		return DataMosConfig{}, fmt.Errorf("DataMos config has no datasets")
	}

	seen := make(map[int]struct{}, len(cfg.Datasets))
	for _, dataset := range cfg.Datasets {
		if _, exists := seen[dataset.DatasetID]; exists {
			return DataMosConfig{}, fmt.Errorf("duplicate DataMos dataset config for dataset_id=%d", dataset.DatasetID)
		}
		seen[dataset.DatasetID] = struct{}{}
		if err := validateDatasetConfig(dataset); err != nil {
			return DataMosConfig{}, err
		}
	}

	return cfg, nil
}

func selectedDatasets(cfg DataMosConfig, requested string) ([]DataMosDatasetConfig, error) {
	if requested == "" || strings.EqualFold(requested, "all") {
		return cfg.Datasets, nil
	}

	datasetID, err := strconv.Atoi(requested)
	if err != nil {
		return nil, fmt.Errorf("invalid DATAMOS_DATASET_ID=%q (use integer id or all)", requested)
	}

	for _, dataset := range cfg.Datasets {
		if dataset.DatasetID == datasetID {
			return []DataMosDatasetConfig{dataset}, nil
		}
	}

	return nil, fmt.Errorf("missing DataMos dataset config for dataset_id=%d", datasetID)
}

func (cfg DataMosDatasetConfig) geometryMode() string {
	mode := strings.TrimSpace(cfg.GeometryMode)
	if mode == "" {
		return dataMosGeometryModePoint
	}
	return mode
}

func validateDatasetConfig(cfg DataMosDatasetConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("DataMos dataset %d config has empty name", cfg.DatasetID)
	}
	if strings.TrimSpace(cfg.Category) == "" {
		return fmt.Errorf("DataMos dataset %d config has empty category", cfg.DatasetID)
	}
	if strings.TrimSpace(cfg.Subcategory) == "" {
		return fmt.Errorf("DataMos dataset %d config has empty subcategory", cfg.DatasetID)
	}
	if len(cfg.Projection) == 0 {
		return fmt.Errorf("DataMos dataset %d config has empty projection", cfg.DatasetID)
	}
	if strings.TrimSpace(cfg.NameField) == "" {
		return fmt.Errorf("DataMos dataset %d config has empty name_field", cfg.DatasetID)
	}
	switch cfg.geometryMode() {
	case dataMosGeometryModePoint, dataMosGeometryModeArea:
	default:
		return fmt.Errorf("DataMos dataset %d has invalid geometry_mode=%q", cfg.DatasetID, cfg.GeometryMode)
	}
	if !containsString(cfg.Projection, "global_id") {
		return fmt.Errorf("DataMos dataset %d projection must include global_id", cfg.DatasetID)
	}
	if !containsString(cfg.Projection, cfg.NameField) {
		return fmt.Errorf("DataMos dataset %d name field %q is missing from projection", cfg.DatasetID, cfg.NameField)
	}
	return nil
}

func fetchDataMosPage(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	apiKey string,
	requestBody []byte,
	datasetID int,
	limit int,
	offset int,
) (FeatureCollection, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return FeatureCollection{}, fmt.Errorf("invalid endpoint url: %w", err)
	}

	q := u.Query()
	q.Set("$top", strconv.Itoa(limit))
	q.Set("$skip", strconv.Itoa(offset))
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()

	var lastErr error
	for attempt := 1; attempt <= dataMosMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(requestBody))
		if err != nil {
			return FeatureCollection{}, fmt.Errorf("request build failed: %w", err)
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: dataset=%d, skip=%d, top=%d, attempt=%d/%d: %w", datasetID, offset, limit, attempt, dataMosMaxAttempts, err)
			if retryAllowed(ctx, attempt, lastErr) {
				continue
			}
			return FeatureCollection{}, lastErr
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read failed: dataset=%d, skip=%d, top=%d, attempt=%d/%d: %w", datasetID, offset, limit, attempt, dataMosMaxAttempts, err)
			if retryAllowed(ctx, attempt, lastErr) {
				continue
			}
			return FeatureCollection{}, lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(body), 1000))
			if isRetryableStatus(resp.StatusCode) && retryAllowed(ctx, attempt, lastErr) {
				continue
			}
			return FeatureCollection{}, fmt.Errorf("DataMos request failed: dataset=%d, skip=%d, top=%d: %w", datasetID, offset, limit, lastErr)
		}

		var collection FeatureCollection
		if err := json.Unmarshal(body, &collection); err != nil {
			return FeatureCollection{}, fmt.Errorf("invalid json: dataset=%d, skip=%d, top=%d: %w; body: %s", datasetID, offset, limit, err, truncate(string(body), 500))
		}

		return collection, nil
	}

	return FeatureCollection{}, lastErr
}

func extractObjects(collection FeatureCollection, cfg DataMosDatasetConfig) ([]ExtractedObject, extractionStats) {
	var stats extractionStats
	objects := make([]ExtractedObject, 0, len(collection.Features))

	for _, feature := range collection.Features {
		globalID, ok := globalID(feature.Properties.Attributes, &stats)
		if !ok {
			continue
		}

		name := configuredName(feature.Properties.Attributes, cfg.NameField)
		if name == "" {
			stats.MissingName++
			continue
		}

		points, ok := geometryPoints(feature.Geometry, &stats)
		if !ok {
			continue
		}

		for pointIndex, point := range points {
			objects = append(objects, ExtractedObject{
				Source:         "datamos",
				DatasetID:      cfg.DatasetID,
				SourceGlobalID: globalID,
				PointIndex:     pointIndex,
				Category:       cfg.Category,
				Subcategory:    cfg.Subcategory,
				Name:           name,
				Lon:            point.Lon,
				Lat:            point.Lat,
			})
		}
	}

	return objects, stats
}

func extractAreas(collection FeatureCollection, cfg DataMosDatasetConfig) ([]ExtractedArea, extractionStats) {
	var stats extractionStats
	areas := make([]ExtractedArea, 0, len(collection.Features))

	for _, feature := range collection.Features {
		globalID, ok := globalID(feature.Properties.Attributes, &stats)
		if !ok {
			continue
		}

		name := configuredName(feature.Properties.Attributes, cfg.NameField)
		if name == "" {
			stats.MissingName++
			continue
		}

		geometryJSON, ok := areaGeometryJSON(feature.Geometry, &stats)
		if !ok {
			continue
		}

		areas = append(areas, ExtractedArea{
			Source:         "datamos",
			DatasetID:      cfg.DatasetID,
			SourceGlobalID: globalID,
			Category:       cfg.Category,
			Subcategory:    cfg.Subcategory,
			Name:           name,
			GeometryType:   feature.Geometry.Type,
			GeometryJSON:   geometryJSON,
		})
	}

	return areas, stats
}

func saveObjects(ctx context.Context, db *pgxpool.Pool, objects []ExtractedObject) error {
	if len(objects) == 0 {
		return nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db transaction begin failed: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, obj := range objects {
		_, err := tx.Exec(ctx, `
			INSERT INTO infrastructure_objects (
				source,
				source_dataset_id,
				source_object_id,
				source_point_index,
				category,
				subcategory,
				name,
				geom,
				updated_at
			)
			VALUES (
				$1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				ST_SetSRID(ST_MakePoint($8, $9), 4326),
				now()
			)
			ON CONFLICT (source, source_dataset_id, source_object_id, source_point_index)
			DO UPDATE SET
				category = EXCLUDED.category,
				subcategory = EXCLUDED.subcategory,
				name = EXCLUDED.name,
				geom = EXCLUDED.geom,
				updated_at = now()
		`,
			obj.Source,
			obj.DatasetID,
			obj.SourceGlobalID,
			obj.PointIndex,
			obj.Category,
			obj.Subcategory,
			obj.Name,
			obj.Lon,
			obj.Lat,
		)
		if err != nil {
			return fmt.Errorf("save infrastructure object failed: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db transaction commit failed: %w", err)
	}

	return nil
}

func saveAreas(ctx context.Context, db *pgxpool.Pool, areas []ExtractedArea) error {
	if len(areas) == 0 {
		return nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db transaction begin failed: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, area := range areas {
		_, err := tx.Exec(ctx, `
			WITH incoming AS (
				SELECT
					$1::text AS source,
					$2::integer AS source_dataset_id,
					$3::bigint AS source_object_id,
					$4::text AS category,
					$5::text AS subcategory,
					$6::text AS name,
					ST_Multi(
						ST_CollectionExtract(
							ST_MakeValid(
								ST_SetSRID(ST_GeomFromGeoJSON($7), 4326)
							),
							3
						)
					) AS geom
			)
			INSERT INTO infrastructure_areas (
				source,
				source_dataset_id,
				source_object_id,
				category,
				subcategory,
				name,
				geom,
				area_m2,
				updated_at
			)
			SELECT
				source,
				source_dataset_id,
				source_object_id,
				category,
				subcategory,
				name,
				geom,
				ST_Area(geom::geography),
				now()
			FROM incoming
			ON CONFLICT (source, source_dataset_id, source_object_id)
			DO UPDATE SET
				category = EXCLUDED.category,
				subcategory = EXCLUDED.subcategory,
				name = EXCLUDED.name,
				geom = EXCLUDED.geom,
				area_m2 = EXCLUDED.area_m2,
				updated_at = now()
		`,
			area.Source,
			area.DatasetID,
			area.SourceGlobalID,
			area.Category,
			area.Subcategory,
			area.Name,
			area.GeometryJSON,
		)
		if err != nil {
			return fmt.Errorf("save infrastructure area failed: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db transaction commit failed: %w", err)
	}

	return nil
}

func globalID(attributes map[string]json.RawMessage, stats *extractionStats) (int64, bool) {
	raw, ok := attributes["global_id"]
	if !ok || len(raw) == 0 {
		stats.MissingGlobalID++
		return 0, false
	}

	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, true
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err == nil {
			return parsed, true
		}
	}

	stats.BadGlobalID++
	return 0, false
}

func configuredName(attributes map[string]json.RawMessage, field string) string {
	raw, ok := attributes[field]
	if !ok || len(raw) == 0 {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	return strings.TrimSpace(value)
}

func areaGeometryJSON(geometry Geometry, stats *extractionStats) (string, bool) {
	if len(geometry.Coordinates) == 0 {
		stats.MissingGeometry++
		return "", false
	}

	switch geometry.Type {
	case "Polygon", "MultiPolygon":
	default:
		stats.UnsupportedGeometry++
		return "", false
	}

	data, err := json.Marshal(geometry)
	if err != nil {
		stats.BadGeometry++
		return "", false
	}

	return string(data), true
}

func geometryPoints(geometry Geometry, stats *extractionStats) ([]geoPoint, bool) {
	if len(geometry.Coordinates) == 0 {
		stats.MissingGeometry++
		return nil, false
	}

	switch geometry.Type {
	case "Point":
		var coords [2]float64
		if err := json.Unmarshal(geometry.Coordinates, &coords); err != nil {
			stats.BadGeometry++
			return nil, false
		}
		return []geoPoint{normalizedMoscowPoint(geoPoint{Lon: coords[0], Lat: coords[1]}, stats)}, true

	case "MultiPoint":
		var coords [][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &coords); err != nil {
			stats.BadGeometry++
			return nil, false
		}
		if len(coords) == 0 {
			stats.MissingGeometry++
			return nil, false
		}

		points := make([]geoPoint, 0, len(coords))
		for _, coord := range coords {
			points = append(points, normalizedMoscowPoint(geoPoint{Lon: coord[0], Lat: coord[1]}, stats))
		}
		return points, true

	case "Polygon":
		var polygon [][][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &polygon); err != nil {
			stats.BadGeometry++
			return nil, false
		}

		point, ok := polygonRepresentativePoint(polygon)
		if !ok {
			stats.BadGeometry++
			return nil, false
		}
		return []geoPoint{normalizedMoscowPoint(point, stats)}, true

	case "MultiPolygon":
		var polygons [][][][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &polygons); err != nil {
			stats.BadGeometry++
			return nil, false
		}
		if len(polygons) == 0 {
			stats.MissingGeometry++
			return nil, false
		}

		points := make([]geoPoint, 0, len(polygons))
		for _, polygon := range polygons {
			point, ok := polygonRepresentativePoint(polygon)
			if !ok {
				stats.BadGeometry++
				return nil, false
			}
			points = append(points, normalizedMoscowPoint(point, stats))
		}
		return points, true

	default:
		stats.UnsupportedGeometry++
		return nil, false
	}
}

func polygonRepresentativePoint(polygon [][][2]float64) (geoPoint, bool) {
	if len(polygon) == 0 {
		return geoPoint{}, false
	}

	return ringRepresentativePoint(polygon[0])
}

func ringRepresentativePoint(ring [][2]float64) (geoPoint, bool) {
	ring = withoutClosingCoordinate(ring)
	if len(ring) == 0 {
		return geoPoint{}, false
	}

	if len(ring) >= 3 {
		if point, ok := ringCentroid(ring); ok {
			return point, true
		}
	}

	return averageRingPoint(ring), true
}

func withoutClosingCoordinate(ring [][2]float64) [][2]float64 {
	if len(ring) < 2 {
		return ring
	}

	first := ring[0]
	last := ring[len(ring)-1]
	if first[0] == last[0] && first[1] == last[1] {
		return ring[:len(ring)-1]
	}

	return ring
}

func ringCentroid(ring [][2]float64) (geoPoint, bool) {
	var areaSum float64
	var lonSum float64
	var latSum float64

	for i := range ring {
		current := ring[i]
		next := ring[(i+1)%len(ring)]
		cross := current[0]*next[1] - next[0]*current[1]
		areaSum += cross
		lonSum += (current[0] + next[0]) * cross
		latSum += (current[1] + next[1]) * cross
	}

	if areaSum == 0 {
		return geoPoint{}, false
	}

	return geoPoint{
		Lon: lonSum / (3 * areaSum),
		Lat: latSum / (3 * areaSum),
	}, true
}

func averageRingPoint(ring [][2]float64) geoPoint {
	var lonSum float64
	var latSum float64

	for _, coord := range ring {
		lonSum += coord[0]
		latSum += coord[1]
	}

	count := float64(len(ring))
	return geoPoint{
		Lon: lonSum / count,
		Lat: latSum / count,
	}
}

func normalizedMoscowPoint(point geoPoint, stats *extractionStats) geoPoint {
	if looksLikeSwappedMoscowPoint(point) {
		stats.SwappedCoordinates++
		return geoPoint{
			Lon: point.Lat,
			Lat: point.Lon,
		}
	}

	return point
}

func looksLikeSwappedMoscowPoint(point geoPoint) bool {
	return point.Lon >= 54 && point.Lon <= 57 && point.Lat >= 36 && point.Lat <= 39
}

func retryAllowed(ctx context.Context, attempt int, err error) bool {
	if attempt >= dataMosMaxAttempts {
		return false
	}

	delay := dataMosRetryDelay(attempt)
	log.Printf(
		"DataMos retry: attempt=%d/%d failed: %v; retry_in=%s",
		attempt,
		dataMosMaxAttempts,
		err,
		delay,
	)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func dataMosRetryDelay(failedAttempt int) time.Duration {
	delay := dataMosInitialRetryDelay
	for i := 1; i < failedAttempt; i++ {
		delay *= 2
		if delay >= dataMosMaxRetryDelay {
			return dataMosMaxRetryDelay
		}
	}
	return delay
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func getIntEnv(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getStringEnv(key string, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func databaseDSN() string {
	host := getStringEnv("DB_HOST", "localhost")
	port := getStringEnv("DB_PORT", "5432")
	dbName := getStringEnv("DB_NAME", "webcity")
	user := getStringEnv("DB_USER", "webcity")
	password := getStringEnv("DB_PASSWORD", "webcity")

	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + dbName,
	}

	q := dsn.Query()
	q.Set("sslmode", "disable")
	dsn.RawQuery = q.Encode()

	return dsn.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func firstObjects(objects []ExtractedObject, limit int) []ExtractedObject {
	if len(objects) <= limit {
		return objects
	}
	return objects[:limit]
}

func firstAreas(areas []ExtractedArea, limit int) []ExtractedArea {
	if len(areas) <= limit {
		return areas
	}
	return areas[:limit]
}
