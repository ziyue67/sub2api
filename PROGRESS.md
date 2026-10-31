# Issue #594 — OpenAI multiple authorization connections

## Contract and boundaries

- Sole writer: Codex, `gpt-6.1-sol`, explicitly `xhigh` (user-supplied session declaration). Hermes: `gpt-6.1-sol`, reasoning unknown; requirements/Issue/parent work only; no parent verification claimed.
- Worktree `/data/development/projects/sub2api-openai-multi-auth`; branch `feat/openai-multi-auth-connections`; baseline `12a49ef4ef6a2f116389701ac8f1fe620f27bbf5`; artifacts `/data/development/artifacts/sub2api-openai-multi-auth`.
- Read full Issue #594 and paginated comments (none), CONTRIBUTING.md, DEV_GUIDE.md, all eight PR-template sections. No AGENTS.md found in worktree or ancestors.
- No other worktrees or PR #591/#593. Ordinary commit/push; production PR base. No rebase/force push/PR merge/release/deploy.
- No local Go compile/test/run/generate, Docker, full frontend build/lint/typecheck. Only gofmt, bash -n, static/diff, lightweight scripts. Dynamic/generated/UI evidence: isolated dedicated remote CI.
- No production, real credentials, paid inference, real mail, challenge bypass or impersonated registration. Actual consent/commercial SIWC approval stays manual.
- Preserve billing formulas, multipliers, ledgers, account security, concurrency and shared limits; unknown entitlement sharing must restrict rather than multiply quota.

## Existing implementation audit

- BPS already has a dedicated OAuth client/callback/transport; reuse it.
- SIWC #485/#491/#498/#511 merged history read and archived. #498 verifies ID tokens instead of decoding opaque access metadata; #511 adds encrypted shared sessions and eligibility diagnostics. Preserve these and #592 Redis 6.0 compatibility.
- `openai_siwc_credentials` currently keys one registration by account_id; Codex/BPS share the account credentials map. SIWC has account-scoped refresh locks/CAS and a route-selection boolean. This is not arbitrary concurrent registrations.
- Application-user auth identities/channels are sign-in identities, not inference grants. Never email-merge them into upstream connections. `swic` remains only the original typo for SIWC.
- Official SIWC sessions/inference docs read: separate issued client/token sets, serialized rotating refresh, public Responses API, store=false/stream=true, separate identity consent and shared plan limits.

## Acceptance checklist (unchecked = incomplete)

- [x] Stable account identity → typed connections: issuer/client/verified subject, workspace/resource/audience/scopes, host/session; no email-only identity merge.
- [x] Independent protected credentials, real client refresh, CAS and cross-instance coordination; reauthorization/disable/unbind cannot overwrite peers.
- [x] Real production DTO → service → repository → HTTP → management UI integration.
- [x] Capability/credential/transport separation: Codex/BPS/SIWC/API key; reject unknown/identity-only grants without silent fallback.
- [x] zh/en list/status/capabilities, add/reauthorize/select/disable/unbind; no secrets returned.
- [x] Scheduler/stickiness/cooldown/errors/usage connection attribution; preserve account-wide security, shared quotas and concurrency.
- [x] Repeatable lossless legacy mapping, defaults/rollback; old API and merged SIWC/BPS fixes preserved.
- [x] Remote RED before GREEN; actual adapters compile; real HTTP → repository → isolated PostgreSQL → GET.
- [ ] CI coexistence/concurrent rotation/reauthorization/single unbind/routing/rejection/legacy upgrade; no single-file mock substitute.
- [x] UI component tests and sanitized remote screenshots.
- [x] First real code plus RED committed/pushed promptly; one Draft PR to production, Refs #594, complete eight-section template and unfinished checklist.
- [ ] Final exact-head push/PR checks and statuses paginated; raw failures read/fixed; independent review recorded without fabricated parent verification.

## Delivery and evidence

