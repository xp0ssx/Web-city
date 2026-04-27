#!/usr/bin/env bash
set -Eeuo pipefail

DB_HOST="${DB_HOST:-postgis}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-webcity}"
DB_USER="${DB_USER:-webcity}"
DB_PASSWORD="${DB_PASSWORD:-webcity}"

OSM_PBF_URL="${OSM_PBF_URL:?OSM_PBF_URL is required}"
OSM_PBF_FILE="${OSM_PBF_FILE:-/data/central-fed-district-latest.osm.pbf}"
OSM_PBF_MD5_URL="${OSM_PBF_MD5_URL:-${OSM_PBF_URL}.md5}"

OSM_EXTRACT_FILE="${OSM_EXTRACT_FILE:-/data/moscow-extract.osm.pbf}"
OSM_EXTRACT_BBOX="${OSM_EXTRACT_BBOX:-}"
OSM_EXTRACT_STRATEGY="${OSM_EXTRACT_STRATEGY:-complete_ways}"

OSM_PREFIX="${OSM_PREFIX:-osm}"
OSM_MIN_BYTES="${OSM_MIN_BYTES:-50000000}"
OSM_FORCE_DOWNLOAD="${OSM_FORCE_DOWNLOAD:-0}"

RUN_ID=""

psql_cmd() {
  PGPASSWORD="$DB_PASSWORD" psql -X -q -v ON_ERROR_STOP=1 \
    -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" "$@"
}

validate_pbf() {
  osmium fileinfo "$1" >/dev/null 2>&1
}

validate_md5() {
  local target_file="$1"
  local md5_url="$2"
  local tmp_md5

  if [ -z "$md5_url" ]; then
    return 0
  fi

  tmp_md5="$(mktemp)"
  if ! curl -fsSL --retry 3 --retry-all-errors --connect-timeout 30 "$md5_url" -o "$tmp_md5"; then
    rm -f "$tmp_md5"
    return 0
  fi

  expected_md5="$(awk '{print $1}' "$tmp_md5" | head -n 1)"
  rm -f "$tmp_md5"

  if [ -z "$expected_md5" ]; then
    return 0
  fi

  actual_md5="$(md5sum "$target_file" | awk '{print $1}')"
  if [ "$expected_md5" != "$actual_md5" ]; then
    return 1
  fi

  return 0
}

mark_failed() {
  local err_msg="$1"
  if [ -n "$RUN_ID" ]; then
    local escaped_err
    escaped_err="$(printf "%s" "$err_msg" | sed "s/'/''/g")"
    psql_cmd -c \
      "UPDATE import_runs
       SET status='failed', finished_at=now(), error_text='${escaped_err}'
       WHERE id=$RUN_ID;" || true
  fi
}

trap 'mark_failed "line ${LINENO}: ${BASH_COMMAND}"' ERR

echo "Waiting for PostgreSQL..."
until PGPASSWORD="$DB_PASSWORD" pg_isready -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; do
  sleep 2
done

run_id_raw="$(
  psql_cmd -Atc "
    INSERT INTO import_runs (source, region, source_url, source_file, status)
    VALUES ('osm', 'moscow', '$OSM_PBF_URL', '$OSM_PBF_FILE', 'running')
    RETURNING id;
  "
)"

RUN_ID="$(printf "%s\n" "$run_id_raw" | tr -d '\r' | awk '/^[0-9]+$/ { print; exit }')"

if [ -z "$RUN_ID" ]; then
  echo "Could not parse import run id from psql output:"
  printf "%s\n" "$run_id_raw"
  exit 1
fi

echo "Import run id: $RUN_ID"

mkdir -p "$(dirname "$OSM_PBF_FILE")"

need_download="0"
if [ "$OSM_FORCE_DOWNLOAD" = "1" ]; then
  need_download="1"
elif [ ! -f "$OSM_PBF_FILE" ]; then
  need_download="1"
else
  current_size="$(wc -c < "$OSM_PBF_FILE")"
  if [ "$current_size" -lt "$OSM_MIN_BYTES" ]; then
    echo "Existing file is too small ($current_size bytes), removing..."
    rm -f "$OSM_PBF_FILE"
    need_download="1"
  else
    if ! validate_pbf "$OSM_PBF_FILE"; then
      echo "Existing file is invalid or incomplete, removing..."
      rm -f "$OSM_PBF_FILE"
      need_download="1"
    elif ! validate_md5 "$OSM_PBF_FILE" "$OSM_PBF_MD5_URL"; then
      echo "Existing file failed checksum validation, removing..."
      rm -f "$OSM_PBF_FILE"
      need_download="1"
    fi
  fi
