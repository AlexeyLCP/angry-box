//go:build wsl_smoke

package singbox

// downloadurl_test_helper.go — test-only hook to override the patched-binary
// download URL (used by the WSL smoke tests to point Deploy at a local HTTP
// server instead of GitHub). Guarded by the wsl_smoke build tag so it never
// ships in production builds.

// SetDownloadURLForTest overrides singBoxDownloadURLs[arch] and returns a
// restore function. Call the restore at test teardown. Checksum is left as-is
// (the local tarball is the same bytes as deps/, so the existing sha256 still
// verifies).
func SetDownloadURLForTest(arch, url string) (restore func()) {
	old := singBoxDownloadURLs[arch]
	singBoxDownloadURLs[arch] = url
	return func() { singBoxDownloadURLs[arch] = old }
}