-- Additive upgrade: never change migration 282 after it has been distributed.
-- Both API keys and subscriptions can change groups. Neither current row proves
-- an old allocation's original admission group, so never backfill from them.
ALTER TABLE regional_budget_allocations ADD COLUMN group_id BIGINT;
ALTER TABLE regional_budget_allocations ADD CONSTRAINT regional_allocation_group_positive CHECK(group_id IS NULL OR group_id>0);
-- NULL group is explicit unknown attribution. Existing money and permanent IDs
-- remain intact. Stop new admissions before this upgrade and drain old grants.
-- Runtime refuses new primary admissions/grants while any unknown held allocation
-- exists; verified late settlement retains NULL group metadata, never guesses it.

-- Funding kind/subscription ID are already persisted in the old allocation.
-- Repair that proven metadata without guessing a historical group or changing
-- money, event identities, outbox acknowledgments or spent counters.
UPDATE usage_logs u SET subscription_id=NULLIF(a.subscription_id,0),
 billing_type=CASE WHEN a.subscription_id=0 THEN 0 ELSE 1 END,billing_mode='token'
FROM regional_budget_allocations a
WHERE a.allocation_id='request:'||u.request_id AND a.api_key_id=u.api_key_id;
UPDATE usage_logs u SET subscription_id=NULLIF(a.subscription_id,0),
 billing_type=CASE WHEN a.subscription_id=0 THEN 0 ELSE 1 END,billing_mode='token'
FROM regional_settled_events e,regional_budget_allocations a
WHERE e.event_id=u.request_id AND a.allocation_id='grant:'||e.lease_id AND a.api_key_id=u.api_key_id;
