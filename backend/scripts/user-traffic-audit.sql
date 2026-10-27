-- Read-only capacity and reconciliation check. Run explicitly against the
-- intended database: psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f ...
-- Never changes or deletes events. The reconciliation scans only the previous
-- complete UTC hour, and compares both tables in one consistent snapshot.
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';

SELECT c.relname,
       c.reltuples::bigint AS estimated_rows,
       pg_size_pretty(pg_table_size(c.oid)) AS table_size,
       pg_size_pretty(pg_indexes_size(c.oid)) AS index_size,
       pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size
FROM pg_class c
WHERE c.oid IN ('user_traffic_events'::regclass, 'user_traffic_hourly'::regclass)
ORDER BY c.relname;

WITH boundary AS (
    SELECT date_trunc('hour', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS end_at
), raw AS (
    SELECT date_trunc('hour', started_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS bucket_start,
           user_id, api_key_id, group_id,
           SUM(upload_bytes) AS upload_bytes, SUM(download_bytes) AS download_bytes,
           COUNT(*) AS requests
    FROM user_traffic_events, boundary
    WHERE started_at >= end_at - INTERVAL '1 hour' AND started_at < end_at
    GROUP BY 1, 2, 3, 4
), hourly AS (
    SELECT h.* FROM user_traffic_hourly h, boundary
    WHERE bucket_start >= end_at - INTERVAL '1 hour' AND bucket_start < end_at
)
SELECT COALESCE(r.bucket_start, h.bucket_start) AS bucket_start,
       COALESCE(r.user_id, h.user_id) AS user_id,
       COALESCE(r.api_key_id, h.api_key_id) AS api_key_id,
       COALESCE(r.group_id, h.group_id) AS group_id,
       r.requests AS raw_requests, h.requests AS hourly_requests,
       r.upload_bytes AS raw_upload, h.upload_bytes AS hourly_upload,
       r.download_bytes AS raw_download, h.download_bytes AS hourly_download
FROM raw r FULL JOIN hourly h USING (bucket_start, user_id, api_key_id, group_id)
WHERE r.requests IS DISTINCT FROM h.requests
   OR r.upload_bytes IS DISTINCT FROM h.upload_bytes
   OR r.download_bytes IS DISTINCT FROM h.download_bytes
ORDER BY 1, 2, 3, 4
LIMIT 100;

ROLLBACK;
