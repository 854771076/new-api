package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminCanReadWriteConcurrencyPermissions(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	assert.True(t, Can(101, common.RoleAdminUser, ConcurrencyRead))
	assert.True(t, Can(101, common.RoleAdminUser, ConcurrencyWrite))
}

func TestCommonUserCannotReadConcurrencyPermissions(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	assert.False(t, Can(202, common.RoleCommonUser, ConcurrencyRead))
	assert.False(t, Can(202, common.RoleCommonUser, ConcurrencyWrite))
}

func TestRootUserShortCircuits(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	assert.True(t, Can(303, common.RoleRootUser, ConcurrencyRead))
	assert.True(t, Can(303, common.RoleRootUser, ConcurrencyWrite))
}