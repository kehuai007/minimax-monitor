// Package version exposes the build-time version string for minimax-monitor.
package version

// Version is set at build time via:
//   go build -ldflags "-X minimax-monitor/internal/version.Version=1.2.3"
//
// Local builds (no ldflags) report "dev".
var Version = "dev"
