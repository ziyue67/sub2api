# Key group protocol filtering and visible rates

## Problem

The unified routing editor used plain group-name options and a small gray rate
outside the dropdown. The creation-only provider cards grouped several different
platforms together, and editing a key had no protocol/platform filter.

## Behavior

- One protocol/platform filter in the shared routing editor, available during both
  creation and editing. Values come from available groups' configured platform,
  never inferred from their names. Composite groups remain a separate category.
- All platforms are shown initially. Filtering only changes candidates for adding
  or replacing a route. Every selected route stays visible with its settings intact.
- Names, platform labels and prominent rate badges appear in both selected values
  and searchable dropdown options. User-specific rates, including zero, take
  precedence; the default rate remains visible with a strikethrough when overridden.
  Subscription and peak-window indicators are retained.
- Unknown historical selected group IDs are shown as disabled `#ID` options, rather
  than silently replaced. Opening/filtering a legacy key does not convert its policy.
- This is a selection aid, not a new endpoint restriction or protocol conversion.
  Routing, pricing calculation, permissions and database schema are unchanged.

## Verification

Three new editor regressions failed before the change and passed afterwards.
The editor, actual KeysView creation/editing tests and locale completeness checks
pass (83 tests). Typecheck and production bundle were validated separately.

A full frontend run exposed 34 failures in seven unrelated existing suites
(Grok diagnostics/model sync, security audit navigation, admin permission labels,
settings anchors and account links). Running those same suites from pristine
`39ef9b97e` reproduced them. An additional missing translation introduced here
was corrected and the locale completeness test rerun successfully. These existing
failures are not removed or skipped by this change. The dedicated CI job covers
the touched UI and the existing routing regression job remains in place.

## Build security follow-up

The PR scan found GO-2026-6859 in the existing Go 1.27.3 standard library.
The build images, go.mod and CI version assertions are updated together to Go
1.27.4, which fixes the net/http MaxBytesReader header race. No scan exception
is added and no application billing/routing code changes are included.