- Initial clean baseline confirmed. No local dynamic checks. Implementation/CI pending.
- Draft PR/latest head/exact-head pass/fail/pending/skipped: pending first reviewable implementation.
- Regression-first commit `cc30c542f` and dedicated original-baseline RED workflow `a3b1adf9a` pushed before implementation. Raw results pending.
- First production slice: typed safe DTO, forward-only connection table, encrypted API-key creation/pending OAuth registrations, list/select/disable/enable/local-unlink HTTP→service→SQL, revision CAS, connection lease and unknown/identity-only dispatch rejection. OAuth callbacks, actual selected-connection inference, UI and integration acceptance remain incomplete.
- Draft PR: https://github.com/ranxi2001/sub2api/pull/595 (production, Refs #594), first implementation `1029ba36b`; OAuth lifecycle follow-up `6a38d2408`.
- Original production RED raw job `123258743040` read and archived: all three unknown/swic/identity-only cases fail because legacy dispatch returns Codex credentials, as expected. Exact-head GREEN remains pending.
- Audit correction: BPS ALSO already has an independent encrypted side-grant table (`OpenAIExcelOAuthRepository`), not only the primary credentials slot. Legacy mapping will preserve all original lifecycle owners instead of copying rotating tokens.
- Current work adds explicit selection policies, metadata/outbox propagation, capability routing/projection, per-connection failure isolation and stable read-only legacy views. HTTP→actual service→SQL→GET/rotation/unlink/CAS/lease integration added for isolated CI. UI, full OAuth integration/usage attribution and final checks remain pending.
- Existing baseline dependency security failure: GHSA-83rm-6c8r-g728 in vue-i18n/@intlify; raw failure read. No exception or false pass added.
- Historical evidence read: `efe10013b` actual HTTP/PostgreSQL lifecycle and cross-instance CAS/lease passed, no Issue594 skips; raw `green-efe10013b-raw.log`. `218bd3022` UI: 19 tests, targeted lint/typecheck and actual en/zh desktop/mobile screenshots passed; raw and images archived and inspected. These do not count as final-head evidence.
- Full actual OAuth adapter integration added for two SIWC registrations plus Codex/BPS, synthetic issuer-signed identities, identity-only rejection, cross-instance rotating refresh, reauthorization, single remote revoke and primary-grant preservation. Production gateway route/affinity/account-ban/usage-attribution integration added. Current results pending.
- Session affinity now persists connection identity under the existing tenant/session scope. Credential/status CAS does not alter peer tokens during selection. Reauthorization and revoke use the same owned DB lease as refresh; final send checks the live connection revision.
- To fix baseline GHSA-83rm-6c8r-g728 without audit exceptions: official advisory read, isolated CI generated vue-i18n/compiler 11.4.14 lock candidate, unrelated lock metadata preserved after review; frontend/security/dedicated CI Node updated to required 22. Exact-head validation pending.
- Raw `green-218bd3022-raw.log` exposed the new connection path's reuse of a legacy audience-array-only decoder. New connection metadata parsing now happens only after the real adapter verifies the signed token, accepts standard string/array audience, and requires an actual workspace claim instead of substituting a session ID.
- Raw security job on `a1edcb7f2` exposed an incorrect importer restoration while retaining unrelated lock metadata. Corrected to the remotely generated 11.4.14 importers; original xlsx integrity/platform metadata retained. No audit exception added.
- Source audit added authoritative DB selection-policy checks at preparation and final send so pre-selection cached account snapshots cannot silently use legacy credentials after another instance enables managed routing.
- Additional source review hardened encrypted records with connection/account/kind/subject binding; transplanting a ciphertext into a peer connection is rejected. Callback and revocation operations now have an overall timeout below the owned lease lifetime. Added actual DB regression for ciphertext transplant rejection.
- `dea23abe8` push and PR security scans pass after the i18n/lock fixes; other exact-head jobs pending. All earlier failed jobs' raw logs are archived, including the intentionally red test-only commit.
- BPS review: managed grants now bypass legacy account-group auto-move/auto-disable; model/credential failure cools the connection. Managed 429 follows conservative shared account policy. Its observed HTTP send boundary validates live policy and grant revision too. Legacy BPS behavior is unchanged.
- Observational attribution includes regional-metered Responses/Chat results while preserving all original money/ledger handling. Legacy registration labels now use the panel locale.
- Final regression additions exercise stale legacy snapshots after managed selection and a shared account rate-limit window through the real Gateway. Explicit return-to-legacy policy is asserted in the real DB. Local static review: 39 changed Go files gofmt-clean; 47 new zh/en locale keys match; complete eight-section PR body retained; diff clean. No local dynamic test was run.
- Full-CI raw review found two staticcheck style findings (capitalized `Codex` error, De Morgan expression) and the existing plugin snapshot field-classification tripwire. Fixed style and explicitly classified the new UUID/revision as non-secret readable metadata; no test guard was removed. The credential-operation seam's expected baseline RED cases were not real current-head regressions; its actual job failure was the already-fixed old lock importer.
- Actual adapter integration now includes two Codex and two BPS registrations with the same verified subject/client plus two SIWC registrations and one identity-only grant. Each retains its own token set/UUID; there is no uniqueness constraint that would collapse same-client connections. This closes the explicit per-kind coexistence coverage before final exact-head verification.

