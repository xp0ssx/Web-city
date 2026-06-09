package store

import (
	"context"
	"fmt"
)

type TilePath struct {
	Kind string
	Path string
}

func (s *Store) BaseTilePaths(ctx context.Context, z, x, y int) ([]TilePath, error) {
	if z < 0 || z > 19 {
		return nil, fmt.Errorf("zoom must be between 0 and 19")
	}
	if x < 0 || y < 0 || x >= (1<<z) || y >= (1<<z) {
		return nil, fmt.Errorf("tile coordinates are outside zoom bounds")
	}

	rows, err := s.db.Query(ctx, `
		WITH params AS (
			SELECT
				ST_TileEnvelope($1, $2, $3) AS tile_3857
		),
		bounds AS (
			SELECT
				tile_3857,
				ST_Transform(tile_3857, 4326) AS tile_4326,
				ST_XMin(tile_3857) AS xmin,
				ST_YMax(tile_3857) AS ymax,
				256.0 / (ST_XMax(tile_3857) - ST_XMin(tile_3857)) AS scale,
				(ST_XMax(tile_3857) - ST_XMin(tile_3857)) / 256.0 AS pixel_size
			FROM params
		),
		area_candidates AS (
			SELECT
				CASE
					WHEN a.subcategory = 'water_bodies' THEN 'water'
					ELSE 'green'
				END AS kind,
				a.geom
			FROM infrastructure_areas a
			CROSS JOIN bounds b
			WHERE $1 >= 8
				AND a.geom && b.tile_4326
				AND a.subcategory IN ('water_bodies', 'parks_greenery', 'natural_greenery')
				AND (
					$1 >= 12
					OR ($1 = 11 AND a.area_m2 >= 5000)
					OR ($1 = 10 AND a.area_m2 >= 10000)
					OR ($1 <= 9 AND a.area_m2 >= 50000)
				)
		),
		area_paths AS (
			SELECT
				c.kind,
				ST_AsSVG(
					ST_Affine(
						clipped.geom,
						b.scale,
						0,
						0,
						b.scale,
						-b.xmin * b.scale,
						-b.ymax * b.scale
					),
					0,
					0
				) AS path
			FROM area_candidates c
			CROSS JOIN bounds b
			CROSS JOIN LATERAL (
				SELECT ST_SimplifyPreserveTopology(
					ST_CollectionExtract(
						ST_Intersection(ST_Transform(c.geom, 3857), b.tile_3857),
						3
					),
					GREATEST(
						CASE
							WHEN $1 <= 9 THEN b.pixel_size * 8.0
							WHEN $1 = 10 THEN b.pixel_size * 5.0
							WHEN $1 = 11 THEN b.pixel_size * 3.0
							WHEN $1 = 12 THEN b.pixel_size * 1.5
							ELSE b.pixel_size * 0.5
						END,
						1.0
					)
				) AS geom
			) clipped
			WHERE NOT ST_IsEmpty(clipped.geom)
		),
		building_candidates AS (
			SELECT
				'building' AS kind,
				m.geom
			FROM map_buildings m
			CROSS JOIN bounds b
			WHERE $1 >= m.min_zoom
				AND m.geom && b.tile_4326
		),
		building_paths AS (
			SELECT
				c.kind,
				ST_AsSVG(
					ST_Affine(
						clipped.geom,
						b.scale,
						0,
						0,
						b.scale,
						-b.xmin * b.scale,
						-b.ymax * b.scale
					),
					0,
					0
				) AS path
			FROM building_candidates c
			CROSS JOIN bounds b
			CROSS JOIN LATERAL (
				SELECT ST_SimplifyPreserveTopology(
					ST_CollectionExtract(
						ST_Intersection(ST_Transform(c.geom, 3857), b.tile_3857),
						3
					),
					GREATEST(
						CASE
							WHEN $1 <= 14 THEN b.pixel_size
							WHEN $1 = 15 THEN b.pixel_size * 0.5
							ELSE b.pixel_size * 0.25
						END,
						0.25
					)
				) AS geom
			) clipped
			WHERE NOT ST_IsEmpty(clipped.geom)
		),
		road_candidates AS (
			SELECT
				'road_' || m.road_class AS kind,
				m.geom AS way,
				m.sort_order
			FROM map_roads m
			CROSS JOIN bounds b
			WHERE $1 >= m.min_zoom
				AND m.geom && b.tile_4326
		),
		road_paths AS (
			SELECT
				c.kind,
				c.sort_order,
				ST_AsSVG(
					ST_Affine(
						clipped.geom,
						b.scale,
						0,
						0,
						b.scale,
						-b.xmin * b.scale,
						-b.ymax * b.scale
					),
					0,
					0
				) AS path
			FROM road_candidates c
			CROSS JOIN bounds b
			CROSS JOIN LATERAL (
				SELECT ST_Simplify(
					ST_CollectionExtract(
						ST_Intersection(ST_Transform(c.way, 3857), b.tile_3857),
						2
					),
					GREATEST(
						CASE
							WHEN $1 <= 10 THEN b.pixel_size * 2.0
							WHEN $1 <= 12 THEN b.pixel_size
							WHEN $1 <= 15 THEN b.pixel_size * 0.5
							ELSE b.pixel_size * 0.25
						END,
						0.25
					)
				) AS geom
			) clipped
			WHERE NOT ST_IsEmpty(clipped.geom)
		),
		line_candidates AS (
			SELECT
				CASE
					WHEN a.area_type = 'admin_district' THEN 'admin_boundary'
					ELSE 'municipality_boundary'
				END AS kind,
				ST_Boundary(a.geom) AS way,
				90 AS sort_order
			FROM administrative_areas a
			CROSS JOIN bounds b
			WHERE $1 >= 8
				AND a.geom && b.tile_4326
				AND (a.area_type = 'admin_district' OR $1 >= 11)
		),
		line_paths AS (
			SELECT
				c.kind,
				c.sort_order,
				ST_AsSVG(
					ST_Affine(
						clipped.geom,
						b.scale,
						0,
						0,
						b.scale,
						-b.xmin * b.scale,
						-b.ymax * b.scale
					),
					0,
					0
				) AS path
			FROM line_candidates c
			CROSS JOIN bounds b
			CROSS JOIN LATERAL (
				SELECT ST_Simplify(
					ST_CollectionExtract(
						ST_Intersection(ST_Transform(c.way, 3857), b.tile_3857),
						2
					),
					GREATEST(
						CASE
							WHEN $1 <= 10 THEN b.pixel_size * 2.0
							WHEN $1 = 11 THEN b.pixel_size
							ELSE b.pixel_size * 0.5
						END,
						1.0
					)
				) AS geom
			) clipped
			WHERE NOT ST_IsEmpty(clipped.geom)
		)
		SELECT kind, path
		FROM (
			SELECT kind, 1 AS sort_order, path FROM area_paths WHERE kind = 'water'
			UNION ALL
			SELECT kind, 2 AS sort_order, path FROM area_paths WHERE kind = 'green'
			UNION ALL
			SELECT kind, 5 AS sort_order, path FROM building_paths
			UNION ALL
			SELECT kind, sort_order + 10 AS sort_order, path FROM road_paths
			UNION ALL
			SELECT kind, sort_order, path FROM line_paths
		) all_paths
		WHERE path IS NOT NULL AND path <> ''
		ORDER BY sort_order, kind
	`, z, x, y)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make([]TilePath, 0, 256)
	for rows.Next() {
		var path TilePath
		if err := rows.Scan(&path.Kind, &path.Path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return paths, nil
}
