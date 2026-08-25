package version

// Version and Commit are injected at build time via -ldflags (see Makefile).
var (
	Version = "v0.1.0-dev"
	Commit  = "unknown"
)
