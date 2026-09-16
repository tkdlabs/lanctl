// Package version holds the build version, injected at link time:
//
//	go build -ldflags "-X github.com/tkdlabs/lanctl/internal/version.Version=v1.2.3"
package version

// Version is the current build version. It defaults to "dev" for local builds.
var Version = "dev"
