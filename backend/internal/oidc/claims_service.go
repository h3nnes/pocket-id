package oidc

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

const (
	idTokenType = "id-token"
)

type ClaimsService struct {
	db           *gorm.DB
	customClaims CustomClaimSource
	baseURL      string
	signer       TokenSigner
}

func newClaimsService(db *gorm.DB, customClaims CustomClaimSource, baseURL string, signer TokenSigner) *ClaimsService {
	return &ClaimsService{
		db:           db,
		customClaims: customClaims,
		baseURL:      baseURL,
		signer:       signer,
	}
}

// ValidateUserAccess re-checks, at token-issuance time, that the user behind a grant is
// still allowed to obtain tokens for the client.
func (s *ClaimsService) ValidateUserAccess(ctx context.Context, userID string, client Client) error {
	// Grants without a resource owner (e.g. client_credentials) carry an empty subject
	// and have no user to validate.
	if userID == "" {
		return nil
	}

	var user model.User
	err := dbFromContext(ctx, s.db).
		Preload("UserGroups").
		First(&user, "id = ?", userID).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fosite.ErrInvalidGrant.WithHint("The user account no longer exists.")
	}
	if err != nil {
		return err
	}

	if user.Disabled {
		return fosite.ErrInvalidGrant.WithHint("The user account is disabled.")
	}

	if !IsUserGroupAllowedToAuthorize(user, client.OidcClient) {
		return fosite.ErrAccessDenied.WithHint("You are not allowed to access this service.")
	}

	return nil
}

// applyIDTokenClaims applies the claims of a user to the ID token claims in the session based on the requested scopes.
// The client is optional; when supplied it enables per-client claim remappings configured by an admin.
func (s *ClaimsService) applyIDTokenClaims(ctx context.Context, session *Session, scopes fosite.Arguments, client *model.OidcClient) error {
	userID := session.Subject
	if userID == "" {
		return nil
	}

	claims, err := s.GetUserClaimsForClient(ctx, userID, scopes, client)
	if err != nil {
		return err
	}

	// Record the signing algorithm on the ID token header so fosite derives the at_hash/
	// c_hash digest from it (e.g. RS384 -> SHA-384, ES512 -> SHA-512). Without this the
	// header is empty and fosite defaults to SHA-256, producing wrong hashes whenever the
	// signing key is not a 256-bit algorithm. ToMap() strips "alg" before signing, so this
	// never overrides the real JWS header. The signer is always wired in production; it is
	// only nil in unit tests that do not assert hash correctness.
	if s.signer != nil {
		alg, err := s.signer.GetKeyAlg()
		if err != nil {
			return err
		}
		session.IDTokenHeaders().Add("alg", alg.String())
	}

	applyUserClaimsToIDToken(session, userID, claims)
	return nil
}

func applyUserClaimsToIDToken(session *Session, userID string, claims map[string]any) {
	idTokenClaims := session.IDTokenClaims()
	idTokenClaims.Subject = userID
	idTokenClaims.Extra = claims
	idTokenClaims.Extra[common.TokenTypeClaim] = idTokenType
	if session.AuthenticationMethod != "" {
		idTokenClaims.AuthenticationMethodsReferences = []string{session.AuthenticationMethod}
	}
}

// GetUserClaims retrieves the claims for a user based on the requested scopes. It includes standard claims
// like "sub" and "email" as well as any custom claims defined for the user or their groups.
func (s *ClaimsService) GetUserClaims(ctx context.Context, userID string, scopes []string) (map[string]any, error) {
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

	if slices.Contains(scopes, "profile") {
		customClaims, err := s.customClaims.GetCustomClaimsForUserWithUserGroups(ctx, user.ID, db)
		if err != nil {
			return nil, err
		}

		for _, customClaim := range customClaims {
			// A custom claim value can be a JSON document or a plain string
			var jsonValue any
			if err := json.Unmarshal([]byte(customClaim.Value), &jsonValue); err == nil {
				claims[customClaim.Key] = jsonValue
			} else {
				claims[customClaim.Key] = customClaim.Value
			}
		}

		claims["given_name"] = user.FirstName
		claims["family_name"] = user.LastName
		claims["name"] = user.FullName()
		claims["display_name"] = user.DisplayName
		claims["preferred_username"] = user.Username
		claims["picture"] = s.baseURL + "/api/users/" + user.ID + "/profile-picture.png"
	}

	claims["sub"] = user.ID

	// Only release the email claims when the user actually has an email. Emitting
	// email_verified alongside a null/absent email (OIDC Core §5.1) is malformed and can
	// mislead relying parties that key trust decisions on email_verified.
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

	return claims, nil
}

