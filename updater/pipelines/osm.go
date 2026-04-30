package pipelines

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OSMConfig struct {
	Rules []OSMRuleConfig `json:"rules"`
}

type OSMRuleConfig struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Subcategory  string `json:"subcategory"`
	ObjectType   string `json:"object_type"`
	GeometryMode string `json:"geometry_mode"`
	TagKey       string `json:"tag_key"`
	TagValue     string `json:"tag_value"`
}

const (
	osmSource            = "osm"
	osmGeometryModePoint = "point"
	osmGeometryModeArea  = "area"

	osmPointLayerIndexBase   = 0
	osmLineLayerIndexBase    = 1000000
	osmPolygonLayerIndexBase = 2000000
)

var osmAreaTags = map[string]map[string]struct{}{
	"natural": {
		"water":     {},
		"wetland":   {},
		"wood":      {},
		"scrub":     {},
		"grassland": {},
	},
}

func RunOSM() error {
	configPath := getStringEnv("OSM_CONFIG_FILE", "config/osm_rules.json")
	cfg, err := loadOSMConfig(configPath)
	if err != nil {
		return err
	}

	rules, err := selectedOSMRules(cfg, strings.TrimSpace(os.Getenv("OSM_RULE_ID")))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	db, err := pgxpool.New(ctx, databaseDSN())
	if err != nil {
		return fmt.Errorf("db pool init failed: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}

	if err := ensureOSMRawTables(ctx, db); err != nil {
		return err
	}

	pointRules := 0
	areaRules := 0
	for _, rule := range rules {
		switch rule.geometryMode() {
		case osmGeometryModeArea:
			areaRules++
		default:
			pointRules++
		}
	}

	log.Printf("OSM import started: rules=%d, point_rules=%d, area_rules=%d", len(rules), pointRules, areaRules)

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db transaction begin failed: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM infrastructure_objects WHERE source = $1`, osmSource); err != nil {
		return fmt.Errorf("delete old OSM objects failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM infrastructure_areas WHERE source = $1`, osmSource); err != nil {
		return fmt.Errorf("delete old OSM areas failed: %w", err)
	}

	totalObjects := int64(0)
	totalAreas := int64(0)
	for _, rule := range rules {
		if rule.geometryMode() == osmGeometryModeArea {
			rows, err := saveOSMAreas(ctx, tx, rule)
			if err != nil {
				return err
			}
			totalAreas += rows
			log.Printf(
				"OSM rule: id=%d, %s=%s, mode=area, category=%s, subcategory=%s, object_type=%s, rows=%d",
				rule.ID,
				rule.TagKey,
				rule.TagValue,
				rule.Category,
				rule.Subcategory,
				rule.ObjectType,
				rows,
			)
			continue
		}

		rows, err := saveOSMObjects(ctx, tx, rule)
		if err != nil {
			return err
		}
		totalObjects += rows
		log.Printf(
			"OSM rule: id=%d, %s=%s, mode=point, category=%s, subcategory=%s, object_type=%s, rows=%d",
			rule.ID,
			rule.TagKey,
			rule.TagValue,
			rule.Category,
			rule.Subcategory,
			rule.ObjectType,
			rows,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db transaction commit failed: %w", err)
	}

	log.Printf("OSM import completed: rules=%d, saved_objects=%d, saved_areas=%d", len(rules), totalObjects, totalAreas)
	return nil
}

type pgxExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func saveOSMObjects(ctx context.Context, tx pgxExecutor, rule OSMRuleConfig) (int64, error) {
	tag, err := tx.Exec(ctx, `
		WITH candidates AS (
			SELECT
				osm_id,
				$2::integer AS source_point_index_base,
				name,
				ST_SetSRID(ST_PointOnSurface(way), 4326) AS geom
			FROM osm_point
			WHERE way IS NOT NULL
				AND NOT ST_IsEmpty(way)
				AND tags -> $8 = $9

			UNION ALL

			SELECT
				osm_id,
				$3::integer AS source_point_index_base,
				name,
				ST_SetSRID(ST_PointOnSurface(way), 4326) AS geom
			FROM osm_line
			WHERE way IS NOT NULL
				AND NOT ST_IsEmpty(way)
				AND tags -> $8 = $9

			UNION ALL

			SELECT
				osm_id,
				$4::integer AS source_point_index_base,
				name,
				ST_SetSRID(ST_PointOnSurface(way), 4326) AS geom
			FROM osm_polygon
			WHERE way IS NOT NULL
				AND NOT ST_IsEmpty(way)
				AND tags -> $8 = $9
		),
		numbered AS (
			SELECT
				osm_id,
				source_point_index_base
					+ row_number() OVER (
						PARTITION BY source_point_index_base, osm_id
						ORDER BY COALESCE(NULLIF(btrim(name), ''), $11), ST_AsBinary(geom)
					)::integer
					- 1 AS source_point_index,
				name,
				geom
			FROM candidates
		)
		INSERT INTO infrastructure_objects (
			source,
			source_dataset_id,
			source_object_id,
			source_point_index,
			category,
			subcategory,
			object_type,
			name,
			geom,
			updated_at
		)
		SELECT
			$1,
			$5,
			osm_id,
			source_point_index,
			$6,
			$7,
			$10,
			COALESCE(NULLIF(btrim(name), ''), $11),
			geom,
			now()
		FROM numbered
		ON CONFLICT (source, source_dataset_id, source_object_id, source_point_index)
		DO UPDATE SET
			category = EXCLUDED.category,
			subcategory = EXCLUDED.subcategory,
			object_type = EXCLUDED.object_type,
			name = EXCLUDED.name,
			geom = EXCLUDED.geom,
			updated_at = now()
	`,
		osmSource,
		osmPointLayerIndexBase,
		osmLineLayerIndexBase,
		osmPolygonLayerIndexBase,
		rule.ID,
		rule.Category,
		rule.Subcategory,
		rule.TagKey,
		rule.TagValue,
		rule.ObjectType,
		rule.Name,
	)
	if err != nil {
		return 0, fmt.Errorf("save OSM objects failed for rule_id=%d (%s=%s): %w", rule.ID, rule.TagKey, rule.TagValue, err)
	}

	return tag.RowsAffected(), nil
}

func saveOSMAreas(ctx context.Context, tx pgxExecutor, rule OSMRuleConfig) (int64, error) {
	tag, err := tx.Exec(ctx, `
		WITH candidates AS (
			SELECT
				osm_id,
				COALESCE(NULLIF(btrim(name), ''), $8) AS name,
				ST_Multi(
					ST_CollectionExtract(
						ST_MakeValid(way),
						3
					)
				) AS geom
			FROM osm_polygon
			WHERE way IS NOT NULL
				AND NOT ST_IsEmpty(way)
				AND tags -> $5 = $6
		),
		grouped AS (
			SELECT
				osm_id,
				min(name) AS name,
				ST_Multi(
					ST_CollectionExtract(
						ST_MakeValid(
							ST_UnaryUnion(ST_Collect(geom))
						),
						3
					)
				) AS geom
			FROM candidates
			GROUP BY osm_id
		),
		prepared AS (
			SELECT
				osm_id,
				name,
				geom
			FROM grouped
			WHERE geom IS NOT NULL
				AND NOT ST_IsEmpty(geom)
		)
		INSERT INTO infrastructure_areas (
			source,
			source_dataset_id,
			source_object_id,
			category,
			subcategory,
			object_type,
			name,
			geom,
			area_m2,
			updated_at
		)
		SELECT
			$1,
			$2,
			osm_id,
			$3,
			$4,
			$7,
			name,
			geom,
			ST_Area(geom::geography),
			now()
		FROM prepared
		ON CONFLICT (source, source_dataset_id, source_object_id)
		DO UPDATE SET
			category = EXCLUDED.category,
			subcategory = EXCLUDED.subcategory,
			object_type = EXCLUDED.object_type,
			name = EXCLUDED.name,
			geom = EXCLUDED.geom,
			area_m2 = EXCLUDED.area_m2,
			updated_at = now()
	`,
		osmSource,
		rule.ID,
		rule.Category,
		rule.Subcategory,
		rule.TagKey,
		rule.TagValue,
		rule.ObjectType,
		rule.Name,
	)
	if err != nil {
		return 0, fmt.Errorf("save OSM areas failed for rule_id=%d (%s=%s): %w", rule.ID, rule.TagKey, rule.TagValue, err)
	}

	return tag.RowsAffected(), nil
}

func loadOSMConfig(path string) (OSMConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return OSMConfig{}, fmt.Errorf("open OSM config failed: %w", err)
	}
	defer file.Close()

	var cfg OSMConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return OSMConfig{}, fmt.Errorf("decode OSM config failed: %w", err)
	}

	if len(cfg.Rules) == 0 {
		return OSMConfig{}, fmt.Errorf("OSM config has no rules")
	}

	seen := make(map[int]struct{}, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if _, exists := seen[rule.ID]; exists {
			return OSMConfig{}, fmt.Errorf("duplicate OSM rule id=%d", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if err := validateOSMRule(rule); err != nil {
			return OSMConfig{}, err
		}
	}

	return cfg, nil
}

func selectedOSMRules(cfg OSMConfig, requested string) ([]OSMRuleConfig, error) {
	if requested == "" || strings.EqualFold(requested, "all") {
		return cfg.Rules, nil
	}

	ruleID, err := strconv.Atoi(requested)
	if err != nil {
		return nil, fmt.Errorf("invalid OSM_RULE_ID=%q (use integer id or all)", requested)
	}

	for _, rule := range cfg.Rules {
		if rule.ID == ruleID {
			return []OSMRuleConfig{rule}, nil
		}
	}

	return nil, fmt.Errorf("missing OSM rule config for rule_id=%d", ruleID)
}

func validateOSMRule(rule OSMRuleConfig) error {
	if rule.ID <= 0 {
		return fmt.Errorf("OSM rule has invalid id=%d", rule.ID)
	}
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("OSM rule %d has empty name", rule.ID)
	}
	if strings.TrimSpace(rule.Category) == "" {
		return fmt.Errorf("OSM rule %d has empty category", rule.ID)
	}
	if strings.TrimSpace(rule.Subcategory) == "" {
		return fmt.Errorf("OSM rule %d has empty subcategory", rule.ID)
	}
	if strings.TrimSpace(rule.ObjectType) == "" {
		return fmt.Errorf("OSM rule %d has empty object_type", rule.ID)
	}
	if strings.TrimSpace(rule.TagKey) == "" {
		return fmt.Errorf("OSM rule %d has empty tag_key", rule.ID)
	}
	if strings.TrimSpace(rule.TagValue) == "" {
		return fmt.Errorf("OSM rule %d has empty tag_value", rule.ID)
	}

	switch rule.geometryMode() {
	case osmGeometryModePoint:
		if isOSMAreaTag(rule.TagKey, rule.TagValue) {
			return fmt.Errorf("OSM rule %d uses area tag %s=%s but geometry_mode=point", rule.ID, rule.TagKey, rule.TagValue)
		}
	case osmGeometryModeArea:
		if !isOSMAreaTag(rule.TagKey, rule.TagValue) {
			return fmt.Errorf("OSM rule %d uses geometry_mode=area, but only water and green natural=* tags are area data", rule.ID)
		}
	default:
		return fmt.Errorf("OSM rule %d has invalid geometry_mode=%q", rule.ID, rule.GeometryMode)
	}

	return nil
}

func (rule OSMRuleConfig) geometryMode() string {
	mode := strings.TrimSpace(rule.GeometryMode)
	if mode == "" {
		return osmGeometryModePoint
	}
	return mode
}

func isOSMAreaTag(key string, value string) bool {
	values, ok := osmAreaTags[key]
	if !ok {
		return false
	}
	_, ok = values[value]
	return ok
}

func ensureOSMRawTables(ctx context.Context, db *pgxpool.Pool) error {
	requiredTables := []string{"osm_point", "osm_line", "osm_polygon"}
	for _, table := range requiredTables {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			return fmt.Errorf("check OSM raw table %s failed: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("missing OSM raw table %s; run raw osm2pgsql import before OSM rules import", table)
		}
	}
	return nil
}
