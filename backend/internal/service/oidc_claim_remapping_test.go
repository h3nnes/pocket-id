package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pocket-id/pocket-id/backend/internal/dto"
)

func TestValidateClaimRemappings(t *testing.T) {
	// A nil or empty slice must be accepted so clients without remappings continue to work
	t.Run("empty slice is valid", func(t *testing.T) {
		require.NoError(t, validateClaimRemappings(nil))
		require.NoError(t, validateClaimRemappings([]dto.OidcClientClaimRemappingDto{}))
	})

	t.Run("user_field with allowlisted source is valid", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "given_name", SourceType: "user_field", SourceValue: "first_name"},
		})
		require.NoError(t, err)
	})

	t.Run("custom_claim source with syntactically valid key is valid", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "email", SourceType: "custom_claim", SourceValue: "work_email"},
		})
		require.NoError(t, err)
	})

	t.Run("static source with plain string is valid", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "department", SourceType: "static", SourceValue: "engineering"},
		})
		require.NoError(t, err)
	})

	t.Run("static source with JSON array is valid", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "roles", SourceType: "static", SourceValue: `["a","b"]`},
		})
		require.NoError(t, err)
	})

	t.Run("reserved claim is rejected", func(t *testing.T) {
		// Iterate the full reserved set so any future additions are automatically covered
		for name := range reservedClaimsForRemapping {
			err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
				{ClaimName: name, SourceType: "static", SourceValue: "x"},
			})
			assert.Error(t, err, "expected reserved claim %q to be rejected", name)
		}
	})

	t.Run("duplicate claim name is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "email", SourceType: "static", SourceValue: "a"},
			{ClaimName: "email", SourceType: "static", SourceValue: "b"},
		})
		require.Error(t, err)
	})

	t.Run("whitespace-only claim name is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "   ", SourceType: "static", SourceValue: "x"},
		})
		require.Error(t, err)
	})

	t.Run("claim name with invalid characters is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "bad name!", SourceType: "static", SourceValue: "x"},
		})
		require.Error(t, err)
	})

	t.Run("user_field with non-allowlisted source is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "sensitive", SourceType: "user_field", SourceValue: "password_hash"},
		})
		require.Error(t, err)
	})

	t.Run("custom_claim with invalid key syntax is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "email", SourceType: "custom_claim", SourceValue: "not a valid key"},
		})
		require.Error(t, err)
	})

	t.Run("static value over size cap is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "big", SourceType: "static", SourceValue: strings.Repeat("x", maxStaticValueBytes+1)},
		})
		require.Error(t, err)
	})

	t.Run("unknown source type is rejected", func(t *testing.T) {
		err := validateClaimRemappings([]dto.OidcClientClaimRemappingDto{
			{ClaimName: "x", SourceType: "not_a_type", SourceValue: "y"},
		})
		require.Error(t, err)
	})
}
