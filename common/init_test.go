package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

// TestErrorLogEnabledDefault pins the product decision that the ERROR_LOG_ENABLED
// switch is ON unless a deployment explicitly turns it off: the default
// deployment must not leave failed requests without any log line (they were
// invisible in the admin log, and the failed-path latency breakdown never
// reached the log table).
//
// The env var is empty-but-set here on purpose: GetEnvOrDefaultBool treats an
// empty value as "not set" and falls back to the code default.
func TestErrorLogEnabledDefault(t *testing.T) {
	original := constant.ErrorLogEnabled
	t.Cleanup(func() { constant.ErrorLogEnabled = original })

	t.Setenv("ERROR_LOG_ENABLED", "")
	initConstantEnv()
	assert.True(t, constant.ErrorLogEnabled,
		"ERROR_LOG_ENABLED must default to true when the env var is not set")
	assert.True(t, constant.DefaultErrorLogEnabled,
		"the documented default must stay true")

	t.Setenv("ERROR_LOG_ENABLED", "false")
	initConstantEnv()
	assert.False(t, constant.ErrorLogEnabled,
		"an explicit ERROR_LOG_ENABLED=false must still win over the default")
}
