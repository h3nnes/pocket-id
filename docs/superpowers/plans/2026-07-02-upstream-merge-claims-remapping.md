# Upstream Merge + Claims Remapping Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge 74 upstream commits into `pocket-id-fork` via `git merge` and port the custom per-client claim remapping feature to the new `backend/internal/oidc/claims_service.go` architecture, with all tests passing.

**Architecture:** Add upstream as a git remote, merge, resolve conflicts in 6 files by taking the upstream base and re-applying the fork's custom claim remapping additions. Move `applyClaimRemappings` from `service/oidc_service.go` into `oidc/claims_service.go`; extend `GetUserClaims` to accept an optional client for remapping. Write new unit tests.

**Tech Stack:** Go 1.24+, SvelteKit 5 (Svelte 5 runes), Playwright, pnpm, Docker Compose

**Repo paths:**
- Fork: `/home/henrik/gitea-projects/pocketid/pocket-id-fork`
- Upstream: `/home/henrik/gitea-projects/pocketid/pocket-id-upstream`

---

## Task 1: Add upstream remote and run git merge

**Files:**
- No file edits — git operations only

- [ ] **Step 1: Add the upstream remote**

Run from `/home/henrik/gitea-projects/pocketid/pocket-id-fork`:
```bash
git remote add upstream ../pocket-id-upstream
git fetch upstream
```
Expected: fetch completes, `upstream/main` is now available.

- [ ] **Step 2: Start the merge**

```bash
git merge upstream/main --no-commit --no-ff -m "chore: merge upstream main into fork (claims remapping port)"
```
Expected: merge will stop with conflicts. Output lists conflicted files. Do NOT commit yet — conflicts must be resolved first.

- [ ] **Step 3: Confirm which files are conflicted**

```bash
git status
```
Expected output includes files marked `both modified:` — at minimum: `backend/internal/model/oidc.go`, `backend/internal/dto/oidc_dto.go`, `backend/internal/service/oidc_service.go`, `frontend/src/lib/types/oidc.type.ts`, `frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte`. Note any additional conflicted files for resolution in subsequent tasks.

---

## Task 2: Resolve conflict in `backend/internal/model/oidc.go`

**Files:**
- Modify: `backend/internal/model/oidc.go`

Goal: Start from upstream's version (which has the new `OidcClient` fields, `PkceSupported`, `SkipConsent`, `RequiresPushedAuthorizationRequests`, updated `OidcClientFederatedIdentity` with `ReplayProtection`, and removed `OidcAuthorizationCode`/`OidcRefreshToken` types). Re-add the fork's custom types `OidcClientClaimRemapping` and `ClaimRemappingSourceType`.

- [ ] **Step 1: Take upstream's version as the base**

```bash
git checkout --theirs backend/internal/model/oidc.go
```

- [ ] **Step 2: Open the file and add the custom types**

The upstream file ends with `OidcDeviceCode` (or similar). After the `OidcClientCredentials` struct (which in upstream only has `FederatedIdentities`), add `ClaimRemappings` field and the supporting types.

The file's `OidcClientCredentials` struct currently looks like:
```go
type OidcClientCredentials struct { //nolint:recvcheck
	FederatedIdentities []OidcClientFederatedIdentity `json:"federatedIdentities,omitempty"`
}
```

Change it to:
```go
type OidcClientCredentials struct { //nolint:recvcheck
	FederatedIdentities []OidcClientFederatedIdentity `json:"federatedIdentities,omitempty"`
	ClaimRemappings     []OidcClientClaimRemapping    `json:"claimRemappings,omitempty"`
}
```

After the `OidcClientFederatedIdentity` struct definition, add:
```go
type OidcClientClaimRemapping struct {
	ClaimName   string                   `json:"claimName"`
	SourceType  ClaimRemappingSourceType `json:"sourceType"`
	SourceValue string                   `json:"sourceValue"`
}

type ClaimRemappingSourceType string

const (
	RemappingSourceUserField   ClaimRemappingSourceType = "user_field"
	RemappingSourceCustomClaim ClaimRemappingSourceType = "custom_claim"
	RemappingSourceStatic      ClaimRemappingSourceType = "static"
)
```

