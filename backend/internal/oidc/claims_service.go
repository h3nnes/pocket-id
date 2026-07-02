package oidc

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
func (s *ClaimsService) applyIDTokenClaims(ctx context.Context, session *Session, scopes fosite.Arguments) error {
	userID := session.Subject
	if userID == "" {
		return nil
	}

	claims, err := s.GetUserClaims(ctx, userID, scopes, nil)
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

// GetUserClaims retrieves the claims for a user based on the requested scopes.
// It includes standard claims like "sub" and "email" as well as any custom claims defined for the user or their groups.
// When client is non-nil and has claim remappings configured, those remappings are applied after all standard claims are set.
func (s *ClaimsService) GetUserClaims(ctx context.Context, userID string, scopes []string, client *model.OidcClient) (map[string]any, error) {
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
		var loadErr error
		customClaimsList, loadErr = s.customClaims.GetCustomClaimsForUserWithUserGroups(ctx, user.ID, db)
		if loadErr != nil {
			return nil, loadErr
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

	// Only release the email claims when the user actually has an email.
	// Emitting email_verified alongside a null/absent email is malformed and can mislead relying parties.
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
}

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
				foundValue = user.Email != nil
				if foundValue {
					remappedValue = *user.Email
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
				foundValue = user.Locale != nil
				if foundValue {
					remappedValue = *user.Locale
				}
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
