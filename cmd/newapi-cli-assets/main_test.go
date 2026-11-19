package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageSkillsExcludesSecretsAndNormalizesShellScripts(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"newapi-cli", "new-api-app-framework"} {
		root := filepath.Join(".agents", "skills", name)
		for path, data := range map[string]string{
			"SKILL.md":                 "skill contract",
			".env.example":             "EXAMPLE=value",
			".env.local":               "SECRET=must-not-publish",
			"node_modules/private.txt": "dependency",
			"dist/private.txt":         "build output",
			".temp/project-ref":        "local project",
			"scripts/check.sh":         "#!/bin/sh\r\nexit 0\r\n",
		} {
			full := filepath.Join(root, path)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0700))
			require.NoError(t, os.WriteFile(full, []byte(data), 0600))
		}
	}
	require.NoError(t, packageSkills("skills.zip"))
	z, err := zip.OpenReader("skills.zip")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, z.Close()) })
	contents := make(map[string]string)
	for _, file := range z.File {
		r, err := file.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		contents[file.Name] = string(data)
	}
	assert.Len(t, contents, 6)
	for _, name := range []string{"newapi-cli", "new-api-app-framework"} {
		assert.Equal(t, "skill contract", contents[name+"/SKILL.md"])
		assert.Equal(t, "EXAMPLE=value", contents[name+"/.env.example"])
		assert.Equal(t, "#!/bin/sh\nexit 0\n", contents[name+"/scripts/check.sh"])
	}
}