// GetUserClaimsForClient returns the standard claims for a user and then applies any per-client remappings configured on the client.
// A nil client is a no-op wrapper around GetUserClaims and preserves existing behavior for callers that have no client in hand.
func (s *ClaimsService) GetUserClaimsForClient(ctx context.Context, userID string, scopes []string, client *model.OidcClient) (map[string]any, error) {
	// Fetch the standard claims first so remapping is a strict post-processing step
	claims, err := s.GetUserClaims(ctx, userID, scopes)
	if err != nil {
		return nil, err
	}

	// Skip remapping when the caller has no client context or the client has no remappings configured
	if client == nil || len(client.Credentials.ClaimRemappings) == 0 {
		return claims, nil
	}

	// Only touch the database for source types that actually require it
	// A remapping set consisting entirely of static values needs neither the user record nor the custom-claim map
	needsUser := false
	needsCustomClaims := false
	for _, r := range client.Credentials.ClaimRemappings {
		switch r.SourceType {
		case model.RemappingSourceUserField:
			needsUser = true
		case model.RemappingSourceCustomClaim:
			needsCustomClaims = true
		}
	}

	db := dbFromContext(ctx, s.db)

	// Load the user record only when a user_field remapping actually needs it
	// The reload is intentional so remapping still works when GetUserClaims took the fast path without a load
	var user model.User
	if needsUser {
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			return nil, err
		}
	}

	// Load custom claims only when a custom_claim remapping actually needs them
	// The map is populated once so per-entry lookups stay O(1)
	var customClaimsMap map[string]any
	if needsCustomClaims && s.customClaims != nil {
		customClaims, err := s.customClaims.GetCustomClaimsForUserWithUserGroups(ctx, userID, db)
		if err != nil {
			return nil, err
		}
		customClaimsMap = make(map[string]any, len(customClaims))
		for _, cc := range customClaims {
			var jsonValue any
			if err := json.Unmarshal([]byte(cc.Value), &jsonValue); err == nil {
				customClaimsMap[cc.Key] = jsonValue
			} else {
				customClaimsMap[cc.Key] = cc.Value
			}
		}
	}

	applyClaimRemappings(claims, client.Credentials.ClaimRemappings, &user, customClaimsMap)
	return claims, nil
}

// remappingReservedClaims is a defense-in-depth guard applied at token-issuance time
// A legacy row that somehow bypassed validation (hand-edited SQL, an older buggy build, an offline migration) must never override claims that carry token semantics
var remappingReservedClaims = map[string]bool{
	"sub": true, "iss": true, "aud": true, "exp": true, "iat": true,
	"auth_time": true, "nonce": true, "acr": true, "amr": true, "azp": true,
	"nbf": true, "jti": true, "sid": true,
	"at_hash": true, "c_hash": true, "s_hash": true,
	"typ": true, "client_id": true, "cnf": true, "act": true,
}

// applyClaimRemappings overrides or adds claims in place based on the client's admin-configured remappings.
// Remappings whose source cannot be resolved leave the existing claim untouched; this preserves the original value as a safe fallback.
func applyClaimRemappings(
	claims map[string]any,
	remappings []model.OidcClientClaimRemapping,
	user *model.User,
	customClaimsMap map[string]any,
) {
	for _, remapping := range remappings {
		// Skip reserved claims defensively even if a legacy row somehow slipped past validation
		if remappingReservedClaims[remapping.ClaimName] {
			continue
		}

		var remappedValue any
		var foundValue bool

		switch remapping.SourceType {
		case model.RemappingSourceUserField:
			// User-field lookup is a fixed switch so the allowlist is enforced structurally
			switch remapping.SourceValue {
			case "email":
				if user.Email != nil && *user.Email != "" {
					remappedValue = *user.Email
					foundValue = true
				}
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
				if user.Locale != nil && *user.Locale != "" {
					remappedValue = *user.Locale
					foundValue = true
				}
			}

		case model.RemappingSourceCustomClaim:
			// Custom-claim lookup returns whatever type was stored (string, number, array, object)
			if v, ok := customClaimsMap[remapping.SourceValue]; ok {
				remappedValue = v
				foundValue = true
			}

		case model.RemappingSourceStatic:
			// Static values are first tried as JSON so arrays and booleans work; a parse failure falls back to the literal string
			var jsonValue any
			if err := json.Unmarshal([]byte(remapping.SourceValue), &jsonValue); err == nil {
				remappedValue = jsonValue
			} else {
				remappedValue = remapping.SourceValue
			}
			foundValue = true
		}

		// Apply the remapped value only if the source resolved; otherwise the original claim is preserved
		if foundValue {
			claims[remapping.ClaimName] = remappedValue
		}
	}
}
