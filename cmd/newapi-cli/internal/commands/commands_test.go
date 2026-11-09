package commands

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runTokenCreate(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newTokenCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"create"}, args...))
	return cmd.Execute()
}

func requireUsageError(t *testing.T, err error, contains string) {
	t.Helper()
	require.Error(t, err)
	var cmdErr *CommandError
	require.True(t, errors.As(err, &cmdErr), "expected CommandError, got %T", err)
	assert.Equal(t, ExitUsage, cmdErr.ExitCode)
	assert.Equal(t, "usage_error", cmdErr.Code)
	assert.Contains(t, cmdErr.Message, contains)
}

func TestTokenCreateQuotaFlags(t *testing.T) {
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--quota", "10"), "--quota")
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--unlimited-quota=false", "--quota", "-5"), "negative")
}

func TestTokenCreateExpiresInMustBePositive(t *testing.T) {
	for _, value := range []string{"0s", "-1h", "-0.5s"} {
		t.Run(value, func(t *testing.T) {
			requireUsageError(t, runTokenCreate(t, "--name", "t", "--expires-in", value), "--expires-in")
		})
	}
}

func TestTokenCreateRejectsInvalidDuration(t *testing.T) {
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--expires-in", "24"), "Go duration")
}

func TestTokenCreateReachesRequestWithValidFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	tests := []struct {
		name string
		args []string
	}{
		{name: "default unlimited quota", args: []string{"--name", "t"}},
		{name: "explicit finite quota", args: []string{"--name", "t", "--unlimited-quota=false", "--quota", "10"}},
		{name: "positive expiry", args: []string{"--name", "t", "--expires-in", "1h"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runTokenCreate(t, tc.args...)
			var cmdErr *CommandError
			require.True(t, errors.As(err, &cmdErr), "expected CommandError, got %v", err)
			assert.Equal(t, ExitAuth, cmdErr.ExitCode)
			assert.Equal(t, "not_logged_in", cmdErr.Code)
		})
	}
}