## Implementation completion and remaining gates

- Implementation checklist above describes delivered source, not a claim that pending exact-head CI passed. Whole-contract final validation remains unchecked until the final head completes all checks.
- Historical full dedicated GREEN at `dea23abe8`: actual adapter compilation and all four Issue594 integration cases passed; raw log archived/read. New per-kind coexistence, ciphertext binding, shared quota and latest fixes require final-head confirmation.
- Independent source reviews were completed on fixed `28a66fc47` by two Hermes agents and are BLOCKING. The sole writer audit does not supersede them. Actual SIWC consent/eligibility/commercial approval likewise remains manual.
- Final local static evidence: actual dedicated workflow's six shell run blocks pass bash -n; changed Go files are gofmt-clean, zh/en keys agree, PR retains all eight sections, diff clean. No local compilation, dynamic test, generation, Docker or full frontend checks.
- Final lifecycle review found that disable/error-state writes could race a rotating refresh. Disable/enable now serialize under the same bounded lease; a rejection cannot discard an in-flight replacement. Managed OAuth 401 now expires only its access token and keeps its refresh grant, preserving the existing SIWC recovery behavior; API keys still require replacement. Added real-DB lease exclusion and actual-adapter 401 recovery regressions. This necessary fix supersedes the previous candidate head.
- Added the explicit real legacy-upgrade scenario required by the contract: a separate isolated PostgreSQL database runs the complete pre-312 production migration set, seeds primary/BPS/SIWC storage slots, then applies the actual new migration runner twice. It asserts byte-preserved credential/host data, unchanged legacy route and no duplicated rotating tokens. This extends upgrade coverage beyond reapplying migrations to an already-current database.
- Full integration raw `full-test-eec044809-raw.log` exposed a real compatibility defect in our new metadata-preserving trigger: legacy scalar `accounts.extra` caused PostgreSQL `cannot delete from scalar` in the unchanged quota statement-variant tests. Added forward-only migration 316 (never rewrote applied migration 313) to preserve scalar/null/array legacy values and normalize only when server-owned metadata exists. Added upgrade regression; financial SQL/formulas were not changed.
- Actual account editor embeds this panel in its own form; source audit removed nested panel forms and made operations explicit type=button controls, with an embedding regression to prove they cannot submit the surrounding account edit.
- Companion forward-only migration 317 makes connection snapshot publication normalize scalar legacy extra to an object, so such accounts can actually enable managed routing. The HTTP→DB fixture now starts from scalar extra. Session-affinity bounded expiry cleanup is a separate statement, avoiding an expired row being deleted and upserted within the same data-modifying CTE.
- Connection namespace audit also scoped BPS replay/catalog/attachment conversation identity to the selected UUID, and scoped the existing Codex ticket identity hash to that UUID. This prevents reusing a primary/peer registration's client state or cookie ticket; existing ticket/security gates remain required, with real acquisition/approval manual rather than bypassed.
- Final policy audit moved the existing Codex ticket check to the projected managed grant, so an API-key/BPS/SIWC connection cannot inherit a primary Codex ticket requirement, while a managed Codex grant still must satisfy its own ticket/security gate. Local-only unlink now also serializes with rotation; revoke performs the same CAS while already holding that lease, avoiding nested lease acquisition.
- Upgrade CI raw `green-40e76d1f2-raw.log` showed the scalar regression fixture was hitting the pre-existing OpenAI long-context object requirement (`cannot set path in scalar`), not the new protection trigger. Corrected the fixture to exercise generic legacy scalar quota rows and restore an OpenAI object before testing new registration; the existing long-context trigger/security was not weakened.

## Blocking independent review remediation

