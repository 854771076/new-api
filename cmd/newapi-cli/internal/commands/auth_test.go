package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

func TestIsLoopbackServer(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:3001":    true,
		"http://127.0.0.1:3001":    true,
		"http://[::1]:3001":        true,
		"http://192.168.1.10:3001": false,
		"https://api.example.com":  false,
	}
	for server, want := range cases {
		assert.Equal(t, want, isLoopbackServer(server), server)
	}
}
