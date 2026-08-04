package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pocket-id/pocket-id/backend/internal/dto"
	"github.com/pocket-id/pocket-id/backend/internal/model"
)

// validUserFieldSources is the allowlist of user-profile fields that a remapping may source from
// The allowlist matches fields that already appear in default OIDC claims so admins never expose data that the user model would otherwise keep private
var validUserFieldSources = map[string]bool{
	"email":        true,
	"first_name":   true,
	"last_name":    true,
	"display_name": true,
	"username":     true,
	"locale":       true,
}

// reservedClaimsForRemapping lists claim names that carry token semantics and must never be remapped
// Overriding any of these would break token verification, subject binding, hash-based binding assertions, or audience correlation
// The set is intentionally broader than the OIDC core standard so future access-token or token-exchange work does not silently trust admin-supplied values for RFC 8725, RFC 9068, RFC 7800 or RFC 8693 claims
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
	"sid":       true,
	"at_hash":   true,
	"c_hash":    true,
	"s_hash":    true,
	"typ":       true,
	"client_id": true,
	"cnf":       true,
	"act":       true,
}

// customClaimKeyPattern matches conservative claim-key syntax used across OIDC deployments
// Restricting the pattern prevents header-injection-style keys and stops path-like values from being used as claim keys
var customClaimKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\-]{0,254}$`)

// maxStaticValueBytes bounds a static remapping value so a single malformed entry cannot inflate token size unboundedly
const maxStaticValueBytes = 1000

// validateClaimRemappings enforces semantic rules on top of the DTO struct-tag validation
// It runs before any persistence write so invalid input never touches the database
func validateClaimRemappings(remappings []dto.OidcClientClaimRemappingDto) error {
	// Track normalized claim names to reject duplicate mappings
	seenClaims := make(map[string]struct{}, len(remappings))

	for i, remapping := range remappings {
		// Normalize the claim name before every downstream check so validation matches persisted form
		claimName := strings.TrimSpace(remapping.ClaimName)
		if claimName == "" {
			return fmt.Errorf("claim remapping #%d has an empty claim name", i+1)
		}

		// Reject claim names that carry token semantics; letting them through would break token verification
		if reservedClaimsForRemapping[claimName] {
			return fmt.Errorf("cannot remap reserved claim %q", claimName)
		}

		// Reject claim names that would collide with an earlier entry in the same submission
		if _, ok := seenClaims[claimName]; ok {
			return fmt.Errorf("duplicate claim remapping for %q", claimName)
		}
		seenClaims[claimName] = struct{}{}

		// Enforce the same conservative key syntax on the claim name itself so admins cannot craft odd JWT keys
		if !customClaimKeyPattern.MatchString(claimName) {
			return fmt.Errorf("claim name %q contains characters that are not allowed", claimName)
		}

		// The source value is trimmed prior to type-specific checks to match the persisted form
		sourceValue := strings.TrimSpace(remapping.SourceValue)
		if sourceValue == "" {
			return fmt.Errorf("claim remapping for %q has an empty source value", claimName)
		}

		// Dispatch to per-type validation so each source type can enforce its own rules
		switch model.ClaimRemappingSourceType(remapping.SourceType) {
		case model.RemappingSourceUserField:
			// User-field sources must be on the fixed allowlist
			if !validUserFieldSources[sourceValue] {
				return fmt.Errorf("invalid user field %q for remapping of claim %q", sourceValue, claimName)
			}

		case model.RemappingSourceCustomClaim:
			// Custom-claim sources must match the conservative key syntax used elsewhere for custom claim keys
			if !customClaimKeyPattern.MatchString(sourceValue) {
				return fmt.Errorf("invalid custom claim key %q for remapping of claim %q", sourceValue, claimName)
			}

		case model.RemappingSourceStatic:
			// Static values are size-capped to prevent a single entry from ballooning token size
			if len(sourceValue) > maxStaticValueBytes {
				return fmt.Errorf("static value for claim %q exceeds %d bytes", claimName, maxStaticValueBytes)
			}

		default:
			return fmt.Errorf("invalid source type %q for claim %q", remapping.SourceType, claimName)
		}
	}

	return nil
}
