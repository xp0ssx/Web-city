package store

import (
	"context"
	"strings"
)

type InfrastructureFilter struct {
	Source      string
	Category    string
	Subcategory string
	ObjectType  string
	Limit       int
}

type InfrastructureFacet struct {
	Geometry    string `json:"geometry"`
	Source      string `json:"source"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	ObjectType  string `json:"object_type"`
	Count       int64  `json:"count"`
}

type InfrastructureFeatureRow struct {
	ID               int64
	Source           string
	SourceDatasetID  int
	SourceObjectID   int64
	SourcePointIndex *int
	Category         string
	Subcategory      string
	ObjectType       string
	Name             string
	AreaM2           *float64
	GeometryJSON     []byte
}

func (s *Store) ListInfrastructureFacets(ctx context.Context) ([]InfrastructureFacet, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			'object' AS geometry,
			source,
			category,
			subcategory,
			object_type,
			COUNT(*) AS count
		FROM infrastructure_objects
		GROUP BY source, category, subcategory, object_type

		UNION ALL

		SELECT
			'area' AS geometry,
			source,
			category,
			subcategory,
			object_type,
			COUNT(*) AS count
		FROM infrastructure_areas
		GROUP BY source, category, subcategory, object_type

		ORDER BY geometry, source, category, subcategory, object_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]InfrastructureFacet, 0, 128)
	for rows.Next() {
		var item InfrastructureFacet
		if err := rows.Scan(
			&item.Geometry,
			&item.Source,
			&item.Category,
			&item.Subcategory,
			&item.ObjectType,
			&item.Count,
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

func (s *Store) ListInfrastructureObjects(ctx context.Context, filter InfrastructureFilter) ([]InfrastructureFeatureRow, error) {
	filter = normalizeInfrastructureFilter(filter)

	rows, err := s.db.Query(ctx, `
		SELECT
			id,
			source,
			source_dataset_id,
			source_object_id,
			source_point_index,
			category,
			subcategory,
			object_type,
			name,
			ST_AsGeoJSON(geom)::jsonb AS geometry_json
		FROM infrastructure_objects
		WHERE ($1 = '' OR source = $1)
			AND ($2 = '' OR category = $2)
			AND ($3 = '' OR subcategory = $3)
			AND ($4 = '' OR object_type = $4)
		ORDER BY source, category, subcategory, object_type, id
		LIMIT $5
	`,
		filter.Source,
		filter.Category,
		filter.Subcategory,
		filter.ObjectType,
		filter.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]InfrastructureFeatureRow, 0, filter.Limit)
	for rows.Next() {
		var item InfrastructureFeatureRow
		var sourcePointIndex int
		if err := rows.Scan(
			&item.ID,
			&item.Source,
			&item.SourceDatasetID,
			&item.SourceObjectID,
			&sourcePointIndex,
			&item.Category,
			&item.Subcategory,
			&item.ObjectType,
			&item.Name,
			&item.GeometryJSON,
		); err != nil {
			return nil, err
		}
		item.SourcePointIndex = &sourcePointIndex
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Store) ListInfrastructureAreas(ctx context.Context, filter InfrastructureFilter) ([]InfrastructureFeatureRow, error) {
	filter = normalizeInfrastructureFilter(filter)

	rows, err := s.db.Query(ctx, `
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
			ST_AsGeoJSON(geom)::jsonb AS geometry_json
		FROM infrastructure_areas
		WHERE ($1 = '' OR source = $1)
			AND ($2 = '' OR category = $2)
			AND ($3 = '' OR subcategory = $3)
			AND ($4 = '' OR object_type = $4)
		ORDER BY source, category, subcategory, object_type, id
		LIMIT $5
	`,
		filter.Source,
		filter.Category,
		filter.Subcategory,
		filter.ObjectType,
		filter.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]InfrastructureFeatureRow, 0, filter.Limit)
	for rows.Next() {
		var item InfrastructureFeatureRow
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

func normalizeInfrastructureFilter(filter InfrastructureFilter) InfrastructureFilter {
	filter.Source = strings.TrimSpace(filter.Source)
	filter.Category = strings.TrimSpace(filter.Category)
	filter.Subcategory = strings.TrimSpace(filter.Subcategory)
	filter.ObjectType = strings.TrimSpace(filter.ObjectType)

	if filter.Limit <= 0 {
		filter.Limit = 1000
	}
	if filter.Limit > 50000 {
		filter.Limit = 50000
	}

	return filter
}