- [ ] **Step 3: Mark the file as resolved**

```bash
git add backend/internal/model/oidc.go
```

---

## Task 3: Resolve conflict in `backend/internal/dto/oidc_dto.go`

**Files:**
- Modify: `backend/internal/dto/oidc_dto.go`

Goal: Start from upstream's version (which has `RequiresPushedAuthorizationRequests`, `SkipConsent`, `ReplayProtection` on `OidcClientFederatedIdentityDto`). Re-add `OidcClientClaimRemappingDto` and the `ClaimRemappings` field on `OidcClientCredentialsDto`.

- [ ] **Step 1: Take upstream's version as the base**

```bash
git checkout --theirs backend/internal/dto/oidc_dto.go
```

- [ ] **Step 2: Add ClaimRemappings to OidcClientCredentialsDto**

Find the `OidcClientCredentialsDto` struct (upstream version):
```go
type OidcClientCredentialsDto struct {
	FederatedIdentities []OidcClientFederatedIdentityDto `json:"federatedIdentities,omitempty"`
}
```

Change it to:
```go
type OidcClientCredentialsDto struct {
	FederatedIdentities []OidcClientFederatedIdentityDto `json:"federatedIdentities,omitempty"`
	ClaimRemappings     []OidcClientClaimRemappingDto    `json:"claimRemappings,omitempty"`
}
```

After `OidcClientFederatedIdentityDto`, add:
```go
type OidcClientClaimRemappingDto struct {
	ClaimName   string `json:"claimName" binding:"required,min=1,max=255"`
	SourceType  string `json:"sourceType" binding:"required,oneof=user_field custom_claim static"`
	SourceValue string `json:"sourceValue" binding:"required,min=1,max=1000"`
}
```

- [ ] **Step 3: Mark the file as resolved**

```bash
git add backend/internal/dto/oidc_dto.go
```

---

## Task 4: Resolve conflict in `backend/internal/service/oidc_service.go`

**Files:**
- Modify: `backend/internal/service/oidc_service.go`

Goal: Start from upstream's lean version (~854 lines, CRUD only). Re-add: `validUserFieldSources` map, `reservedClaimsForRemapping` map, `validateClaimRemappings` function, and the `ClaimRemappings` wiring in `updateOIDCClientModelFromDto`.

- [ ] **Step 1: Take upstream's version as the base**

```bash
git checkout --theirs backend/internal/service/oidc_service.go
```

- [ ] **Step 2: Add the validation maps and function**

Find the section near the end of the file that has `updateOIDCClientModelFromDto`. Before that function, add the following block (after any existing `var` blocks or top-level declarations):

```go
var validUserFieldSources = map[string]bool{
	"email":        true,
	"first_name":   true,
	"last_name":    true,
	"display_name": true,
	"username":     true,
	"locale":       true,
}

// Reserved claims that cannot be remapped
var reservedClaimsForRemapping = map[string]bool{
	"sub":       true,
	"iss":       true,
	"aud":       true,
	"exp":       true,
	"iat":       true,
	"auth_time": true,
	"nonce":     true,
	"acr":       true,
	"amr":       true,
	"azp":       true,
	"nbf":       true,
	"jti":       true,
	"groups":    true,
}

func validateClaimRemappings(remappings []dto.OidcClientClaimRemappingDto) error {
	seenClaims := make(map[string]bool)

	for _, remapping := range remappings {
		// Check for duplicates
		if seenClaims[remapping.ClaimName] {
			return fmt.Errorf("duplicate claim remapping for '%s'", remapping.ClaimName)
		}
		seenClaims[remapping.ClaimName] = true

		// Check if claim is reserved
		if reservedClaimsForRemapping[remapping.ClaimName] {
			return fmt.Errorf("cannot remap reserved claim '%s'", remapping.ClaimName)
		}

		// Validate source based on type
		switch remapping.SourceType {
		case "user_field":
			if !validUserFieldSources[remapping.SourceValue] {
				return fmt.Errorf("invalid user field '%s' for remapping", remapping.SourceValue)
			}
		case "custom_claim":
			if len(remapping.SourceValue) == 0 || len(remapping.SourceValue) > 255 {
				return fmt.Errorf("invalid custom claim key length")
			}
		case "static":
			// Static values are always valid (string or JSON)
		default:
			return fmt.Errorf("invalid source type '%s'", remapping.SourceType)
		}
	}

	return nil
}
```

