// Package buildinfo contains the immutable identity embedded at build time.
package buildinfo

// Version and SourceRevision are set by the release container build with Go
// linker flags. Ordinary local builds retain explicit development defaults.
var (
	Version        = "development"
	SourceRevision = "unknown"
)
