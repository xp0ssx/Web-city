package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type TagKeyCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type TagValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

func (s *Store) ListTagKeys(ctx context.Context) ([]TagKeyCount, error) {
	rows, err := s.db.Query(ctx, `
        SELECT key, COUNT(*) AS count
        FROM osm_features
        CROSS JOIN LATERAL jsonb_object_keys(tags) AS key
        GROUP BY key
        ORDER BY count DESC, key ASC
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TagKeyCount, 0, 64)

	for rows.Next() {
		var item TagKeyCount
		if err := rows.Scan(&item.Key, &item.Count); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Store) ListTagValues(ctx context.Context, key string) ([]TagValueCount, error) {
	rows, err := s.db.Query(ctx, `
        SELECT COALESCE(tags ->> $1, '') AS value, COUNT(*) AS count
        FROM osm_features
        WHERE tags ? $1
        GROUP BY value
        ORDER BY count DESC, value ASC
    `, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TagValueCount, 0, 64)

	for rows.Next() {
		var item TagValueCount
		if err := rows.Scan(&item.Value, &item.Count); err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

type ClusterPoint struct {
	Lon   float64
	Lat   float64
	Count int64
}

type FeatureRow struct {
	OSMID        int64
	Name         string
	SourceLayer  string
	TagsJSON     []byte
	GeometryJSON []byte
}

func (s *Store) ListFeatures(ctx context.Context, key, value, geometry string, limit int) ([]FeatureRow, error) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	geometry = strings.ToLower(strings.TrimSpace(geometry))

	if key == "" {
		return nil, errors.New("missing key")
	}

	if geometry == "" {
		geometry = "all"
	}

	if limit <= 0 {
		limit = 500
	}
	if limit > 100000 {
		limit = 100000
	}

	var query string

	switch geometry {
	case "point":
		query = `
            SELECT
                osm_id,
                COALESCE(name, '') AS name,
                'point' AS source_layer,
                hstore_to_jsonb_loose(tags)::text AS tags_json,
                ST_AsGeoJSON(way) AS geometry_json
            FROM osm_point
            WHERE tags ? $1
              AND ($2 = '' OR tags -> $1 = $2)
            ORDER BY osm_id
            LIMIT $3
        `
	case "polygon":
		query = `
            SELECT
                osm_id,
                COALESCE(name, '') AS name,
                'polygon' AS source_layer,
                hstore_to_jsonb_loose(tags)::text AS tags_json,
                ST_AsGeoJSON(way) AS geometry_json
            FROM osm_polygon
            WHERE tags ? $1
              AND ($2 = '' OR tags -> $1 = $2)
            ORDER BY osm_id
            LIMIT $3
        `
	case "line":
		query = `
        SELECT
            osm_id,
            COALESCE(name, '') AS name,
            'line' AS source_layer,
            hstore_to_jsonb_loose(tags)::text AS tags_json,
            ST_AsGeoJSON(way) AS geometry_json
        FROM osm_line
        WHERE tags ? $1
          AND ($2 = '' OR tags -> $1 = $2)
        ORDER BY osm_id
        LIMIT $3
    `
	case "all":
		query = `
            WITH combined AS (
                SELECT
                    osm_id,
                    COALESCE(name, '') AS name,
                    'point' AS source_layer,
                    hstore_to_jsonb_loose(tags)::text AS tags_json,
                    ST_AsGeoJSON(way) AS geometry_json
                FROM osm_point
                WHERE tags ? $1
                  AND ($2 = '' OR tags -> $1 = $2)

                UNION ALL

                SELECT
                    osm_id,
                    COALESCE(name, '') AS name,
                    'polygon' AS source_layer,
                    hstore_to_jsonb_loose(tags)::text AS tags_json,
                    ST_AsGeoJSON(way) AS geometry_json
                FROM osm_polygon
                WHERE tags ? $1
                  AND ($2 = '' OR tags -> $1 = $2)
				
				UNION ALL

					SELECT
						osm_id,
						COALESCE(name, '') AS name,
						'line' AS source_layer,
						hstore_to_jsonb_loose(tags)::text AS tags_json,
						ST_AsGeoJSON(way) AS geometry_json
					FROM osm_line
					WHERE tags ? $1
					AND ($2 = '' OR tags -> $1 = $2)
            )
            SELECT
                osm_id,
                name,
                source_layer,
                tags_json,
                geometry_json
            FROM combined
            ORDER BY source_layer, osm_id
            LIMIT $3
        `
	default:
		return nil, errors.New("invalid geometry")
	}

	rows, err := s.db.Query(ctx, query, key, value, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]FeatureRow, 0, limit)

	for rows.Next() {
		var item FeatureRow
		if err := rows.Scan(&item.OSMID, &item.Name, &item.SourceLayer, &item.TagsJSON, &item.GeometryJSON); err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