- [ ] **Step 3: Check that `fmt` is imported**

Look at the imports block. If `"fmt"` is not already imported, add it. The imports section should include:
```go
import (
	"context"
	"fmt"
	// ... other existing imports
)
```

- [ ] **Step 4: Add validateClaimRemappings calls in CreateClient and UpdateClient**

Find where `CreateClient` validates credentials (look for where `validateFederatedIdentities` or similar validation is called). After any existing credential validation in both `CreateClient` and `UpdateClient`, add:

```go
if err := validateClaimRemappings(input.Credentials.ClaimRemappings); err != nil {
    return model.OidcClient{}, err
}
```

Note: in `UpdateClient` the return type will be `(model.OidcClient, error)` — match the existing pattern.

- [ ] **Step 5: Add ClaimRemappings wiring in updateOIDCClientModelFromDto**

Find `updateOIDCClientModelFromDto`. At the end of the Credentials section (after the FederatedIdentities loop), add:

```go
// Credentials - ClaimRemappings
client.Credentials.ClaimRemappings = make([]model.OidcClientClaimRemapping, len(input.Credentials.ClaimRemappings))
for i, cr := range input.Credentials.ClaimRemappings {
    client.Credentials.ClaimRemappings[i] = model.OidcClientClaimRemapping{
        ClaimName:   cr.ClaimName,
        SourceType:  model.ClaimRemappingSourceType(cr.SourceType),
        SourceValue: cr.SourceValue,
    }
}
```

- [ ] **Step 6: Mark the file as resolved**

```bash
git add backend/internal/service/oidc_service.go
```

---

## Task 5: Port claim remapping logic into `backend/internal/oidc/claims_service.go`

**Files:**
- Modify: `backend/internal/oidc/claims_service.go`

Goal: Add `applyClaimRemappings` as a method on `ClaimsService` and extend `GetUserClaims` to accept an optional `*model.OidcClient` — when it is non-nil and has remappings, apply them after all standard claims are set. Also build and pass `customClaimsMap` so remappings with `custom_claim` source can look up values.

This file is NOT in conflict (it does not exist in the fork) — edit it directly.

- [ ] **Step 1: Add required imports**

Open `backend/internal/oidc/claims_service.go`. The current imports are:
```go
import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/ory/fosite"
	"github.com/pocket-id/pocket-id/backend/internal/common"
	"github.com/pocket-id/pocket-id/backend/internal/model"
	"gorm.io/gorm"
)
```

Add `"log/slog"` to the import list:
```go
import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"

	"github.com/ory/fosite"
	"github.com/pocket-id/pocket-id/backend/internal/common"
	"github.com/pocket-id/pocket-id/backend/internal/model"
	"gorm.io/gorm"
)
```

- [ ] **Step 2: Change the signature of GetUserClaims to accept an optional client**

Find:
```go
func (s *ClaimsService) GetUserClaims(ctx context.Context, userID string, scopes []string) (map[string]any, error) {
```

