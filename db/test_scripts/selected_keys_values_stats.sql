SELECT
  kv.key   AS tag_key,
  kv.value AS tag_value,
  COUNT(*)::bigint AS objects_count
FROM osm_features f
CROSS JOIN LATERAL jsonb_each_text(f.tags) AS kv(key, value)
WHERE kv.key IN (
  'amenity',
  'shop',
  'leisure',
  'tourism',
  'historic',
  'healthcare',
  'public_transport',
  'railway',
  'highway',
  'parking',
  'natural',
  'waterway',
  'landuse',
  'man_made',
  'emergency',
  'craft',
  'office'
)
GROUP BY kv.key, kv.value
HAVING COUNT(*) >= 20
ORDER BY kv.key, objects_count DESC, kv.value;