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
// the kernel/TUN-overlay stability. The flag is stamped on the NodeInfo used
// for this render and persisted so the status-page hash matches the last apply.
//
// Best-effort: a probe error (modinfo/awg missing, SSH hiccup) is treated as
// "not supported" (false) rather than failing the whole deploy — the userspace
// fallback renders a working config either way. The deploy log notes the
// detection outcome so the operator can see WHY a v3 inbound went userspace.
// modinfo / awg --version are world-readable, so no sudo wrapping is needed.
func detectKernelAWG3(ctx context.Context, client ports.SSHClient) bool {
	// 1. Kernel module HPK support. Prefer the FUNCTIONAL probe: the PR #192
	//    build's header_protection.c exports awg_header_protection_set_key into
	//    /proc/kallsyms — immune to the fake "1.0.0" PACKAGE_VERSION that upstream
	//    stamps into every dkms.conf/Makefile (lucx-ui c3001499 found the
	//    version-parse approach broken for exactly this reason). Fall back to the
	//    modinfo major>=3 parse for modules that report a real 3.x version.
	kallsyms, _, _, _ := client.RunWithOutput(ctx,
		"grep -q awg_header_protection_set_key /proc/kallsyms && echo yes || echo no",
		15*time.Second)
	hasSym := strings.TrimSpace(kallsyms) == "yes"

	modVer, _, _, _ := client.RunWithOutput(ctx,
		"modinfo amneziawg 2>/dev/null | awk '/^version:/{print $2; exit}'",
		15*time.Second)
	modVer = strings.TrimSpace(modVer)
	if !hasSym && !awgKernelVersionSupportsHPK(modVer) {
		log.Printf("awg3-kernel: node module (kallsyms=%v, version %q) does not support header protection (need PR #192) — AWG 3.0 inbounds fall back to userspace", hasSym, modVer)
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
// RenderNodeAWGConfs) read, keyed off NodeInfo.KernelAWG3Supported (probed at
// deploy, persisted). nil-safe for dry-run / preview paths without a probe.
func kernelAWG3EnabledFor(nodeInfo *model.NodeInfo) bool {
	return nodeInfo != nil && nodeInfo.KernelAWG3Supported
}

func persistKernelAWG3(store *Store, nodeID string, supported bool) {
	info, err := store.GetNodeInfo(nodeID)
	if err != nil || info == nil || info.KernelAWG3Supported == supported {
		return
	}
	info.KernelAWG3Supported = supported
	if err := store.SaveNodeInfo(info); err != nil {
		log.Printf("awg3-kernel: persist flag for %s: %v", nodeID, err)
	}
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