Change to:
```go
// GetUserClaims retrieves the claims for a user based on the requested scopes.
// It includes standard claims like "sub" and "email" as well as any custom claims defined for the user or their groups.
// When client is non-nil and has claim remappings configured, those remappings are applied after all standard claims are set.
func (s *ClaimsService) GetUserClaims(ctx context.Context, userID string, scopes []string, client *model.OidcClient) (map[string]any, error) {
```

- [ ] **Step 3: Build customClaimsMap inside GetUserClaims and apply remappings**

Inside `GetUserClaims`, the current code loads custom claims only inside `if slices.Contains(scopes, "profile")`. Change the function body so `customClaimsMap` is built unconditionally (needed for `custom_claim` remapping source regardless of scopes), then apply remappings at the end.

Replace the existing function body (from `db := dbFromContext(...)` to the final `return claims, nil`) with:

```go
db := dbFromContext(ctx, s.db)

var user model.User
err := db.
    Preload("UserGroups").
    First(&user, "id = ?", userID).
    Error
if err != nil {
    return nil, err
}

claims := make(map[string]any, 10)

// Load custom claims unconditionally so they are available for remapping regardless of the requested scopes.
// Guard against nil so tests that pass customClaims=nil still compile (e.g. TestClaimsServiceValidateUserAccess).
var customClaimsList []model.CustomClaim
if s.customClaims != nil {
    customClaimsList, err = s.customClaims.GetCustomClaimsForUserWithUserGroups(ctx, user.ID, db)
    if err != nil {
        return nil, err
    }
}

customClaimsMap := make(map[string]any, len(customClaimsList))
for _, customClaim := range customClaimsList {
    // A custom claim value can be a JSON document or a plain string
    var jsonValue any
    if err := json.Unmarshal([]byte(customClaim.Value), &jsonValue); err == nil {
        customClaimsMap[customClaim.Key] = jsonValue
    } else {
        customClaimsMap[customClaim.Key] = customClaim.Value
    }
}

if slices.Contains(scopes, "profile") {
    for key, value := range customClaimsMap {
        claims[key] = value
    }

    claims["given_name"] = user.FirstName
    claims["family_name"] = user.LastName
    claims["name"] = user.FullName()
    claims["display_name"] = user.DisplayName
    claims["preferred_username"] = user.Username
    claims["picture"] = s.baseURL + "/api/users/" + user.ID + "/profile-picture.png"
}

claims["sub"] = user.ID

// Only release the email claims when the user actually has an email
if slices.Contains(scopes, "email") && user.Email != nil && *user.Email != "" {
    claims["email"] = *user.Email
    claims["email_verified"] = user.EmailVerified
}

if slices.Contains(scopes, "groups") {
    userGroups := make([]string, len(user.UserGroups))
    for i, group := range user.UserGroups {
        userGroups[i] = group.Name
    }
    claims["groups"] = userGroups
}

// Apply per-client claim remappings after all standard claims are populated
if client != nil && len(client.Credentials.ClaimRemappings) > 0 {
    slog.Debug(
        "Applying claim remappings",
        "clientID", client.ID,
        "remappingCount", len(client.Credentials.ClaimRemappings),
    )
    if err := s.applyClaimRemappings(claims, client.Credentials.ClaimRemappings, &user, customClaimsMap); err != nil {
        return nil, err
    }
}

return claims, nil
```

- [ ] **Step 4: Update existing GetUserClaims call sites in claims_service_test.go**

The existing test file calls `GetUserClaims` with 3 arguments. Now it takes 4. Find all calls:
```bash
grep -n "GetUserClaims" /home/henrik/gitea-projects/pocketid/pocket-id-fork/backend/internal/oidc/claims_service_test.go
```

For each call that previously looked like:
```go
claims, err := service.GetUserClaims(t.Context(), userID, []string{"openid"})
```

Change to (passing `nil` — no client-specific remapping):
```go
claims, err := service.GetUserClaims(t.Context(), userID, []string{"openid"}, nil)
```

Apply this change to every existing `GetUserClaims` call in the test file.

