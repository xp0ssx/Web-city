BEGIN;

CREATE TABLE IF NOT EXISTS map_roads (
  id BIGSERIAL PRIMARY KEY,

  source_osm_id BIGINT NOT NULL,
  road_class TEXT NOT NULL,
  min_zoom INTEGER NOT NULL,
  sort_order INTEGER NOT NULL,
  name TEXT,

  geom GEOMETRY(LineString, 4326) NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT map_roads_min_zoom_check
    CHECK (min_zoom BETWEEN 0 AND 19),
  CONSTRAINT map_roads_road_class_not_blank
    CHECK (btrim(road_class) <> '')
);

CREATE INDEX IF NOT EXISTS idx_map_roads_geom
  ON map_roads USING GIST (geom);

CREATE INDEX IF NOT EXISTS idx_map_roads_min_zoom
  ON map_roads (min_zoom);

CREATE INDEX IF NOT EXISTS idx_map_roads_class
  ON map_roads (road_class);

CREATE TABLE IF NOT EXISTS map_buildings (
  id BIGSERIAL PRIMARY KEY,

  source_osm_id BIGINT NOT NULL,
  min_zoom INTEGER NOT NULL,
  name TEXT,

  geom GEOMETRY(MultiPolygon, 4326) NOT NULL,
  area_m2 DOUBLE PRECISION NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT map_buildings_min_zoom_check
    CHECK (min_zoom BETWEEN 0 AND 19),
  CONSTRAINT map_buildings_area_m2_check
    CHECK (area_m2 >= 0)
);

CREATE INDEX IF NOT EXISTS idx_map_buildings_geom
  ON map_buildings USING GIST (geom);

CREATE INDEX IF NOT EXISTS idx_map_buildings_min_zoom
  ON map_buildings (min_zoom);

COMMIT;
