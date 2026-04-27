BEGIN;

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS hstore;

CREATE TABLE IF NOT EXISTS import_runs (
  id BIGSERIAL PRIMARY KEY,
  source TEXT NOT NULL DEFAULT 'osm',
  region TEXT NOT NULL DEFAULT 'moscow',
  source_url TEXT,
  source_file TEXT,
  status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed')),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  rows_point BIGINT NOT NULL DEFAULT 0,
  rows_line BIGINT NOT NULL DEFAULT 0,
  rows_polygon BIGINT NOT NULL DEFAULT 0,
  rows_features BIGINT NOT NULL DEFAULT 0,
  error_text TEXT
);

CREATE TABLE IF NOT EXISTS osm_features (
  id BIGSERIAL PRIMARY KEY,
  import_run_id BIGINT NOT NULL REFERENCES import_runs(id) ON DELETE CASCADE,
  osm_id BIGINT NOT NULL,
  source_layer TEXT NOT NULL CHECK (source_layer IN ('point', 'line', 'polygon')),
  name TEXT,
  tags JSONB NOT NULL DEFAULT '{}'::jsonb,
  geom GEOMETRY(Point, 4326) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_osm_features_import_run_id
  ON osm_features (import_run_id);

CREATE INDEX IF NOT EXISTS idx_osm_features_geom_gist
  ON osm_features USING GIST (geom);

CREATE INDEX IF NOT EXISTS idx_osm_features_geog_gist
  ON osm_features USING GIST ((geom::geography));

CREATE INDEX IF NOT EXISTS idx_osm_features_tags_gin
  ON osm_features USING GIN (tags);

CREATE INDEX IF NOT EXISTS idx_osm_features_name
  ON osm_features (name);

COMMIT;