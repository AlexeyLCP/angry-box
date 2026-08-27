// Package version holds the angry-box build version. It is a separate package
// (not main) so both cmd/angry-box and internal/web can import it without an
// import cycle (main imports web, so web cannot import main's `version` var).
//
// The value is overridable at link time via -ldflags "-X
// github.com/alexeylcp/angry-box/internal/version.Version=vX.Y.Z" (the release
// workflow injects the git tag). The default below is a fallback for local
// `go build` / `go run` without ldflags — bumped manually per release so an
// untagged build still reports a sane version (the UI sidebar + /health +
// `angry-box version` all read this).
package version

// Version is the angry-box build version. Override at link time with
// -ldflags "-X version.Version=vX.Y.Z".
var Version = "v0.8.31"
