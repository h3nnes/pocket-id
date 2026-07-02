# Upstream Merge + Claims Remapping Port Design Spec

**Date:** 2026-07-02

## Goal

Bring `pocket-id-fork` up to date with `pocket-id-upstream` (74 commits ahead, last synced at `bb5a111`) while keeping the custom per-client claim remapping feature working. All backend unit tests, frontend svelte-check, and Playwright e2e tests must pass after the merge.

## Context

### The Fork's Custom Feature

The fork adds **per-OIDC-client claim remapping**: an admin can configure rules on each OIDC client that rewrite standard claims (e.g. `email`) to pull their values from a different source — a user field, a custom claim key, or a static value. If the source is not found, the rule falls back to the original claim value. There are three source types: `user_field`, `custom_claim`, `static`.

The feature was introduced in commit `8af475d` and refined in six subsequent commits. It has no existing tests.

### The Upstream Refactor

The most significant upstream change since the fork's sync point is PR `#1520` ("use fosite for OAuth 2.0 logic"), which:
- Migrated all OAuth 2.0 authorization/token/userinfo logic out of `oidc_service.go` into a new `backend/internal/oidc/` package.
- Moved the claim-building function (`getUserClaims`) into `backend/internal/oidc/claims_service.go` as `GetUserClaims`.
- Reduced `backend/internal/service/oidc_service.go` from ~2453 lines (fork) to ~854 lines (upstream) — it now only handles OIDC client CRUD.

Other notable upstream changes: new fields on `OidcClient` (`RequiresPushedAuthorizationRequests`, `SkipConsent`, `PkceSupported`), updated `OidcClientFederatedIdentity` (added `ReplayProtection`), updated frontend types and form.

## Architecture

### Merge Strategy

Use `git merge` with the upstream repo added as a git remote. Conflicts will be resolved file by file, taking the upstream version as the base and re-applying custom additions on top.

### Where the Feature Lives After the Merge

| Component | Before (fork) | After (merged) |
|---|---|---|
| Claim remapping model types | `model/oidc.go` | `model/oidc.go` (merged) |
| Claim remapping DTO + validation | `dto/oidc_dto.go` + `service/oidc_service.go` | `dto/oidc_dto.go` (merged) + `oidc/claims_service.go` |
| `applyClaimRemappings` logic | `service/oidc_service.go` | `oidc/claims_service.go` |
| `validateClaimRemappings` | `service/oidc_service.go` | `service/oidc_service.go` (CRUD validation) |
| CRUD wiring (save/load remappings) | `service/oidc_service.go` | `service/oidc_service.go` (merged) |
| Frontend types | `frontend/src/lib/types/oidc.type.ts` | same file (merged) |
| UI component | `claim-remappings-input.svelte` (new file, keep) | same file (unchanged) |
| Form integration | `oidc-client-form.svelte` | same file (merged) |

### Key Design Decisions

**`GetUserClaims` signature stays unchanged in upstream.** The upstream `GetUserClaims(ctx, userID, scopes)` does not know about the client. The claim remapping feature needs the client's `ClaimRemappings`. The approach: add an optional `*model.OidcClient` parameter to `GetUserClaims`, defaulting to `nil` (no remapping). All existing callers pass `nil`; the preview builder passes the client. This keeps the interface change minimal and backward-compatible within the codebase.

**`applyClaimRemappings` moves to `oidc/claims_service.go`.** It is a pure function that transforms a claims map; it belongs with the other claim logic.

**`validateClaimRemappings` stays in `service/oidc_service.go`.** It validates DTO input during client create/update — it belongs with the CRUD validation layer.

**Reserved claims list stays in `service/oidc_service.go`.** It is used only during DTO validation at create/update time.

**No new DB migrations needed.** Remappings are stored as JSON inside the existing `OidcClientCredentials` column, so no schema change is required.

## Files Changed

### Backend

- `backend/internal/model/oidc.go` — add back `OidcClientClaimRemapping`, `ClaimRemappingSourceType`, constants; merge upstream's new `OidcClient` fields and `OidcClientFederatedIdentity.ReplayProtection`
- `backend/internal/dto/oidc_dto.go` — add back `OidcClientClaimRemappingDto` to `OidcClientCredentialsDto`; merge upstream's new DTO fields (`RequiresPushedAuthorizationRequests`, `SkipConsent`, `ReplayProtection`)
- `backend/internal/service/oidc_service.go` — add back `validateClaimRemappings`, `validUserFieldSources`, `reservedClaimsForRemapping`, and ClaimRemappings CRUD wiring in `updateOIDCClientModelFromDto`; merge upstream's new fields
- `backend/internal/oidc/claims_service.go` — add `applyClaimRemappings` method; extend `GetUserClaims` to accept optional `*model.OidcClient` and call `applyClaimRemappings` when client has remappings
- `backend/internal/oidc/claims_service_test.go` — add tests for `applyClaimRemappings` covering all three source types, fallback behavior, and static JSON parsing

### Frontend

- `frontend/src/lib/types/oidc.type.ts` — add back `ClaimRemappingSourceType`, `OidcClientClaimRemapping`; add to `OidcClientCredentials`; merge upstream's new fields (`requiresPushedAuthorizationRequests`, `skipConsent`, `replayProtection`, `pkceSupported`)
- `frontend/src/routes/settings/admin/oidc-clients/claim-remappings-input.svelte` — keep as-is (no upstream equivalent)
- `frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte` — re-apply the `ClaimRemappingsInput` import and usage on top of upstream's updated form

## Testing

- **Backend unit:** `cd backend && go test -tags=exclude_frontend ./...`
- **Frontend check:** `pnpm check` from repo root
- **E2e:** `cd tests/setup && docker compose up -d --build` then `pnpm test` from repo root

New unit tests for `applyClaimRemappings` cover:
1. `user_field` source — maps correctly, falls back when field is nil (e.g. email not set)
2. `custom_claim` source — maps correctly, falls back when key not found in custom claims
3. `static` source — string value, JSON object value
4. No remappings — claims map unchanged
5. Multiple remappings — all applied in order
