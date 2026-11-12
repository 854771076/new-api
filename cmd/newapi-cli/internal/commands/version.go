package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/buildinfo"
)

const (
	npmPackageName = "@aswless_854771076/newapi-cli"
	// The scoped npm package name must be URL-encoded on the registry path:
	// only the "/" becomes "%2F", while the "@" stays as-is.
	npmRegistryURL = "https://registry.npmjs.org/@aswless_854771076%2Fnewapi-cli/latest"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version and check for updates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "newapi-cli version %s\n", buildinfo.Version)
			checkNPMUpdate(cmd)
			return nil
		},
	}
}

func newSelfUpdateCmd() *cobra.Command {
	var run bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update newapi-cli to the latest npm release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			command := "npm install -g " + npmPackageName + "@latest"
			if !run {
				fmt.Fprintf(cmd.OutOrStdout(), "Run the following command to update newapi-cli (refreshes the binary and the bundled skills):\n  %s\nPass --run to execute it now.\n", command)
				return nil
			}
			parts := strings.Fields(command)
			execCmd := exec.Command(parts[0], parts[1:]...)
			execCmd.Stdout = cmd.OutOrStdout()
			execCmd.Stderr = cmd.ErrOrStderr()
			if err := execCmd.Run(); err != nil {
				return generalErr("self-update failed: %v", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&run, "run", false, "execute the npm install instead of printing it")
	return cmd
}

// checkNPMUpdate queries the npm registry for the latest published version and
// prints a one-line hint when it is newer than the running build. Network and
// parse failures are ignored; version checks never fail the command.
func checkNPMUpdate(cmd *cobra.Command) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(npmRegistryURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return
	}
	latest := strings.TrimSpace(meta.Version)
	if latest == "" {
		return
	}
	cmp, ok := compareVersions(latest, buildinfo.Version)
	if ok && cmp <= 0 {
		return
	}
	// Either the registry version is newer, or the comparison could not parse a
	// version: only ever print a hint, never fail.
	fmt.Fprintf(cmd.OutOrStdout(), "Update available: %s (running %s). Run: npm install -g %s@latest\n", latest, buildinfo.Version, npmPackageName)
}

// compareVersions compares two dotted version strings after stripping an
// optional "v" prefix. The second return reports whether the comparison was
// decisive; a non-numeric segment makes it inconclusive.
func compareVersions(a, b string) (int, bool) {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		var aok, bok bool
		if i < len(as) {
			an, aok = parseVersionSegment(as[i])
		}
		if i < len(bs) {
			bn, bok = parseVersionSegment(bs[i])
		}
		if !aok || !bok {
			return 0, false
		}
		if an != bn {
			if an > bn {
				return 1, true
			}
			return -1, true
		}
	}
	return 0, true
}

// parseVersionSegment reads the leading numeric prefix of a version segment.
func parseVersionSegment(s string) (int, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}
