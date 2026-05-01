BEGIN;

CREATE TABLE IF NOT EXISTS administrative_areas (
  id BIGSERIAL PRIMARY KEY,

  area_type TEXT NOT NULL,
  name TEXT NOT NULL,
  parent_name TEXT,
  abbreviation TEXT,
  okato TEXT,
  oktmo TEXT,
  type_mo TEXT,

  source TEXT NOT NULL,
  source_file TEXT NOT NULL,

  geom GEOMETRY(MultiPolygon, 4326) NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT administrative_areas_area_type_check
    CHECK (area_type IN ('admin_district', 'municipality')),
  CONSTRAINT administrative_areas_area_key
    UNIQUE (area_type, name),
  CONSTRAINT administrative_areas_name_not_blank
    CHECK (btrim(name) <> ''),
  CONSTRAINT administrative_areas_source_not_blank
    CHECK (btrim(source) <> ''),
  CONSTRAINT administrative_areas_source_file_not_blank
    CHECK (btrim(source_file) <> '')
);

CREATE INDEX IF NOT EXISTS idx_administrative_areas_area_type
  ON administrative_areas (area_type);

CREATE INDEX IF NOT EXISTS idx_administrative_areas_parent_name
  ON administrative_areas (parent_name);

CREATE INDEX IF NOT EXISTS idx_administrative_areas_geom
  ON administrative_areas USING GIST (geom);

CREATE TABLE IF NOT EXISTS district_economic_metrics (
  id BIGSERIAL PRIMARY KEY,

  period_month DATE NOT NULL,
  municipality_name TEXT NOT NULL,
  source_district TEXT NOT NULL,

  price_rub_m2 INTEGER NOT NULL,
  yield_vs_bank_deposit NUMERIC(8, 3) NOT NULL,

  source TEXT NOT NULL,
  source_file TEXT NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT district_economic_metrics_key
    UNIQUE (period_month, municipality_name),
  CONSTRAINT district_economic_metrics_municipality_not_blank
    CHECK (btrim(municipality_name) <> ''),
  CONSTRAINT district_economic_metrics_source_district_not_blank
    CHECK (btrim(source_district) <> ''),
  CONSTRAINT district_economic_metrics_price_check
    CHECK (price_rub_m2 > 0),
  CONSTRAINT district_economic_metrics_yield_check
    CHECK (yield_vs_bank_deposit >= 0),
  CONSTRAINT district_economic_metrics_source_not_blank
    CHECK (btrim(source) <> ''),
  CONSTRAINT district_economic_metrics_source_file_not_blank
    CHECK (btrim(source_file) <> '')
);

CREATE INDEX IF NOT EXISTS idx_district_economic_metrics_municipality
  ON district_economic_metrics (municipality_name);

COMMIT;
