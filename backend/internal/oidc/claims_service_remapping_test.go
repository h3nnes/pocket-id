package oidc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pocket-id/pocket-id/backend/internal/model"
	testutils "github.com/pocket-id/pocket-id/backend/internal/utils/testing"
)

// TestClaimsServiceGetUserClaimsForClientNoRemappings verifies GetUserClaimsForClient is a transparent wrapper when the client is nil or has no remappings configured
func TestClaimsServiceGetUserClaimsForClientNoRemappings(t *testing.T) {
	db := testutils.NewDatabaseForTest(t)
	const userID = "user-noop"

	customClaims := fakeCustomClaimSource{}
	service := newClaimsService(db, customClaims, "https://id.example.com", nil)

	require.NoError(t, db.Create(&model.User{
		Base:     model.Base{ID: userID},
		Username: "noop",
		Email:    stringPointer("noop@example.com"),
	}).Error)

	t.Run("nil client is a no-op wrapper", func(t *testing.T) {
		claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid"}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"sub": userID}, claims)
	})

	t.Run("empty remappings is a no-op wrapper", func(t *testing.T) {
		client := &model.OidcClient{Base: model.Base{ID: "c1"}, Name: "C"}
		claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid"}, client)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"sub": userID}, claims)
	})
}

// TestClaimsServiceGetUserClaimsForClientRemapsFromSources exercises all three source types and confirms remapping applies even when the source scope is not requested
func TestClaimsServiceGetUserClaimsForClientRemapsFromSources(t *testing.T) {
	db := testutils.NewDatabaseForTest(t)
	const userID = "user-remap"

	customClaims := fakeCustomClaimSource{claims: []model.CustomClaim{
		{Key: "work_email", Value: "tim@work.example"},
		{Key: "flags", Value: `["a","b"]`},
	}}
	service := newClaimsService(db, customClaims, "https://id.example.com", nil)

	require.NoError(t, db.Create(&model.User{
		Base:        model.Base{ID: userID},
		Username:    "tim",
		FirstName:   "Tim",
		LastName:    "Cook",
		DisplayName: "Tim Cook",
		Email:       stringPointer("tim@example.com"),
	}).Error)

	client := &model.OidcClient{
		Base: model.Base{ID: "c-remap"},
		Name: "Remap",
		Credentials: model.OidcClientCredentials{
			ClaimRemappings: []model.OidcClientClaimRemapping{
				// Remap email to a custom claim; shared-mailbox use case
				{ClaimName: "email", SourceType: model.RemappingSourceCustomClaim, SourceValue: "work_email"},
				// Static JSON array is parsed and emitted as an array in the token
				{ClaimName: "roles", SourceType: model.RemappingSourceStatic, SourceValue: `["admin"]`},
				// Static non-JSON literal is emitted as a string
				{ClaimName: "department", SourceType: model.RemappingSourceStatic, SourceValue: "engineering"},
				// User-field mapping onto a fresh claim name
				{ClaimName: "given_name_copy", SourceType: model.RemappingSourceUserField, SourceValue: "first_name"},
			},
		},
	}

	t.Run("remaps standard claim even when only email scope is requested", func(t *testing.T) {
		claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid", "email"}, client)
		require.NoError(t, err)
		assert.Equal(t, "tim@work.example", claims["email"])
		assert.Equal(t, []any{"admin"}, claims["roles"])
		assert.Equal(t, "engineering", claims["department"])
		assert.Equal(t, "Tim", claims["given_name_copy"])
	})

	t.Run("remaps also work under profile scope", func(t *testing.T) {
		claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"profile"}, client)
		require.NoError(t, err)
		assert.Equal(t, "engineering", claims["department"])
		assert.Equal(t, "Tim", claims["given_name_copy"])
	})
}

// TestClaimsServiceRemappingFallsBackWhenSourceMissing verifies that a remapping whose source cannot be resolved preserves the original claim value instead of clobbering it with nil
func TestClaimsServiceRemappingFallsBackWhenSourceMissing(t *testing.T) {
	db := testutils.NewDatabaseForTest(t)
	const userID = "user-fallback"

	customClaims := fakeCustomClaimSource{}
	service := newClaimsService(db, customClaims, "https://id.example.com", nil)

	require.NoError(t, db.Create(&model.User{
		Base:     model.Base{ID: userID},
		Username: "user",
		Email:    stringPointer("user@example.com"),
	}).Error)

	client := &model.OidcClient{
		Base: model.Base{ID: "c-fallback"},
		Credentials: model.OidcClientCredentials{
			ClaimRemappings: []model.OidcClientClaimRemapping{
				// Points at a custom claim key that does not exist for this user
				{ClaimName: "email", SourceType: model.RemappingSourceCustomClaim, SourceValue: "missing_key"},
			},
		},
	}

	claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid", "email"}, client)
	require.NoError(t, err)
	// Original email is preserved when the remapping source cannot resolve
	assert.Equal(t, "user@example.com", claims["email"])
}

