package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

type InfrastructureSelector struct {
	Category    string
	Subcategory string
	ObjectTypes []string
}

type MunicipalityContext struct {
	Name               string   `json:"name"`
	ParentName         string   `json:"parent_name,omitempty"`
	TypeMO             string   `json:"type_mo,omitempty"`
	PriceRubM2         *int     `json:"price_rub_m2,omitempty"`
	YieldVsBankDeposit *float64 `json:"yield_vs_bank_deposit,omitempty"`
}

type NearestInfrastructureObject struct {
	ID               int64   `json:"id"`
	Source           string  `json:"source"`
	SourceDatasetID  int     `json:"source_dataset_id"`
	SourceObjectID   int64   `json:"source_object_id"`
	SourcePointIndex int     `json:"source_point_index"`
	Category         string  `json:"category"`
	Subcategory      string  `json:"subcategory"`
	ObjectType       string  `json:"object_type"`
	Name             string  `json:"name"`
	Lon              float64 `json:"lon"`
	Lat              float64 `json:"lat"`
	DistanceM        float64 `json:"distance_m"`
}

type InfrastructureAreaIntersection struct {
	ID              int64           `json:"id"`
	Source          string          `json:"source"`
	SourceDatasetID int             `json:"source_dataset_id"`
	SourceObjectID  int64           `json:"source_object_id"`
	Category        string          `json:"category"`
	Subcategory     string          `json:"subcategory"`
	ObjectType      string          `json:"object_type"`
	Name            string          `json:"name"`
	AreaM2          float64         `json:"area_m2"`
	IntersectionM2  float64         `json:"intersection_m2"`
	GeometryJSON    json.RawMessage `json:"geometry"`
}

func (s *Store) FindMunicipalityByPoint(ctx context.Context, lon, lat float64) (*MunicipalityContext, error) {
	row := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT ST_SetSRID(ST_Point($1, $2), 4326) AS geom
		)
		SELECT
			a.name,
			COALESCE(a.parent_name, '') AS parent_name,
			COALESCE(a.type_mo, '') AS type_mo,
			COALESCE(m.price_rub_m2, 0) AS price_rub_m2,
			m.price_rub_m2 IS NOT NULL AS has_price_rub_m2,
			COALESCE(m.yield_vs_bank_deposit::float8, 0) AS yield_vs_bank_deposit,
			m.yield_vs_bank_deposit IS NOT NULL AS has_yield_vs_bank_deposit
		FROM administrative_areas a
		CROSS JOIN target t
		LEFT JOIN district_economic_metrics m
			ON m.municipality_name = a.name
		WHERE a.area_type = 'municipality'
			AND ST_Covers(a.geom, t.geom)
		ORDER BY ST_Area(a.geom::geography)
		LIMIT 1
	`, lon, lat)

	var result MunicipalityContext
	var price int
	var hasPrice bool
	var yield float64
	var hasYield bool
	if err := row.Scan(
		&result.Name,
		&result.ParentName,
		&result.TypeMO,
		&price,
		&hasPrice,
		&yield,
		&hasYield,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if hasPrice {
		result.PriceRubM2 = &price
	}
	if hasYield {
		result.YieldVsBankDeposit = &yield
	}

	return &result, nil
}

func (s *Store) NearestInfrastructureObject(ctx context.Context, lon, lat float64, selector InfrastructureSelector) (*NearestInfrastructureObject, error) {
	selector = normalizeInfrastructureSelector(selector)

	row := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT
				ST_SetSRID(ST_Point($1, $2), 4326) AS geom,
				ST_SetSRID(ST_Point($1, $2), 4326)::geography AS geog
		)
		SELECT
			o.id,
			o.source,
			o.source_dataset_id,
			o.source_object_id,
			o.source_point_index,
			o.category,
			o.subcategory,
			o.object_type,
			o.name,
			ST_X(o.geom) AS lon,
			ST_Y(o.geom) AS lat,
			ST_Distance(o.geom::geography, t.geog) AS distance_m
		FROM infrastructure_objects o
		CROSS JOIN target t
		WHERE ($3 = '' OR o.category = $3)
			AND ($4 = '' OR o.subcategory = $4)
			AND (cardinality($5::text[]) = 0 OR o.object_type = ANY($5::text[]))
		ORDER BY o.geom <-> t.geom
		LIMIT 1
	`,
		lon,
		lat,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
	)

	var result NearestInfrastructureObject
	if err := row.Scan(
		&result.ID,
		&result.Source,
		&result.SourceDatasetID,
		&result.SourceObjectID,
		&result.SourcePointIndex,
		&result.Category,
		&result.Subcategory,
		&result.ObjectType,
		&result.Name,
		&result.Lon,
		&result.Lat,
		&result.DistanceM,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &result, nil
}

