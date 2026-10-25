-- Removing the typmod preserves all decimal digits of new quote snapshots.
-- Existing ten-place values cannot be made lossless retrospectively: version 0
-- explicitly marks their provenance as unknown/rounded, without backfilling it.
ALTER TABLE usage_billing_quotes ALTER COLUMN quoted_actual_cost TYPE NUMERIC;
ALTER TABLE usage_billing_quotes ADD COLUMN quote_encoding_version SMALLINT NOT NULL DEFAULT 0;
COMMENT ON COLUMN usage_billing_quotes.quoted_actual_cost IS
    'Version 1: exact shortest decimal round-trip of the original float64 quotation; version 0 may already be rounded';
COMMENT ON COLUMN usage_billing_quotes.canonical_actual_cost IS
    'Committed normalized customer amount from the billing command, never recomputed from a stored quote';