// TestClaimsServiceRemappingCustomClaimWithoutProfileScope verifies custom-claim remappings work when only openid scope is requested
// This exercises the code path where GetUserClaims took its fast path and the wrapper must independently load custom claims
func TestClaimsServiceRemappingCustomClaimWithoutProfileScope(t *testing.T) {
	db := testutils.NewDatabaseForTest(t)
	const userID = "user-ccw"

	customClaims := fakeCustomClaimSource{claims: []model.CustomClaim{
		{Key: "department", Value: "sales"},
	}}
	service := newClaimsService(db, customClaims, "https://id.example.com", nil)

	require.NoError(t, db.Create(&model.User{
		Base:     model.Base{ID: userID},
		Username: "u",
	}).Error)

	client := &model.OidcClient{
		Base: model.Base{ID: "c-ccw"},
		Credentials: model.OidcClientCredentials{
			ClaimRemappings: []model.OidcClientClaimRemapping{
				{ClaimName: "dept", SourceType: model.RemappingSourceCustomClaim, SourceValue: "department"},
			},
		},
	}

	claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid"}, client)
	require.NoError(t, err)
	assert.Equal(t, "sales", claims["dept"])
}

// TestClaimsServiceRemappingUserFieldNilSource verifies that mapping from a nil user_field source preserves the original claim
func TestClaimsServiceRemappingUserFieldNilSource(t *testing.T) {
	db := testutils.NewDatabaseForTest(t)
	const userID = "user-nil"

	service := newClaimsService(db, fakeCustomClaimSource{}, "https://id.example.com", nil)

	// User has no email and no locale
	require.NoError(t, db.Create(&model.User{
		Base:     model.Base{ID: userID},
		Username: "u",
	}).Error)

	client := &model.OidcClient{
		Base: model.Base{ID: "c-nil"},
		Credentials: model.OidcClientCredentials{
			ClaimRemappings: []model.OidcClientClaimRemapping{
				{ClaimName: "work_email", SourceType: model.RemappingSourceUserField, SourceValue: "email"},
			},
		},
	}

	claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid"}, client)
	require.NoError(t, err)
	// The claim must not appear at all since the source resolved to nil and no prior value existed
	_, present := claims["work_email"]
	assert.False(t, present)
}

// TestClaimsServiceRemappingStaticJSONVariants verifies static values decode into the intended JSON types
func TestClaimsServiceRemappingStaticJSONVariants(t *testing.T) {	db := testutils.NewDatabaseForTest(t)
	const userID = "user-static"

	service := newClaimsService(db, fakeCustomClaimSource{}, "https://id.example.com", nil)
	require.NoError(t, db.Create(&model.User{Base: model.Base{ID: userID}, Username: "u"}).Error)

	client := &model.OidcClient{
		Base: model.Base{ID: "c-static"},
		Credentials: model.OidcClientCredentials{
			ClaimRemappings: []model.OidcClientClaimRemapping{
				{ClaimName: "num", SourceType: model.RemappingSourceStatic, SourceValue: "42"},
				{ClaimName: "flag", SourceType: model.RemappingSourceStatic, SourceValue: "true"},
				{ClaimName: "arr", SourceType: model.RemappingSourceStatic, SourceValue: `["a"]`},
				{ClaimName: "text", SourceType: model.RemappingSourceStatic, SourceValue: "plain text"},
			},
		},
	}

	claims, err := service.GetUserClaimsForClient(t.Context(), userID, []string{"openid"}, client)
	require.NoError(t, err)
	assert.Equal(t, float64(42), claims["num"])
	assert.Equal(t, true, claims["flag"])
	assert.Equal(t, []any{"a"}, claims["arr"])
	assert.Equal(t, "plain text", claims["text"])
}

// TestClaimsServiceRemappingSkipsReservedClaimsAtApplyTime is a defense-in-depth check
// A legacy row somehow bypassing validation must never be able to override token-critical claims at issuance time
func TestClaimsServiceRemappingSkipsReservedClaimsAtApplyTime(t *testing.T) {
	claims := map[string]any{"sub": "user-1", "aud": "client-1", "iss": "https://id.example.com"}
	remappings := []model.OidcClientClaimRemapping{
		{ClaimName: "sub", SourceType: model.RemappingSourceStatic, SourceValue: "attacker"},
		{ClaimName: "aud", SourceType: model.RemappingSourceStatic, SourceValue: "other"},
		{ClaimName: "iss", SourceType: model.RemappingSourceStatic, SourceValue: "evil"},
		{ClaimName: "safe", SourceType: model.RemappingSourceStatic, SourceValue: "ok"},
	}

	applyClaimRemappings(claims, remappings, &model.User{}, nil)

	// Token-critical claims are preserved
	assert.Equal(t, "user-1", claims["sub"])
	assert.Equal(t, "client-1", claims["aud"])
	assert.Equal(t, "https://id.example.com", claims["iss"])
	// Non-reserved remapping still applies
	assert.Equal(t, "ok", claims["safe"])
}
