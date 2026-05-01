package pipelines

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RunMapLayers() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	db, err := pgxpool.New(ctx, databaseDSN())
	if err != nil {
		return fmt.Errorf("db pool init failed: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}

	if err := ensureMapLayerSources(ctx, db); err != nil {
		return err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db transaction begin failed: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `TRUNCATE TABLE map_roads, map_buildings RESTART IDENTITY`); err != nil {
		return fmt.Errorf("truncate map layers failed: %w", err)
	}

	roadsTag, err := tx.Exec(ctx, `
		INSERT INTO map_roads (
			source_osm_id,
			road_class,
			min_zoom,
			sort_order,
			name,
			geom,
			updated_at
		)
		SELECT
			osm_id,
			CASE
				WHEN highway IN ('motorway', 'motorway_link', 'trunk', 'trunk_link') THEN 'motorway'
				WHEN highway IN ('primary', 'primary_link') THEN 'primary'
				WHEN highway IN ('secondary', 'secondary_link') THEN 'secondary'
				WHEN highway IN ('tertiary', 'tertiary_link', 'unclassified') THEN 'tertiary'
				WHEN highway IN ('residential', 'living_street') THEN 'local'
				WHEN highway IN ('service', 'track') THEN 'service'
				WHEN highway IN ('footway', 'path', 'pedestrian', 'cycleway', 'steps') THEN 'path'
				ELSE 'other'
			END AS road_class,
			CASE
				WHEN highway IN ('motorway', 'motorway_link', 'trunk', 'trunk_link') THEN 9
				WHEN highway IN ('primary', 'primary_link') THEN 10
				WHEN highway IN ('secondary', 'secondary_link') THEN 11
				WHEN highway IN ('tertiary', 'tertiary_link', 'unclassified') THEN 12
				WHEN highway IN ('residential', 'living_street') THEN 14
				WHEN highway IN ('service', 'track') THEN 15
				WHEN highway IN ('footway', 'path', 'pedestrian', 'cycleway', 'steps') THEN 16
				ELSE 15
			END AS min_zoom,
			CASE
				WHEN highway IN ('motorway', 'motorway_link', 'trunk', 'trunk_link') THEN 10
				WHEN highway IN ('primary', 'primary_link') THEN 20
				WHEN highway IN ('secondary', 'secondary_link') THEN 30
				WHEN highway IN ('tertiary', 'tertiary_link', 'unclassified') THEN 40
				WHEN highway IN ('residential', 'living_street') THEN 50
				WHEN highway IN ('service', 'track') THEN 60
				WHEN highway IN ('footway', 'path', 'pedestrian', 'cycleway', 'steps') THEN 70
				ELSE 80
			END AS sort_order,
			NULLIF(btrim(name), '') AS name,
			way AS geom,
			now()
		FROM osm_line
		WHERE way IS NOT NULL
			AND NOT ST_IsEmpty(way)
			AND highway IN (
				'motorway', 'motorway_link', 'trunk', 'trunk_link',
				'primary', 'primary_link',
				'secondary', 'secondary_link',
				'tertiary', 'tertiary_link', 'unclassified',
				'residential', 'living_street',
				'service', 'track',
				'footway', 'path', 'pedestrian', 'cycleway', 'steps'
			)
	`)
	if err != nil {
		return fmt.Errorf("insert map roads failed: %w", err)
	}

	buildingsTag, err := tx.Exec(ctx, `
		WITH candidates AS (
			SELECT
				osm_id,
				NULLIF(btrim(name), '') AS name,
				ST_Multi(
					ST_CollectionExtract(
						ST_MakeValid(way),
						3
					)
				) AS geom
			FROM osm_polygon
			WHERE way IS NOT NULL
				AND NOT ST_IsEmpty(way)
				AND building IS NOT NULL
		),
		prepared AS (
			SELECT
				osm_id,
				name,
				geom,
				ST_Area(geom::geography) AS area_m2
			FROM candidates
			WHERE geom IS NOT NULL
				AND NOT ST_IsEmpty(geom)
		)
		INSERT INTO map_buildings (
			source_osm_id,
			min_zoom,
			name,
			geom,
			area_m2,
			updated_at
		)
		SELECT
			osm_id,
			CASE
				WHEN area_m2 >= 50000 THEN 13
				WHEN area_m2 >= 10000 THEN 14
				WHEN area_m2 >= 2500 THEN 15
				ELSE 16
			END AS min_zoom,
			name,
			geom,
			area_m2,
			now()
		FROM prepared
	`)
	if err != nil {
		return fmt.Errorf("insert map buildings failed: %w", err)
	}

	if _, err := tx.Exec(ctx, `ANALYZE map_roads; ANALYZE map_buildings`); err != nil {
		return fmt.Errorf("analyze map layers failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db transaction commit failed: %w", err)
	}

	if err := clearTileCache(); err != nil {
		return err
	}
	if err := dropOSMRawTables(ctx, db); err != nil {
		return err
	}
	if err := dropOSMFiles(); err != nil {
		return err
	}

	log.Printf("map layers completed: roads=%d, buildings=%d", roadsTag.RowsAffected(), buildingsTag.RowsAffected())
	return nil
}

func ensureMapLayerSources(ctx context.Context, db *pgxpool.Pool) error {
	requiredTables := []string{"osm_line", "osm_polygon", "map_roads", "map_buildings"}
	for _, table := range requiredTables {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			return fmt.Errorf("check table %s failed: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("missing table %s", table)
		}
	}
	return nil
}

func clearTileCache() error {
	cacheDir := getStringEnv("TILE_CACHE_DIR", "")
	if cacheDir == "" {
		return nil
	}

	baseDir := filepath.Join(cacheDir, "base")
	if err := os.RemoveAll(baseDir); err != nil {
		return fmt.Errorf("clear tile cache failed: %w", err)
	}

	log.Printf("tile cache cleared: %s", baseDir)
	return nil
}

func dropOSMRawTables(ctx context.Context, db *pgxpool.Pool) error {
	if getStringEnv("MAP_LAYERS_DROP_OSM_RAW", "0") != "1" {
		return nil
	}

	if _, err := db.Exec(ctx, `
		DROP TABLE IF EXISTS
			osm_point,
			osm_line,
			osm_polygon,
			osm_roads,
			osm_nodes,
			osm_ways,
			osm_rels
		CASCADE
	`); err != nil {
		return fmt.Errorf("drop OSM raw tables failed: %w", err)
	}

	log.Println("OSM raw tables dropped after map layers build")
	return nil
}

func dropOSMFiles() error {
	if getStringEnv("MAP_LAYERS_DROP_OSM_FILES", "0") != "1" {
		return nil
	}

	paths := []string{
		getStringEnv("OSM_EXTRACT_FILE", ""),
		getStringEnv("OSM_PBF_FILE", ""),
	}

	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("drop OSM file %s failed: %w", path, err)
		}
		log.Printf("OSM file dropped after map layers build: %s", path)
	}

	return nil
}