func (s *Store) CountInfrastructureObjectsInRadius(ctx context.Context, lon, lat, radiusM float64, selector InfrastructureSelector) (int64, error) {
	selector = normalizeInfrastructureSelector(selector)

	var count int64
	if err := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT ST_SetSRID(ST_Point($1, $2), 4326)::geography AS geog
		)
		SELECT COUNT(*)
		FROM infrastructure_objects o
		CROSS JOIN target t
		WHERE ($4 = '' OR o.category = $4)
			AND ($5 = '' OR o.subcategory = $5)
			AND (cardinality($6::text[]) = 0 OR o.object_type = ANY($6::text[]))
			AND ST_DWithin(o.geom::geography, t.geog, $3)
	`,
		lon,
		lat,
		radiusM,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
	).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (s *Store) InfrastructureObjectsInRadius(ctx context.Context, lon, lat, radiusM float64, selector InfrastructureSelector, limit int) ([]NearestInfrastructureObject, error) {
	selector = normalizeInfrastructureSelector(selector)
	limit = normalizeAssessmentFeatureLimit(limit, 100, 500)

	rows, err := s.db.Query(ctx, `
		WITH target AS (
			SELECT
				ST_SetSRID(ST_Point($1, $2), 4326) AS geom,
				ST_SetSRID(ST_Point($1, $2), 4326)::geography AS geog
		)
		SELECT
			o.id,
			o.source,
			o.source_dataset_id,
			o.source_object_id,
			o.source_point_index,
			o.category,
			o.subcategory,
			o.object_type,
			o.name,
			ST_X(o.geom) AS lon,
			ST_Y(o.geom) AS lat,
			ST_Distance(o.geom::geography, t.geog) AS distance_m
		FROM infrastructure_objects o
		CROSS JOIN target t
		WHERE ($4 = '' OR o.category = $4)
			AND ($5 = '' OR o.subcategory = $5)
			AND (cardinality($6::text[]) = 0 OR o.object_type = ANY($6::text[]))
			AND ST_DWithin(o.geom::geography, t.geog, $3)
		ORDER BY o.geom <-> t.geom
		LIMIT $7
	`,
		lon,
		lat,
		radiusM,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]NearestInfrastructureObject, 0, limit)
	for rows.Next() {
		var item NearestInfrastructureObject
		if err := rows.Scan(
			&item.ID,
			&item.Source,
			&item.SourceDatasetID,
			&item.SourceObjectID,
			&item.SourcePointIndex,
			&item.Category,
			&item.Subcategory,
			&item.ObjectType,
			&item.Name,
			&item.Lon,
			&item.Lat,
			&item.DistanceM,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Store) CountDistinctInfrastructureObjectTypesInRadius(ctx context.Context, lon, lat, radiusM float64, selector InfrastructureSelector) (int64, error) {
	selector = normalizeInfrastructureSelector(selector)

	var count int64
	if err := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT ST_SetSRID(ST_Point($1, $2), 4326)::geography AS geog
		)
		SELECT COUNT(DISTINCT o.object_type)
		FROM infrastructure_objects o
		CROSS JOIN target t
		WHERE ($4 = '' OR o.category = $4)
			AND ($5 = '' OR o.subcategory = $5)
			AND (cardinality($6::text[]) = 0 OR o.object_type = ANY($6::text[]))
			AND ST_DWithin(o.geom::geography, t.geog, $3)
	`,
		lon,
		lat,
		radiusM,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
	).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (s *Store) InfrastructureAreaIntersectionM2(ctx context.Context, lon, lat, radiusM float64, selector InfrastructureSelector) (float64, error) {
	selector = normalizeInfrastructureSelector(selector)

	var areaM2 float64
	if err := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT ST_Buffer(ST_SetSRID(ST_Point($1, $2), 4326)::geography, $3)::geometry AS geom
		)
		SELECT COALESCE(SUM(ST_Area(ST_Intersection(a.geom, t.geom)::geography)), 0)
		FROM infrastructure_areas a
		CROSS JOIN target t
		WHERE ($4 = '' OR a.category = $4)
			AND ($5 = '' OR a.subcategory = $5)
			AND (cardinality($6::text[]) = 0 OR a.object_type = ANY($6::text[]))
			AND a.geom && t.geom
			AND ST_Intersects(a.geom, t.geom)
	`,
		lon,
		lat,
		radiusM,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
	).Scan(&areaM2); err != nil {
		return 0, err
	}

	return areaM2, nil
}

func (s *Store) InfrastructureAreaIntersections(ctx context.Context, lon, lat, radiusM float64, selector InfrastructureSelector, limit int) ([]InfrastructureAreaIntersection, error) {
	selector = normalizeInfrastructureSelector(selector)
	limit = normalizeAssessmentFeatureLimit(limit, 50, 200)

	rows, err := s.db.Query(ctx, `
		WITH target AS (
			SELECT ST_Buffer(ST_SetSRID(ST_Point($1, $2), 4326)::geography, $3)::geometry AS geom
		),
		intersections AS (
			SELECT
				a.id,
				a.source,
				a.source_dataset_id,
				a.source_object_id,
				a.category,
				a.subcategory,
				a.object_type,
				a.name,
				a.area_m2,
				ST_CollectionExtract(ST_Intersection(a.geom, t.geom), 3) AS geom
			FROM infrastructure_areas a
			CROSS JOIN target t
			WHERE ($4 = '' OR a.category = $4)
				AND ($5 = '' OR a.subcategory = $5)
				AND (cardinality($6::text[]) = 0 OR a.object_type = ANY($6::text[]))
				AND a.geom && t.geom
				AND ST_Intersects(a.geom, t.geom)
		)
		SELECT
			id,
			source,
			source_dataset_id,
			source_object_id,
			category,
			subcategory,
			object_type,
			name,
			area_m2,
			ST_Area(geom::geography) AS intersection_m2,
			ST_AsGeoJSON(ST_Multi(geom))::jsonb AS geometry_json
		FROM intersections
		WHERE NOT ST_IsEmpty(geom)
		ORDER BY intersection_m2 DESC
		LIMIT $7
	`,
		lon,
		lat,
		radiusM,
		selector.Category,
		selector.Subcategory,
		selector.ObjectTypes,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]InfrastructureAreaIntersection, 0, limit)
	for rows.Next() {
		var item InfrastructureAreaIntersection
		if err := rows.Scan(
			&item.ID,
			&item.Source,
			&item.SourceDatasetID,
			&item.SourceObjectID,
			&item.Category,
			&item.Subcategory,
			&item.ObjectType,
			&item.Name,
			&item.AreaM2,
			&item.IntersectionM2,
			&item.GeometryJSON,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func normalizeInfrastructureSelector(selector InfrastructureSelector) InfrastructureSelector {
	selector.Category = strings.TrimSpace(selector.Category)
	selector.Subcategory = strings.TrimSpace(selector.Subcategory)

	objectTypes := make([]string, 0, len(selector.ObjectTypes))
	for _, objectType := range selector.ObjectTypes {
		objectType = strings.TrimSpace(objectType)
		if objectType != "" {
			objectTypes = append(objectTypes, objectType)
		}
	}
	selector.ObjectTypes = objectTypes

	return selector
}

func normalizeAssessmentFeatureLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