- [ ] **Step 5: Add the applyClaimRemappings method**

Add after the closing brace of `GetUserClaims`:

```go
// applyClaimRemappings applies the client's configured claim remapping rules to the claims map.
// Each rule overwrites a named claim with a value drawn from a user field, a custom claim key, or a static value.
// When the source value is not found (e.g. the user has no email, or the custom claim key does not exist),
// the rule is skipped and the original claim value is preserved.
func (s *ClaimsService) applyClaimRemappings(
	claims map[string]any,
	remappings []model.OidcClientClaimRemapping,
	user *model.User,
	customClaimsMap map[string]any,
) error {
	// Snapshot original values so we can fall back when a remapping source is absent
	originalClaims := make(map[string]any, len(claims))
	for k, v := range claims {
		originalClaims[k] = v
	}

	for _, remapping := range remappings {
		var remappedValue any
		var foundValue bool

		switch remapping.SourceType {
		case model.RemappingSourceUserField:
			switch remapping.SourceValue {
			case "email":
				remappedValue = user.Email
				foundValue = user.Email != nil
			case "first_name":
				remappedValue = user.FirstName
				foundValue = true
			case "last_name":
				remappedValue = user.LastName
				foundValue = true
			case "display_name":
				remappedValue = user.DisplayName
				foundValue = true
			case "username":
				remappedValue = user.Username
				foundValue = true
			case "locale":
				remappedValue = user.Locale
				foundValue = user.Locale != nil
			}

		case model.RemappingSourceCustomClaim:
			if value, exists := customClaimsMap[remapping.SourceValue]; exists {
				remappedValue = value
				foundValue = true
			}

		case model.RemappingSourceStatic:
			// Try to parse as JSON; fall back to raw string
			var jsonValue any
			if err := json.Unmarshal([]byte(remapping.SourceValue), &jsonValue); err == nil {
				remappedValue = jsonValue
			} else {
				remappedValue = remapping.SourceValue
			}
			foundValue = true
		}

		if foundValue {
			slog.Debug(
				"Applying claim remapping",
				"claim", remapping.ClaimName,
				"oldValue", originalClaims[remapping.ClaimName],
				"newValue", remappedValue,
			)
			claims[remapping.ClaimName] = remappedValue
		} else {
			slog.Debug(
				"Claim remapping source not found, using fallback",
				"claim", remapping.ClaimName,
				"sourceType", remapping.SourceType,
				"sourceValue", remapping.SourceValue,
			)
			// Preserve the original value when the remapping source does not exist
			if originalValue, exists := originalClaims[remapping.ClaimName]; exists {
				claims[remapping.ClaimName] = originalValue
			}
		}
	}

	return nil
}
```

- [ ] **Step 6: Fix all internal callers of GetUserClaims within the oidc package**

Search for all calls to `GetUserClaims` inside `backend/internal/oidc/`:
```bash
grep -rn "GetUserClaims\|\.GetUserClaims" backend/internal/oidc/
```

For each call that does NOT pass a client (preview builder, token handler, userinfo handler, device service), add `nil` as the fourth argument:
```go
// Before:
claims, err := s.claimsService.GetUserClaims(ctx, userID, scopes)

// After:
claims, err := s.claimsService.GetUserClaims(ctx, userID, scopes, nil)
```

For the preview builder (`preview.go`), it calls `GetUserClaims` in `BuildClientPreview` which has access to the `client model.OidcClient`. Pass the client:
```go
// In BuildClientPreview, change:
userInfo, err := b.claimsService.GetUserClaims(ctx, userID, scopeArgs)

// To:
userInfo, err := b.claimsService.GetUserClaims(ctx, userID, scopeArgs, &client)
```

---

## Task 6: Resolve conflict in `frontend/src/lib/types/oidc.type.ts`

**Files:**
- Modify: `frontend/src/lib/types/oidc.type.ts`