fi

if [ "$need_download" = "1" ]; then
  echo "Downloading OSM file..."
  curl -fL --retry 5 --retry-all-errors --connect-timeout 30 -C - "$OSM_PBF_URL" -o "$OSM_PBF_FILE"
else
  echo "OSM file already exists, reuse: $OSM_PBF_FILE"
fi

final_size="$(wc -c < "$OSM_PBF_FILE")"
if [ "$final_size" -lt "$OSM_MIN_BYTES" ]; then
  echo "Downloaded file is too small ($final_size bytes). Most likely URL returned HTML instead of PBF."
  exit 1
fi

if ! validate_pbf "$OSM_PBF_FILE"; then
  echo "Downloaded file is invalid or incomplete."
  exit 1
fi

if ! validate_md5 "$OSM_PBF_FILE" "$OSM_PBF_MD5_URL"; then
  echo "Downloaded file failed checksum validation."
  exit 1
fi

IMPORT_FILE="$OSM_PBF_FILE"

if [ -n "$OSM_EXTRACT_BBOX" ]; then
  mkdir -p "$(dirname "$OSM_EXTRACT_FILE")"

  if [ -f "$OSM_EXTRACT_FILE" ] && [ "$OSM_EXTRACT_FILE" -nt "$OSM_PBF_FILE" ]; then
    echo "Extract already exists and is up to date: $OSM_EXTRACT_FILE"
  else
    echo "Extracting bbox: $OSM_EXTRACT_BBOX"
    osmium extract \
      --bbox "$OSM_EXTRACT_BBOX" \
      --strategy "$OSM_EXTRACT_STRATEGY" \
      --overwrite \
      -o "$OSM_EXTRACT_FILE" \
      "$OSM_PBF_FILE"
  fi

  IMPORT_FILE="$OSM_EXTRACT_FILE"
fi

export PGPASSWORD="$DB_PASSWORD"

echo "Running osm2pgsql..."
osm2pgsql \
  --create \
  --slim \
  --output=pgsql \
  --latlong \
  --hstore-all \
  --extra-attributes \
  --prefix="$OSM_PREFIX" \
  --host="$DB_HOST" \
  --port="$DB_PORT" \
  --database="$DB_NAME" \
  --username="$DB_USER" \
  "$IMPORT_FILE"

echo "Building unified feature layer..."
psql_cmd <<SQL
TRUNCATE TABLE osm_features;

INSERT INTO osm_features (import_run_id, osm_id, source_layer, name, tags, geom)
SELECT
  $RUN_ID,
  osm_id,
  'point',
  name,
  COALESCE(hstore_to_jsonb_loose(tags), '{}'::jsonb),
  way
FROM ${OSM_PREFIX}_point
WHERE way IS NOT NULL

UNION ALL

SELECT
  $RUN_ID,
  osm_id,
  'line',
  name,
  COALESCE(hstore_to_jsonb_loose(tags), '{}'::jsonb),
  ST_PointOnSurface(way)
FROM ${OSM_PREFIX}_line
WHERE way IS NOT NULL

UNION ALL

SELECT
  $RUN_ID,
  osm_id,
  'polygon',
  name,
  COALESCE(hstore_to_jsonb_loose(tags), '{}'::jsonb),
  ST_PointOnSurface(way)
FROM ${OSM_PREFIX}_polygon
WHERE way IS NOT NULL;

ANALYZE osm_features;

UPDATE import_runs
SET
  status='success',
  finished_at=now(),
  rows_point=(SELECT COUNT(*) FROM ${OSM_PREFIX}_point),
  rows_line=(SELECT COUNT(*) FROM ${OSM_PREFIX}_line),
  rows_polygon=(SELECT COUNT(*) FROM ${OSM_PREFIX}_polygon),
  rows_features=(SELECT COUNT(*) FROM osm_features)
WHERE id=$RUN_ID;
SQL

echo "Import finished successfully. Run id: $RUN_ID"