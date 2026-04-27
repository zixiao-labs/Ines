// Package buildinfo carries values injected at link time via -ldflags.
package buildinfo

// Version is overridden at build time via -ldflags. Defaults to "dev" so
// `go run ./cmd/ines` keeps working without any extra flags.
var Version = "dev"
