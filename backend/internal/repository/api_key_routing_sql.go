package repository

import "fmt"

// Arguments are fixed SQL identifiers/placeholders, never request text. Keep
// canonical policies in the same statement/row lock as bulk binding changes.
func rewriteAPIKeyRoutingSQL(column, oldID, newID string) string {
	return fmt.Sprintf(`CASE WHEN %[1]s IS NULL THEN NULL ELSE jsonb_set(%[1]s, '{routes}',
 COALESCE((SELECT jsonb_agg(route ORDER BY ord) FROM (
  SELECT DISTINCT ON ((mapped->>'group_id')::bigint) mapped AS route, ord
  FROM (
   SELECT CASE WHEN (item->>'group_id')::bigint = %[2]s THEN
    CASE WHEN %[3]s::bigint IS NULL THEN NULL ELSE jsonb_set(item,'{group_id}',to_jsonb(%[3]s::bigint)) END
    ELSE item END AS mapped, ord,
    CASE WHEN (item->>'group_id')::bigint = %[2]s THEN 1 ELSE 0 END AS replaced
   FROM jsonb_array_elements(%[1]s->'routes') WITH ORDINALITY AS e(item,ord)
  ) mapped_routes WHERE mapped IS NOT NULL
  ORDER BY (mapped->>'group_id')::bigint, replaced, ord
 ) unique_routes), '[]'::jsonb)) END`, column, oldID, newID)
}
