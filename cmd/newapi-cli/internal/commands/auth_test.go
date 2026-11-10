package commands

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/buildinfo"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/config"
)

func TestLoginServerPrecedence(t *testing.T) {
	original := buildinfo.DefaultServer
	buildinfo.DefaultServer = "https://default.example.com"
	t.Cleanup(func() { buildinfo.DefaultServer = original })

	profile := &config.Profile{Server: "https://profile.example.com"}

	t.Run("explicit flag wins", func(t *testing.T) {
		t.Setenv(config.EnvServer, "https://env.example.com")
		assert.Equal(t, "https://flag.example.com", loginServer("https://flag.example.com", profile))
	})

	t.Run("env wins over profile and default", func(t *testing.T) {
		t.Setenv(config.EnvServer, "https://env.example.com")
		assert.Equal(t, "https://env.example.com", loginServer("", profile))
	})

	t.Run("profile wins over build default", func(t *testing.T) {
		t.Setenv(config.EnvServer, "")
		assert.Equal(t, "https://profile.example.com", loginServer("", profile))
	})

	t.Run("build default is the last fallback", func(t *testing.T) {
		t.Setenv(config.EnvServer, "")
		assert.Equal(t, "https://default.example.com", loginServer("", nil))
	})
}

func TestLoginRejectsPlaintextRemoteServer(t *testing.T) {
	cmd := newAuthCmd()
	cmd.SetArgs([]string{"login", "--server", "http://api.example.com"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	var cmdErr *CommandError
	require.ErrorAs(t, err, &cmdErr)
	assert.Equal(t, ExitUsage, cmdErr.ExitCode)
	assert.Equal(t, "config_error", cmdErr.Code)
}