Goal: Start from upstream's version (which adds `requiresPushedAuthorizationRequests`, `skipConsent`, `pkceSupported`, and `replayProtection` on `OidcClientFederatedIdentity`). Re-add `ClaimRemappingSourceType`, `OidcClientClaimRemapping`, and `claimRemappings` on `OidcClientCredentials`.

- [ ] **Step 1: Take upstream's version as the base**

```bash
git checkout --theirs frontend/src/lib/types/oidc.type.ts
```

- [ ] **Step 2: Add the custom types**

After `OidcClientFederatedIdentity`, add:
```typescript
export type ClaimRemappingSourceType = 'user_field' | 'custom_claim' | 'static';

export type OidcClientClaimRemapping = {
	claimName: string;
	sourceType: ClaimRemappingSourceType;
	sourceValue: string;
};
```

Find `OidcClientCredentials` (upstream version):
```typescript
export type OidcClientCredentials = {
	federatedIdentities: OidcClientFederatedIdentity[];
};
```

Change to:
```typescript
export type OidcClientCredentials = {
	federatedIdentities: OidcClientFederatedIdentity[];
	claimRemappings?: OidcClientClaimRemapping[];
};
```

- [ ] **Step 3: Mark the file as resolved**

```bash
git add frontend/src/lib/types/oidc.type.ts
```

---

## Task 7: Resolve conflict in `frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte`

**Files:**
- Modify: `frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte`

Goal: Start from upstream's version. Re-apply the `ClaimRemappingsInput` import and usage.

- [ ] **Step 1: Take upstream's version as the base**

```bash
git checkout --theirs frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte
```

- [ ] **Step 2: Add the import**

Find the `<script lang="ts">` import block. Near the other local component imports (e.g. `FederatedIdentitiesInput`), add:
```typescript
import ClaimRemappingsInput from './claim-remappings-input.svelte';
```

- [ ] **Step 3: Add claimRemappings to the form initial state**

Find where `createForm(schema, { ... })` or the form's initial state object is defined. Find the `credentials` field. It should look like:
```typescript
credentials: {
    federatedIdentities: existingClient?.credentials?.federatedIdentities || []
}
```

Change to:
```typescript
credentials: {
    federatedIdentities: existingClient?.credentials?.federatedIdentities || [],
    claimRemappings: existingClient?.credentials?.claimRemappings || []
}
```

- [ ] **Step 4: Add claimRemappings to the Zod schema**

Find the Zod schema for `credentials`. It validates `federatedIdentities` with `z.array(...)`. Add alongside it:
```typescript
claimRemappings: z
    .array(
        z.object({
            claimName: z.string().min(1).max(255),
            sourceType: z.enum(['user_field', 'custom_claim', 'static']),
            sourceValue: z.string().min(1).max(1000)
        })
    )
    .optional()
    .default([])
```

- [ ] **Step 5: Add the helper function for claim remapping errors**

Before the closing `</script>`, or near other helper functions, add:
```typescript
function getClaimRemappingErrors(errors: z.ZodError<any> | undefined) {
    return errors?.issues
        .filter((e) => e.path[0] == 'credentials' && e.path[1] == 'claimRemappings')
        .map((e) => ({ ...e, path: e.path.slice(2) }));
}
```

- [ ] **Step 6: Add the ClaimRemappingsInput component to the template**

Find where `FederatedIdentitiesInput` is used in the template. After it (or in a logical location in the form body), add:
```svelte
<ClaimRemappingsInput
    {client}
    bind:claimRemappings={$inputs.credentials.value.claimRemappings}
    errors={getClaimRemappingErrors($errors)}
/>
```

- [ ] **Step 7: Mark the file as resolved**

```bash
git add frontend/src/routes/settings/admin/oidc-clients/oidc-client-form.svelte
```

---

## Task 8: Resolve any remaining merge conflicts

