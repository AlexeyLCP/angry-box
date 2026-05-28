## Summary

This PR brings the first major release after the clean rebrand and backend completion — **v0.2.0**.

### Major Changes

**AWG Obfuscation (pumbaX integration)**
- Full integration of advanced AmneziaWG CPS generators from [pumbaX/awg-multi-script](https://github.com/pumbaX/awg-multi-script) ("бери все").
- New `pro_2026` and updated `maximum_stealth_2026` presets with full J/S/H ranges and I1–I5 chain (QUIC Initial 1200B Chrome-like, realistic SIP REGISTER, DNS+EDNS0).
- Explicit "Security > Compatibility" policy for stealth profiles.
- Stable entry credentials: AWG server keypair + CPS I1–I5 are now generated **once** at chain creation and never rotate on re-apply.

**Advanced XHTTP Obfuscation**
- New powerful XHTTP generators (`xhttp_cps.go`) ported from community best practices:
  - Random-range header padding
  - XMUX-style multiplexing controls
  - Realistic browser-like headers (NaiveProxy inspiration)
  - `GenerateXHTTPExtra()` for advanced configs
  - Upstream/Downstream separation hints
- These features are now available in **both** sing-box and xray backends.

**New Preset**
- Added `xhttp_max_stealth_2026` — extreme security-focused profile combining heavy XHTTP obfuscation + pro-level AWG.

**Documentation**
- Complete multilingual README overhaul:
  - Primary English (`README.md`)
  - Russian, Chinese, and Persian/Farsi versions
  - Language switcher at the very top of every file
- Updated installation instructions, architecture, and credits.

**CI/CD & Release Engineering**
- Improved GitHub Actions workflows:
  - `build.yml` — tests, vet, golangci-lint, cross-build matrix (amd64/arm64/armv7/keenetic-mipsel)
  - `release.yml` — automated multi-arch builds, UPX compression, tarballs with checksums, automatic GitHub Releases on `v*` tags
- Added `.golangci.yml` for consistent linting
- Release 0.2.0 preparation (version injection, packaging improvements)

### Breaking / Notable Changes
- Stronger default obfuscation in stealth profiles (may affect very old clients).
- All GitHub references and module path updated to `github.com/alexeylcp/angry-box`.

### Credits
Huge thanks to the community projects and researchers whose work was directly incorporated:
- pumbaX (AWG CPS)
- XTLS/RPRX team (XHTTP research)
- klzgrad (NaiveProxy)
- apernet/Hysteria team (Gecko)
- telemt
- Many others (see Acknowledgments section in README)

## Test Plan
- All unit tests pass (`go test ./...`)
- Cross-builds succeed for all supported targets
- New generators and presets validated
- Multilingual docs reviewed

## Checklist
- [x] All tests green
- [x] CI workflows updated and tested
- [x] Documentation complete (4 languages)
- [x] Version bumped and release artifacts ready
- [x] Credits and acknowledgments added

This is a significant step forward for real-world usability under heavy DPI (Russia/Iran/China 2026 threat model).
