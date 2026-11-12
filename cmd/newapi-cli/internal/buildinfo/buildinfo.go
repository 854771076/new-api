// Package buildinfo carries values injected at build time.
package buildinfo

// DefaultServer is the new-api server URL baked into the binary by the release
// workflow through -ldflags -X. It is empty for local builds.
var DefaultServer = ""

// Version is the CLI version baked into the binary by the release workflow
// through -ldflags -X. It is "dev" for local builds.
var Version = "dev"