**Files:**
- Any additional conflicted files reported by `git status` in Task 1 Step 3

- [ ] **Step 1: Check for remaining conflicts**

```bash
git status | grep "both modified"
```

- [ ] **Step 2: For each additional conflicted file**

For files that the fork did not customize (e.g. CI config, translations, CHANGELOG), take the upstream version:
```bash
git checkout --theirs <path/to/file>
git add <path/to/file>
```

For any file that both sides modified with custom logic, inspect the conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`) and merge manually, keeping both sides' changes.

- [ ] **Step 3: Verify no conflict markers remain**

```bash
grep -r "<<<<<<\|=======\|>>>>>>>" backend/ frontend/ tests/ --include="*.go" --include="*.ts" --include="*.svelte"
```
Expected: no output.

---

## Task 9: Write unit tests for applyClaimRemappings

**Files:**
- Modify: `backend/internal/oidc/claims_service_test.go`

- [ ] **Step 1: Add the test function**

Open `backend/internal/oidc/claims_service_test.go`. Add after the existing `TestClaimsServiceGetUserClaims` function:

```go
// TestClaimsServiceApplyClaimRemappings verifies the per-client claim remapping logic:
// each source type maps correctly, absent sources fall back to the original claim value,
// and static JSON is decoded while plain strings are kept as strings.
func TestClaimsServiceApplyClaimRemappings(t *testing.T) {
	service := &ClaimsService{}

	email := "work@example.com"
	user := &model.User{
		Base:        model.Base{ID: "u1"},
		FirstName:   "Ada",
		LastName:    "Lovelace",
		DisplayName: "Ada L.",
		Username:    "ada",
		Email:       &email,
	}

	customClaimsMap := map[string]any{
		"department": "engineering",
		"roles":      []any{"admin", "dev"},
	}

	t.Run("user_field source maps the claim", func(t *testing.T) {
		claims := map[string]any{"email": "personal@example.com"}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "email", SourceType: model.RemappingSourceUserField, SourceValue: "first_name"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, "Ada", claims["email"])
	})

	t.Run("user_field email falls back when user has no email", func(t *testing.T) {
		userNoEmail := &model.User{Base: model.Base{ID: "u2"}, Username: "nomail"}
		claims := map[string]any{"email": "original@example.com"}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "email", SourceType: model.RemappingSourceUserField, SourceValue: "email"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, userNoEmail, customClaimsMap))
		// email field is nil so original value is preserved
		require.Equal(t, "original@example.com", claims["email"])
	})

	t.Run("custom_claim source maps the claim", func(t *testing.T) {
		claims := map[string]any{"email": "original@example.com"}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "email", SourceType: model.RemappingSourceCustomClaim, SourceValue: "department"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, "engineering", claims["email"])
	})

	t.Run("custom_claim source falls back when key not found", func(t *testing.T) {
		claims := map[string]any{"email": "original@example.com"}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "email", SourceType: model.RemappingSourceCustomClaim, SourceValue: "nonexistent"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, "original@example.com", claims["email"])
	})

	t.Run("static source sets a plain string", func(t *testing.T) {
		claims := map[string]any{}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "tenant", SourceType: model.RemappingSourceStatic, SourceValue: "acme-corp"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, "acme-corp", claims["tenant"])
	})

	t.Run("static source decodes JSON", func(t *testing.T) {
		claims := map[string]any{}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "tags", SourceType: model.RemappingSourceStatic, SourceValue: `["a","b"]`},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, []any{"a", "b"}, claims["tags"])
	})

	t.Run("no remappings leaves claims unchanged", func(t *testing.T) {
		claims := map[string]any{"email": "x@example.com", "sub": "u1"}
		require.NoError(t, service.applyClaimRemappings(claims, nil, user, customClaimsMap))
		require.Equal(t, "x@example.com", claims["email"])
		require.Equal(t, "u1", claims["sub"])
	})

	t.Run("multiple remappings are all applied", func(t *testing.T) {
		claims := map[string]any{"email": "old@example.com", "name": "Old Name"}
		remappings := []model.OidcClientClaimRemapping{
			{ClaimName: "email", SourceType: model.RemappingSourceUserField, SourceValue: "email"},
			{ClaimName: "name", SourceType: model.RemappingSourceStatic, SourceValue: "Company User"},
		}
		require.NoError(t, service.applyClaimRemappings(claims, remappings, user, customClaimsMap))
		require.Equal(t, "work@example.com", claims["email"])
		require.Equal(t, "Company User", claims["name"])
	})
}
```

- [ ] **Step 2: Run the new tests to confirm they compile and pass**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork/backend
go test -tags=exclude_frontend -run TestClaimsServiceApplyClaimRemappings ./internal/oidc/ -v
```
Expected: all 7 sub-tests pass.

