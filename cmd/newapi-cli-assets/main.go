// Build the backend's CLI distribution before building the gateway.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/clidistribution"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build() error {
	version, err := os.ReadFile("VERSION")
	if err != nil {
		return err
	}
	v := strings.TrimSpace(string(version))
	if len(os.Args) > 1 {
		v = os.Args[1]
	}
	if v == "" || strings.ContainsAny(v, " \t\r\n'") {
		return fmt.Errorf("invalid version")
	}
	out := "pkg/cliassets/dist"
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	m := clidistribution.Manifest{Version: v, Assets: map[string]clidistribution.Asset{}}
	for _, platform := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"} {
		goos, goarch, _ := strings.Cut(platform, "/")
		name := "newapi-cli-" + goos + "-" + goarch
		if goos == "windows" {
			name += ".exe"
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X github.com/QuantumNous/new-api/cmd/newapi-cli/internal/buildinfo.Version="+v, "-o", filepath.Join(out, name), "./cmd/newapi-cli")
		// Replace inherited target variables: the builder itself runs on the host.
		for _, env := range os.Environ() {
			if strings.HasPrefix(env, "GOOS=") || strings.HasPrefix(env, "GOARCH=") || strings.HasPrefix(env, "CGO_ENABLED=") {
				continue
			}
			cmd.Env = append(cmd.Env, env)
		}
		cmd.Env = append(cmd.Env, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		fmt.Println("Building", name)
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	if err := packageSkills(filepath.Join(out, "skills.zip")); err != nil {
		return err
	}
	for _, name := range []string{"newapi-cli-linux-amd64", "newapi-cli-linux-arm64", "newapi-cli-darwin-amd64", "newapi-cli-darwin-arm64", "newapi-cli-windows-amd64.exe", "newapi-cli-windows-arm64.exe", "skills.zip"} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			return err
		}
		m.Assets[name] = clidistribution.Asset{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data))}
	}
	data, err := common.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "manifest.json"), data, 0644)
}

func packageSkills(destination string) (err error) {
	f, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	z := zip.NewWriter(f)
	defer func() { err = errors.Join(err, z.Close()) }()
	for _, skill := range []string{"newapi-cli", "new-api-app-framework"} {
		root := filepath.Join(".agents", "skills", skill)
		if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
			return err
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if entry.IsDir() {
				if name == "node_modules" || name == ".git" || name == "dist" || name == ".next" || name == ".temp" || name == ".cache" || name == ".vercel" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || (strings.HasPrefix(name, ".env") && !strings.HasSuffix(name, ".example")) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			w, err := z.Create(skill + "/" + filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			r, err := os.Open(path)
			if err != nil {
				return err
			}
			if strings.HasSuffix(path, ".sh") {
				data, readErr := io.ReadAll(r)
				closeErr := r.Close()
				if err := errors.Join(readErr, closeErr); err != nil {
					return err
				}
				_, err := w.Write(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
				return err
			}
			_, copyErr := io.Copy(w, r)
			return errors.Join(copyErr, r.Close())
		})
		if err != nil {
			return err
		}
	}
	return nil
}
