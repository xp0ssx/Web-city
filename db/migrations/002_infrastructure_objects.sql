BEGIN;

CREATE TABLE IF NOT EXISTS infrastructure_objects (
  id BIGSERIAL PRIMARY KEY,

  source TEXT NOT NULL,
  source_dataset_id INTEGER NOT NULL,
  source_object_id BIGINT NOT NULL,
  source_point_index INTEGER NOT NULL DEFAULT 0,

  category TEXT NOT NULL,
  subcategory TEXT NOT NULL,
  object_type TEXT NOT NULL,
  name TEXT NOT NULL,

  geom GEOMETRY(Point, 4326) NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT infrastructure_objects_source_key
    UNIQUE (source, source_dataset_id, source_object_id, source_point_index),
  CONSTRAINT infrastructure_objects_source_point_index_check
    CHECK (source_point_index >= 0),
  CONSTRAINT infrastructure_objects_source_not_blank
    CHECK (btrim(source) <> ''),
  CONSTRAINT infrastructure_objects_category_not_blank
    CHECK (btrim(category) <> ''),
  CONSTRAINT infrastructure_objects_subcategory_not_blank
    CHECK (btrim(subcategory) <> ''),
  CONSTRAINT infrastructure_objects_object_type_not_blank
    CHECK (btrim(object_type) <> ''),
  CONSTRAINT infrastructure_objects_name_not_blank
    CHECK (btrim(name) <> '')
);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_category
  ON infrastructure_objects (category);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_subcategory
  ON infrastructure_objects (subcategory);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_category_subcategory
  ON infrastructure_objects (category, subcategory);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_category_subcategory_object_type
  ON infrastructure_objects (category, subcategory, object_type);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_object_type
  ON infrastructure_objects (object_type);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_geom
  ON infrastructure_objects USING GIST (geom);

CREATE INDEX IF NOT EXISTS idx_infrastructure_objects_source_dataset
  ON infrastructure_objects (source, source_dataset_id);

CREATE TABLE IF NOT EXISTS infrastructure_areas (
  id BIGSERIAL PRIMARY KEY,

  source TEXT NOT NULL,
  source_dataset_id INTEGER NOT NULL,
  source_object_id BIGINT NOT NULL,

  category TEXT NOT NULL,
  subcategory TEXT NOT NULL,
  object_type TEXT NOT NULL,
  name TEXT NOT NULL,

  geom GEOMETRY(MultiPolygon, 4326) NOT NULL,
  area_m2 DOUBLE PRECISION NOT NULL,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT infrastructure_areas_source_key
    UNIQUE (source, source_dataset_id, source_object_id),
  CONSTRAINT infrastructure_areas_area_m2_check
    CHECK (area_m2 >= 0),
  CONSTRAINT infrastructure_areas_source_not_blank
    CHECK (btrim(source) <> ''),
  CONSTRAINT infrastructure_areas_category_not_blank
    CHECK (btrim(category) <> ''),
  CONSTRAINT infrastructure_areas_subcategory_not_blank
    CHECK (btrim(subcategory) <> ''),
  CONSTRAINT infrastructure_areas_object_type_not_blank
    CHECK (btrim(object_type) <> ''),
  CONSTRAINT infrastructure_areas_name_not_blank
    CHECK (btrim(name) <> '')
);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_category
  ON infrastructure_areas (category);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_subcategory
  ON infrastructure_areas (subcategory);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_category_subcategory
  ON infrastructure_areas (category, subcategory);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_category_subcategory_object_type
  ON infrastructure_areas (category, subcategory, object_type);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_object_type
  ON infrastructure_areas (object_type);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_geom
  ON infrastructure_areas USING GIST (geom);

CREATE INDEX IF NOT EXISTS idx_infrastructure_areas_source_dataset
  ON infrastructure_areas (source, source_dataset_id);

COMMIT;
