package chain

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// detectKernelAWG3 reports whether the remote node can render an AWG 3.0
// (header-protection) inbound via the kernel awg-quick + sing-box-TUN-overlay
// path. Two conditions must BOTH hold (live-verified on n1, AGENTS #5 revision
// / PROGRESS §43):
//
//  1. The amnezia-box KERNEL MODULE supports HPK natively — the PR #192
//     "feat: AmneziaWG 3.0" merge (2026-07-30) added the netlink attrs
//     WGDEVICE_A_HEADER_PROTECTION_KEY / CONTENT_PADDING_ADDITION /
//     REKEY_AFTER_TIME. The module version (modinfo) is >= 3.0.
//  2. The userspace amneziawg-TOOLS (awg / awg-quick) parse the
//     `HeaderProtectionKey` keyword — added in amneziawg-tools v3.0.20260730
//     (PR #60). The old tools (v1.0.20260618-2 and earlier) reject the line
//     ("Line unrecognized"), so awg-quick up fails even when the module is new.
//
// When false, an AWGVersion=="3" inbound falls back to the userspace sing-box
// `type:"awg"` endpoint (the v0.8.10 path) — rendering still works, just without
// the kernel/TUN-overlay stability. The flag is set on the per-deploy NodeInfo
// copy and read by the render branches; it is never persisted.
//
// Best-effort: a probe error (modinfo/awg missing, SSH hiccup) is treated as
// "not supported" (false) rather than failing the whole deploy — the userspace
// fallback renders a working config either way. The deploy log notes the
// detection outcome so the operator can see WHY a v3 inbound went userspace.
// modinfo / awg --version are world-readable, so no sudo wrapping is needed.
func detectKernelAWG3(ctx context.Context, client ports.SSHClient) bool {
	// 1. Kernel module version (modinfo amneziawg). The PR #192 line ships a
	//    `version:` of 3.0.20260731-04 or similar; the legacy module is
	//    1.0.20260611. Parse the first numeric-major of the version line.
	modVer, _, _, _ := client.RunWithOutput(ctx,
		"modinfo amneziawg 2>/dev/null | awk '/^version:/{print $2; exit}'",
		15*time.Second)
	modVer = strings.TrimSpace(modVer)
	if !awgKernelVersionSupportsHPK(modVer) {
		log.Printf("awg3-kernel: node module version %q does not support header protection (need >= 3.0, PR #192) — AWG 3.0 inbounds fall back to userspace", modVer)
		return false
	}

	// 2. Userspace awg tool version. amneziawg-tools print
	//    "amneziawg-tools v3.0.20260730 - https://amnezia.org"; the legacy build
	//    prints "amneziawg-tools v1.0.20260618-2". v3.0.20260730+ has the
	//    HeaderProtectionKey keyword (config.c).
	toolVer, _, _, _ := client.RunWithOutput(ctx,
		"awg --version 2>/dev/null | head -1",
		15*time.Second)
	toolVer = strings.TrimSpace(toolVer)
	if !awgToolsVersionSupportsHPK(toolVer) {
		log.Printf("awg3-kernel: node awg tool %q does not support header protection (need amneziawg-tools >= v3.0.20260730) — AWG 3.0 inbounds fall back to userspace", toolVer)
		return false
	}

	log.Printf("awg3-kernel: node supports kernel AWG 3.0 (module %q, tools %q) — AWG 3.0 inbounds render via kernel awg-quick", modVer, toolVer)
	return true
}

// kernelAWG3EnabledFor reports whether an AWG 3.0 inbound on this node should
// render via the kernel awg-quick path (true) vs the userspace sing-box
// `type:"awg"` endpoint fallback (false). It is the single gate the render
// branches (buildStandaloneInOut, buildChainRoleInOut, awgTUNOverlayNeeded,
// RenderNodeAWGConfs) read, all keyed off the runtime-only NodeInfo flag
// stamped by the deploy pre-flight (detectKernelAWG3). nil-safe for the dry-run
// / preview render paths that pass a nodeInfo without a deploy context.
func kernelAWG3EnabledFor(nodeInfo *model.NodeInfo) bool {
	return nodeInfo != nil && nodeInfo.KernelAWG3Supported
}

// awgKernelVersionSupportsHPK parses an amnezia-box kernel module version
// string (from `modinfo amneziawg`) and reports whether it carries the AWG 3.0
// header-protection support. Accepts forms like "3.0.20260731-04",
// "1.0.20260611", "0.0.0". The rule is: major >= 3. (The PR #192 module's
// dkms.conf PACKAGE_VERSION is still "1.0.0", but modinfo reports the
// in-tree MODULE_VERSION which is bumped to 3.0.x — live-verified on n1.)
// An empty / unparseable string is treated as unsupported (fail-closed).
func awgKernelVersionSupportsHPK(version string) bool {
	major := awgVersionMajor(version)
	return major >= 3
}

// awgToolsVersionSupportsHPK parses an amneziawg-tools version line (e.g.
// "amneziawg-tools v3.0.20260730 - https://amnezia.org") and reports whether it
// supports the HeaderProtectionKey keyword (added in v3.0.20260730, PR #60).
// Rule: the major version token >= 3. Empty/unparseable → unsupported.
func awgToolsVersionSupportsHPK(versionLine string) bool {
	// Strip a leading "amneziaawg-tools" name and optional "v" prefix, then take
	// the first dot-separated numeric token.
	s := strings.TrimSpace(versionLine)
	s = strings.TrimPrefix(s, "amneziawg-tools")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	return awgVersionMajor(s) >= 3
}

// awgVersionMajor extracts the leading major version integer from a version
// string. Returns 0 for anything that does not start with digits (so callers
// treating ">= 3" correctly fail-closed on garbage). Examples:
//
//	"3.0.20260731-04"  → 3
//	"1.0.20260611"     → 1
//	"v3.0.20260730 -…" → 3 (after the v-strip in the caller)
//	"" / "unknown"     → 0
func awgVersionMajor(version string) int {
	var n int
	gotDigit := false
	for _, r := range version {
		if r < '0' || r > '9' {
			break
		}
		gotDigit = true
		n = n*10 + int(r-'0')
	}
	if !gotDigit {
		return 0
	}
	return n
}
