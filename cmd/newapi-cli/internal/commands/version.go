package commands

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/buildinfo"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/client"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/clidistribution"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{Use: "version", Short: "Print version and check your server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "newapi-cli version %s\n", buildinfo.Version)
			c, err := distributionClient(server)
			if err != nil {
				return nil
			}
			c.HTTPClient.Timeout = 5 * time.Second
			m, err := fetchManifest(c)
			if err == nil && m.Version != buildinfo.Version {
				fmt.Fprintf(cmd.OutOrStdout(), "Server distributes %s. Run: newapi-cli self-update --run --server %s\n", m.Version, c.BaseURL)
			}
			return nil
		}}
	cmd.Flags().StringVar(&server, "server", "", "new-api distribution server")
	return cmd
}

func newSelfUpdateCmd() *cobra.Command {
	var run bool
	var server string
	cmd := &cobra.Command{Use: "self-update", Short: "Install CLI and both global skills from your server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := distributionClient(server)
			if err != nil {
				return generalErr("update server: %v", err)
			}
			if !run {
				fmt.Fprintf(cmd.OutOrStdout(), "Install CLI and both skills from %s:\nnewapi-cli self-update --run --server %s\n", c.BaseURL, c.BaseURL)
				return nil
			}
			m, err := fetchManifest(c)
			if err != nil {
				return generalErr("manifest: %v", err)
			}
			name := "newapi-cli-" + runtime.GOOS + "-" + runtime.GOARCH
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			binary, err := downloadAsset(c, m, name)
			if err != nil {
				return generalErr("CLI download: %v", err)
			}
			skills, err := downloadAsset(c, m, "skills.zip")
			if err != nil {
				return generalErr("skills download: %v", err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			if err := installDistribution(home, binary, skills); err != nil {
				return generalErr("install: %v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed newapi-cli %s and both skills. Add %s to PATH. Login profiles are unchanged.\n", m.Version, filepath.Join(home, ".newapi-cli", "bin"))
			return nil
		}}
	cmd.Flags().BoolVar(&run, "run", false, "download, verify and install CLI and both skills")
	cmd.Flags().StringVar(&server, "server", "", "new-api distribution server")
	return cmd
}

func distributionClient(server string) (*client.Client, error) {
	_, cfg, profile, err := loadProfile()
	if err != nil {
		return nil, err
	}
	server = loginServer(server, cfg.Profile(profile))
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("use --server with a trusted HTTPS new-api URL")
	}
	c, err := client.New(server, "")
	if err == nil {
		c.HTTPClient.Timeout = 3 * time.Minute
	}
	return c, err
}

func fetchManifest(c *client.Client) (clidistribution.Manifest, error) {
	var m clidistribution.Manifest
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/cli/manifest")
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return m, fmt.Errorf("server returned HTTP %d (CLI assets may not be built)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil {
		return m, err
	}
	if len(data) > 64*1024 {
		return m, fmt.Errorf("manifest too large")
	}
	if err := common.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Version == "" || len(m.Assets) == 0 {
		return m, fmt.Errorf("empty distribution")
	}
	return m, nil
}

func downloadAsset(c *client.Client, m clidistribution.Manifest, name string) ([]byte, error) {
	a, ok := m.Assets[name]
	if !ok || a.Size <= 0 || a.Size > 128*1024*1024 || len(a.SHA256) != 64 {
		return nil, fmt.Errorf("invalid or unsupported asset %s", name)
	}
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/cli/download/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, a.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != a.SHA256 {
		return nil, fmt.Errorf("checksum or size mismatch for %s", name)
	}
	return data, nil
}

// Extract only the two expected skill trees into a private staging directory.
func extractSkills(data []byte, stage string) error {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var total uint64
	for _, f := range z.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) < 2 || (parts[0] != "newapi-cli" && parts[0] != "new-api-app-framework") || strings.ContainsAny(f.Name, "\\:") {
			return fmt.Errorf("invalid skill path %q", f.Name)
		}
		for _, p := range parts {
			if p == ".." || p == "." || (p != "" && strings.TrimRight(p, " .") != p) {
				return fmt.Errorf("unsafe skill path")
			}
		}
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return fmt.Errorf("unsupported archive entry")
		}
		if f.UncompressedSize64 > 128*1024*1024 || total > 128*1024*1024-f.UncompressedSize64 {
			return fmt.Errorf("skills exceed size limit")
		}
		total += f.UncompressedSize64
		path := filepath.Join(stage, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if strings.HasSuffix(f.Name, ".sh") {
			mode = 0700
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			r.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(r, int64(f.UncompressedSize64)+1))
		closeErr := out.Close()
		readErr := r.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if readErr != nil {
			return readErr
		}
	}
	for _, name := range []string{"newapi-cli", "new-api-app-framework"} {
		info, err := os.Stat(filepath.Join(stage, name, "SKILL.md"))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("missing %s/SKILL.md", name)
		}
	}
	return nil
}

// Stage every download before replacement; restore backups on a failed rename.
func installDistribution(home string, binary, skills []byte) error {
	binDir := filepath.Join(home, ".newapi-cli", "bin")
	skillDir := filepath.Join(home, ".agents", "skills")
	for _, dir := range []string{binDir, skillDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	stage, err := os.MkdirTemp(skillDir, ".newapi-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extractSkills(skills, stage); err != nil {
		return err
	}
	file, err := os.CreateTemp(binDir, ".newapi-update-")
	if err != nil {
		return err
	}
	stagedBinary := file.Name()
	defer os.Remove(stagedBinary)
	if _, err := file.Write(binary); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(stagedBinary, 0700); err != nil {
		return err
	}
	name := "newapi-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	targets := []string{filepath.Join(skillDir, "newapi-cli"), filepath.Join(skillDir, "new-api-app-framework"), filepath.Join(binDir, name)}
	sources := []string{filepath.Join(stage, "newapi-cli"), filepath.Join(stage, "new-api-app-framework"), stagedBinary}
	backups := make([]string, len(targets))
	installed := 0
	for i, target := range targets {
		// Unique backups also let Windows rename the running executable.
		backup := target + ".previous-" + filepath.Base(stage)
		if info, statErr := os.Lstat(target); statErr == nil {
			if (i < 2 && !info.IsDir()) || (i == 2 && !info.Mode().IsRegular()) {
				err = fmt.Errorf("unexpected installation target type: %s", target)
				break
			}
			err = os.Rename(target, backup)
			if err != nil {
				break
			}
			backups[i] = backup
		} else if !os.IsNotExist(statErr) {
			err = statErr
			break
		}
		err = os.Rename(sources[i], target)
		if err != nil {
			break
		}
		installed++
	}
	if err != nil {
		for i := len(targets) - 1; i >= 0; i-- {
			if i < installed {
				if e := os.RemoveAll(targets[i]); e != nil {
					return fmt.Errorf("%w; rollback removal: %v; backups: %v", err, e, backups)
				}
			}
			if backups[i] != "" {
				if e := os.Rename(backups[i], targets[i]); e != nil {
					return fmt.Errorf("%w; restore %s: %v", err, backups[i], e)
				}
			}
		}
		return err
	}
	for _, target := range targets {
		// Windows keeps the running executable locked; a later update removes it.
		previous, _ := filepath.Glob(target + ".previous-.newapi-update-*")
		for _, backup := range previous {
			_ = os.RemoveAll(backup)
		}
	}
	return nil
}
