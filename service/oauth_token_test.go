package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthAccessTokenInvalidatedByAuthVersionChange(t *testing.T) {
	user := setupAuthSessionTestDB(t)

	raw, expiresAt, err := IssueOAuthAccessToken(user.Id, "client-abc", "openid profile email")
	require.NoError(t, err)
	require.Positive(t, expiresAt)

	userID, clientID, scope, authVersion, err := ParseOAuthAccessToken(raw)
	require.NoError(t, err)
	assert.Equal(t, user.Id, userID)
	assert.Equal(t, "client-abc", clientID)
	assert.Equal(t, "openid profile email", scope)
	assert.Equal(t, int64(1), authVersion)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.NoError(t, err)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", 2).Error)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
}