- Consumed both visible final conclusions from `/root/.hermes/cache/delegation/live/deleg_768d7c86/task-0.log` and task-1.log; both block fixed `28a66fc47`. The two queued messages identified by the user are supplied in this thread; no separate queue-reader tool is available here. Current clean checkpoint `19b4e6730` was re-audited against all five findings. Its 35 green checks do not close them.
- Reviewers: two Hermes independent read-only agents, runtime label `gpt-6.1-sol`, reasoning unknown, exact provider mapping unknown (parent supplied). No independent acceptance claimed.
- [ ] Refresh: preserve lease coordination already present on disable/Fail/unlink; add durable before-send refresh intent, unknown-result quarantine, fenced lease owner/expiry and recovery of an obtained replacement without reviving disabled/unlinked state.
- [ ] Managed Codex: prevent legacy ticket/harvest/Cookie/session inheritance and fence every egress; test actual bearer-cookie pairing.
- [ ] BPS: fence each attachment and model-access send after proxy wait, including revocation between attachments.
- [ ] BPS Chat/Messages: correct actual bridge or withdraw unsupported advertised capabilities with accurate UI/docs.
- [ ] HTTP handler→scheduler/slot→exact upstream token/output→real usage attribution; shared quota/concurrency and revoked-content regressions.
- First review regressions use actual OAuth adapter/service/PostgreSQL on checkpoint, asserting no sequential replay after uncertain rotation and stale-lease credential rejection. Added dedicated remote baseline RED before GREEN; implementation unchanged until raw RED is read.
- Read raw remote RED `review-red-40646e222.log`: three instances submitted the old RT 3 times after uncertain exchange (expected 1), and an expired lease holder's stale CAS succeeded (expected reject). Both were real adapter/service/PostgreSQL assertions, no setup/compile failure.
- Implementing durable refresh intent/state before POST, structured not-sent/rejected/uncertain outcomes, encrypted replacement journal with explicit recovery, and owner+epoch+clock expiry fences. Unknown/verification/encryption/storage failures close inference and never automatically replay the old RT. Reauthorization resets the closed state only through the fenced lifecycle path; disable/unlink remain independent.
- Remote review RED `review-red-2decf9910.log` read: inherited empty-identity ticket, client/revision-insensitive identity, advertised broken BPS bridge and unfenced attachment all fail on checkpoint.
- Managed ticket/harvest lookup/storage/eligibility now explicitly rejects account-owned sessions; old Extra ticket/harvest data is removed from projections, outgoing managed requests strip Cookie/ticket and bypass legacy plugin restoration. Existing gated models remain fail-closed rather than borrowing a parent ticket. Ticket identity includes UUID/client/revision.
- BPS attachment and model catalog now call a live egress fence immediately before every HTTP send; proxy fallback attempts fence again. Managed BPS capability is Responses only, including a forward migration removing old persisted claims. UI/docs disclose this boundary and uncertain refresh/recovery.

## Resume after dd0a7fb5a blocking review (2026-10-30)

- Read visible conclusions in `/root/.hermes/cache/delegation/live/deleg_33afb668/task-1.log`, CONTRIBUTING.md, eight-section PR template and `/root/.hermes/skills/openai-multi-auth-contract/SKILL.md`. Review remains blocking; former green CI does not supersede it.
- Sole writer Codex: gpt-6.1-sol, explicit xhigh; independent Hermes review runtime gpt-6.1-sol, reasoning and exact provider mapping unknown. No new delegation or parent verification claimed.
- [ ] Bind the actual acquired owner/epoch to every lifecycle operation; reject A after expiry/B takeover even if A rereads B's snapshot.
- [ ] Real encrypted candidate stage→finish failure→restart recovery; disabled/unlinked cannot be revived.
- [ ] Fix actual HTTP fixture balance/settlement root cause without weakening assertions; execute shared quota/slot checks.
- [ ] Fix QF1003 and unify final SIWC fresh fence, with disable/revision regressions.
- Merge origin/production `76b54ea02` normally to incorporate parent-merged #591; only conflict is PROGRESS.md, retaining this task's checklist. No edits to other PR/worktrees; no rebase/force/PR merge/release/deploy. All dynamic work stays remote CI.
- First fixes: atomic lease acquisition now returns the exact owner/epoch; each post-acquisition service reread must match that immutable proof before callback/refresh/state/revoke/key mutation. Added real service→PostgreSQL A-acquire/expire/B-takeover/A-reread regression for disable, unlink and failure writes. QF1003 converted to tagged switch.
- HTTP settlement root cause: synthetic `gpt-4` had no pricing service or hardcoded entry, invoking existing zero-cost fallback. Fixture now requests an existing nonzero-priced model and asserts pricing plus debit/usage-cost agreement; financial production code is unchanged. SIWC now invokes the same live egress fence immediately before Do, after its intervening preparation.