---

## Task 10: Commit the merge

**Files:**
- No additional edits

- [ ] **Step 1: Verify all files are staged**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork
git status
```
Expected: all modified files are in "Changes to be committed". No "Changes not staged" or untracked conflicts.

- [ ] **Step 2: Commit**

```bash
git commit -m "chore: merge upstream main into fork (claims remapping port)"
```

---

## Task 11: Run backend unit tests and fix any failures

**Files:**
- Any files that fail compilation or tests

- [ ] **Step 1: Run the full backend test suite**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork/backend
go test -tags=exclude_frontend ./... 2>&1
```
Expected: all tests pass. Note any failures.

- [ ] **Step 2: If any tests fail, fix the root cause**

For each failing test:
1. Read the error message carefully — it will point to a specific file and line.
2. Open the file and fix the issue (usually a type mismatch, missing import, or wrong function signature).
3. Re-run `go test -tags=exclude_frontend ./...` until all pass.

- [ ] **Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix: post-merge compilation and test fixes"
```

---

## Task 12: Run frontend check and fix any failures

**Files:**
- Any frontend files with type errors

- [ ] **Step 1: Run svelte-check**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork
pnpm check
```
Expected: 0 errors. Note any type errors.

- [ ] **Step 2: Fix any type errors**

Type errors are usually caused by:
- Missing or renamed properties on types (check `oidc.type.ts`)
- Importing a component with the wrong props (check `oidc-client-form.svelte`)
- Zod v4 API differences (use `z.enum([...])` not `z.nativeEnum`, use `z.object` not `z.ZodObject`)

Fix each error and re-run `pnpm check` until clean.

- [ ] **Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix: post-merge frontend type fixes"
```

---

## Task 13: Build the Docker image and run e2e tests

**Files:**
- No file edits — test execution only

- [ ] **Step 1: Stop any running pocket-id process on port 1411**

```bash
ss -tlnp | grep 1411
```
If something is running, stop it before proceeding.

- [ ] **Step 2: Rebuild the Docker image and start the test stack**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork/tests/setup
docker compose up -d --build
```
Expected: build completes, containers start. Wait ~10 seconds for the app to be ready.

- [ ] **Step 3: Run the Playwright test suite**

```bash
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork
pnpm test
```
Expected: all tests pass. Note any failures.

- [ ] **Step 4: If any tests fail, investigate and fix**

```bash
# View failure output from the HTML report:
cd /home/henrik/gitea-projects/pocketid/pocket-id-fork/tests
# The report is at tests/.report/index.html
# Read the failing test spec to understand what it expects, then fix the relevant code
```

For each failing test:
1. Identify the spec file from the failure output (e.g. `tests/specs/oidc.spec.ts`).
2. Read the failing test block.
3. If the failure is due to a changed API response shape (e.g. new fields, changed route), update the backend or test accordingly.
4. Rebuild the Docker image: `cd tests/setup && docker compose up -d --build`
5. Re-run `pnpm test` until all tests pass.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix: post-merge e2e test fixes"
```
