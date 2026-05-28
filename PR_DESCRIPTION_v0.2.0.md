## Angry-BOX v0.2.0 — Advanced 2026 Obfuscation + Router Packaging

**Closes #3**

### What changed

- Full 2026 CPS + XHTTP obfuscation engine (highest priority item from community feedback):
  - `internal/chain/awg_cps.go`: exact ports of the best generators from pumbaX/awg-multi-script (QUIC Initial exactly 1200B Chrome fb, QUIC Short, realistic SIP REGISTER, DNS+EDNS0) + levels 0-3 + mimicry.
  - Two new force-security presets: `pro_2026` and `xhttp_max_stealth_2026` (CPS level 3 + QUIC I1 hard-enforced — "security > compatibility").
  - Integration into `ApplyChain` / `buildAWGUserInbound` via `buildAmneziaSection` so `apply-chain` with these presets actually deploys the real I1-I5 material.
  - Rich `AWGClientMaterial` in ApplyReport (CPSLevel, Mimicry, I1Len, I1Type) so users see exactly what was pushed.
  - Standalone `config` command parity improved for the new presets.

- Real router packaging (major user request):
  - `scripts/build-opkg.sh` (universal)
  - `packaging/keenetic/` and `packaging/openwrt-aarch64/` (control + postinst)
  - Makefile targets: `make build-keenetic-opkg`, `make build-arm64-opkg`
  - CI: `.github/workflows/release.yml` now produces `.ipk` artifacts for mipsel_24kc and aarch64_cortex-a53 and includes them in GitHub Releases.

- Documentation:
  - English primary `README.md` + `README.ru.md` / `README.zh.md` / `README.fa.md`
  - Language switcher at the absolute top of every file
  - Full credits section naming pumbaX, RPRX/Xray#4113, Hysteria Gecko, NaiveProxy, Hiddify, 3x-ui etc.
  - `.ipk` / `opkg install` instructions for Keenetic and OpenWRT

### Breaking / Migration

None. Existing chains and presets continue to work. New presets are additive.

### Testing

- All existing tests + new `awg_cps_test.go` pass (`go test ./...`)
- `go vet` clean
- Manual verification of `apply-chain --profile pro_2026 --user-protocol awg` produces 1200B I1 + correct amnezia section (confirmed in worktree)

### Known limitations (tracked for 0.2.1)

- Full advanced XHTTP padding/XMUX not yet wired into the xray backend (xray remains best-effort secondary).
- sing-box standalone `config -type user --protocol awg` with pro_2026 does not yet emit the CPS packets (only the chain applier path does today).
- Coverage on the new obfuscation code is ~60-70% (reviewer flagged); more integration tests exercising full `ApplyChain` + pro_2026 will be added in follow-up.

### How to test the release artifacts

```bash
# After the tag is pushed and release published
curl -L -O https://github.com/alexeylcp/angry-box/releases/download/v0.2.0/angry-box_0.2.0_mipsel_24kc.ipk
# or the aarch64 one
opkg install angry-box_0.2.0_*.ipk
```

### Credits

Huge thanks to the entire proxy research community (especially pumbaX for the "бери все" generators and the Xray team for the XHTTP work). This release would not exist without your public research.

---

**Full diff**: the `task/3-advanced-obfs-2026` branch (worktree-isolated per project process).

Self-review performed with clean-context subagent (see issue #3 comments).
