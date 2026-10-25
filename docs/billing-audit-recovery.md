# Billing audit recovery boundary

New batch image jobs use pricing snapshot version 2. Their complete hold and
actual charge are rounded once to eight USD decimal places with the existing
half-away-from-zero monetary rule. Prices, discounts and account cost rates are
unchanged. The persisted hold is the exact amount moved between spendable and
frozen balance. Version 0/1 jobs with an exact eight-place hold retain their
existing behavior.

Version-2 holds also carry individual ownership and a held/captured/released
receipt, committed with the money operation. Capture and release are mutually
exclusive even when another job has enough money in the user's frozen pool.
Free jobs retain their zero-money behavior. Existing holds are not backfilled.

A legacy amount with more than eight places cannot execute a new money change.
A retry first makes a read-only hot/archive receipt check using the original
operation ID, key, owner, batch, amounts and payload fingerprint. An exact match
confirms that operation already committed, so the task status can catch up
without moving money. A missing/conflicting receipt fails; a fingerprint conflict
is never itself evidence that a refund succeeded. No pooled balance inference,
historical rounding, backfill, refund or back-charge is supplied.

Ordinary usage is staged synchronously as a versioned immutable event before
monetary execution. The money effects, dedup claim, raw quote, canonical usage
and applied receipt commit together. A worker retries only pending events and
idempotent cache invalidations. A lost commit response is checked against the
durable receipt before any replay. Hot and archived legacy claims remain
authoritative; they are not permission to fabricate history or charge again.

There remains an explicit observation boundary: a process can die after the
upstream produces usage but before the complete event has committed to the
journal. A database unavailable at that boundary also prevents durable delivery.
The application cannot reconstruct final usage it never persisted and does not
call the model again or invent a zero-cost success. This change removes the
in-memory queue window and recovers events committed before execution; it does
not promise that this pre-storage window or unknown upstream usage is solved.

Explicit recovery would first need an authorized reconciliation of the original
hold transaction, all other holds for that user, captures/releases and current
balances. An individually reviewed correction would require separate approval,
an immutable correction identity and a transaction proving both ownership and
conservation. This PR supplies no operator action to perform that correction.

New ordinary platform-quota increments commit with the charge and dedup claim,
independently of the legacy flusher flag. `usage_platform_quota_totals` keeps the
new projection separate from the existing baseline. The effective view combines
only matching current windows; old events cannot rewind a newer window. Explicit
administrator resets update both components in one transaction. Quota removal
and recreation use different quota row identities.

The combined read cache has its own namespace. It is invalidated idempotently;
new billing never replays Redis monetary deltas. Legacy dirty entries retain
their original namespace so the old flusher can finish its baseline snapshots
without overwriting or double-counting new deltas. Missing/old-schema legacy
increments return an explicit not-applied error. No historical cache values are
imported, no missing old charges are inferred, and no balance adjustments occur
at migration time. Existing legacy backlog requires separate evidence and must
not be described as repaired by this PR.

The customer quote is stored in scale-free NUMERIC as the shortest exact decimal
round-trip of the original float64 quotation. `quote_encoding_version=1` marks
that representation; version 0 may have already lost digits under the old
ten-place schema and is not retrospectively repaired. `canonical_actual_cost`
comes from the already-normalized customer command, never a re-rounded stored
quote. Canonical
usage and cache reloads use that same customer amount. `total_cost` and the
account cost multiplier retain their independent meaning. A subscription or
simple-mode key-window charge is not new cash revenue.

Mixed old financial writers/binaries are a release blocker, including a rolling
period in which an old batch worker can claim a version-2 job. Approval requires
an explicitly reviewed admission stop and drain of old in-flight requests,
usage workers, batch workers and flusher cycles (including already-popped
snapshots), then all relevant nodes on the same verified financial protocol and
migrations before new admission. Unknown started work remains occupied; expiry
is not authority to refund or repeat a model request. This document executes no
drain and supplies no automatic migration of old monetary values.

Rollback to an old reader/writer is not approved just because all journal events
are complete: it still cannot account for new projections and owned holds.
A rollback build must understand those states, or a separate, explicitly
authorized reconciliation/drain plan must be reviewed. No command in this PR
merges historical money or projections into a legacy ledger.

Quota window starts retain precise administrative reset cutoffs. Day/week
delivery uses the later of that cutoff and the natural boundary; an event before
the cutoff contributes zero to that window, even if it is the first projection
row. Ordinary and regional writers read the same effective monthly anchor under
the baseline row lock, and advance together only at the 30-day boundary. Checked
auth invalidation errors (including key lookup and broadcast) leave cache work
pending; an unchecked/absent invalidator cannot acknowledge key-quota exhaustion.
