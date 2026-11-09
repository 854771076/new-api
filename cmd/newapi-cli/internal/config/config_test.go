package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)

	cfg := &Config{Profiles: map[string]*Profile{}}
	cfg.SetProfile("default", &Profile{Server: "https://a.example.com", Token: "nap_a", ExpiresAt: 1735689600})
	cfg.SetProfile("staging", &Profile{Server: "https://b.example.com", Token: "nap_b", ExpiresAt: 0})
	require.NoError(t, cfg.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	require.Len(t, loaded.Profiles, 2)

	def := loaded.Profile("default")
	require.NotNil(t, def)
	assert.Equal(t, "https://a.example.com", def.Server)
	assert.Equal(t, "nap_a", def.Token)
	assert.Equal(t, int64(1735689600), def.ExpiresAt)

	staging := loaded.Profile("staging")
	require.NotNil(t, staging)
	assert.Equal(t, "https://b.example.com", staging.Server)
}

func TestSavePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	cfg := &Config{Profiles: map[string]*Profile{}}
	require.NoError(t, cfg.Save(path))

	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
}

func TestSaveRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires extra privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	require.NoError(t, os.WriteFile(target, []byte("{}"), 0o600))
	link := filepath.Join(dir, "link.json")
	require.NoError(t, os.Symlink(target, link))

	cfg := &Config{Profiles: map[string]*Profile{}}
	cfg.SetProfile("default", &Profile{Server: "https://a.example.com", Token: "nap_a"})
	err := cfg.Save(link)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data), "symlink target must stay untouched")
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	path := filepath.Join(dir, FileName)
	cfg := &Config{Profiles: map[string]*Profile{}}
	require.NoError(t, cfg.Save(path))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, FileName, entries[0].Name())
}

func TestSaveCleansUpTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	// A directory at the destination makes the final rename fail after the
	// temporary file has been written.
	require.NoError(t, os.Mkdir(path, 0o700))

	cfg := &Config{Profiles: map[string]*Profile{}}
	require.Error(t, cfg.Save(path))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, FileName, entries[0].Name())
	assert.True(t, entries[0].IsDir(), "only the blocking directory should remain")
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), DirName, FileName))
	require.NoError(t, err)
	require.NotNil(t, cfg.Profiles)
	assert.Empty(t, cfg.Profiles)
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	_, err := Load(path)
	require.Error(t, err)
}

func TestDeleteProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)

	cfg := &Config{Profiles: map[string]*Profile{}}
	cfg.SetProfile("default", &Profile{Server: "https://a.example.com", Token: "nap_a"})
	cfg.SetProfile("extra", &Profile{Server: "https://b.example.com", Token: "nap_b"})
	require.NoError(t, cfg.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	loaded.DeleteProfile("extra")
	loaded.DeleteProfile("absent")
	require.NoError(t, loaded.Save(path))

	reloaded, err := Load(path)
	require.NoError(t, err)
	assert.Nil(t, reloaded.Profile("extra"))
	assert.NotNil(t, reloaded.Profile("default"))
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, DirName, FileName), path)
}