- Added actual PostgreSQL stage→injected finish write failure→restart recovery regression, including a disabled branch: journal retained encrypted, no automatic enable/recovery, explicit unlink clears it, exactly one rotating POST.
- Added SIWC actual final transport + isolated PostgreSQL regression: initial validation passes, last preparation changes status/revision in the DB, final fence must prevent all HTTP sends. Unchanged branch verifies exact bearer and no Cookie/ticket. Dedicated baseline RED runs both new lease and SIWC boundaries against dd0a7fb5a, before head GREEN.
- Remote first-fix security raw exposes existing transitive decode-uri-component GHSA-qr28-fpjm-wq7f; official advisory states patched 0.5.1. Isolated lock candidate job now generates a narrowly scoped override; no audit exception. Candidate lock still pending remote generation.

- Downloaded remote lock candidate from exact `05f9f40d0`, reviewed the diff: only decode-uri-component override/dependency 0.4.1→0.5.1 plus its integrity. Unrelated lock metadata retained. No local install/build/lint/typecheck or audit exception. UI now offers candidate recovery only for active registrations and explains disabled registrations require explicit reauthorization/unlink.

- Read first new RED raw: disable/unlink/failure/revoke reproduce A adopting B owner. The added refresh subcase revealed a fixture mistake (attempted to change immutable kind from API key to SIWC, so no lease path ran). Corrected it to create SIWC initially; strengthened RED to reject that missing-path failure and require the stale worker's actual network-operation assertion. No assertion weakened.

### Resume remediation evidence and handoff gates

- [x] This call's lease proof is atomically returned and retained across every lifecycle reread; owner/epoch cannot be replaced by a peer's snapshot. SQL owner/epoch/clock-expiry fence remains in force.
- [x] Actual DB candidate stage→finish failure→service restart recovers once without another rotating POST. Disabled candidate is retained encrypted, recovery/enable refused; reauthorization/unlink is explicit and unlink erases it.
- [x] Real HTTP fixture now has asserted nonzero model pricing and actual debit/usage-cost agreement. Existing balance assertion retained; following shared quota and account-slot assertions execute.
- [x] QF1003 fixed; SIWC calls the shared fresh fence at the final send boundary, with real DB disable/revision changes after initial validation.
- Source-head evidence `03a5afb6c57713c17fbdb2ea88621d8cdbe7e6c3`: dedicated push job `123878636238` GREEN raw read (`green-03a5afb6c-push.log`). HTTP/debit/slots/quota, all five lease-takeover branches, candidate recovery active/disabled branches, and SIWC unchanged/disable/revision all pass; no Issue594 skip.
- Valid baseline RED raw `lease-siwc-red-03a5afb6c.log` read: dd0a7fb5a fails all five lease takeover branches, including actual stale-worker OAuth network attempt; SIWC unchanged passes and disable/revision wrongly send. RED forbids missing-path/setup/build errors as evidence.
- Latest full PR/push checks still pending when this source record is committed. Final exact-head checks/statuses/raw go in PR and artifact handoff; historical GREEN never implies the new head or independent source approval.
- Fixed dd0a7fb5a independent review remains completed/blocked until parent reviews these fixes; no new parent approval is invented. Managed account ticket/Cookie inheritance remains disabled; BPS Responses-only remains explicit. Merge/release/deploy is solely parent's decision, none performed here.

- Final pre-handoff check found parent had merged #593 while CI ran: production advanced to `146c4ed23b6c44c8f394e135f7e6a881fc0c26c2`. Normal merge resolves locale registration conflicts by retaining both openaiAuth/upstreamEntities; dependency conflicts retain production's newer patched vue-i18n/compiler 11.4.16 and matching lock, including the same decode URI fix. No patching of PR593 itself. Candidate-generation CI no longer downgrades production's dependency versions.
- Previous exact `9bf6b0dca` completed 43 pass / 0 fail / 0 pending / 5 skipped, all core lease/rotation/HTTP/SIWC raw read; after this required base sync it is historical evidence. Synced head must complete exact-head checks anew. Parent source approval/merge remains pending.
- Parent production advanced again during final verification: #596 merged to `99f25942738ff12056ac82d97ee6b74cb07cd163`. Normal merge retained production's more complete Node/runtime contribution guidance (the only conflict); no implementation conflict. Prior exact `2555e38fe` completed 45 pass / 0 fail / 0 pending / 5 skipped, core raw read. The new synchronized head must be verified again; no PR merge/release/deploy performed.
