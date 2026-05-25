.PHONY: all build clean test vet web dev size
.PHONY: cross cross-amd64 cross-arm64 cross-armv7 cross-mips cross-mipsle
.PHONY: build-all router-builds keenetic release

APP := lucx-core
OUT_DIR := build
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/alexeylcp/lucx-core/internal/api.Version=$(VERSION)
GO := go
GOENV := CGO_ENABLED=0 PATH="$$HOME/.local/go/bin:$$PATH" GOTOOLCHAIN=auto

all: test build

# ── Web UI ──
web:
	cd lucx-web && npm run build && rm -rf ../web/dist && cp -r dist ../web/

# ── Go backend (after web) ──
build: web
	$(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP) ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP) ($(VERSION))"

# ── Tests ──
test:
	$(GOENV) $(GO) test ./... -count=1 -timeout 90s

vet:
	$(GOENV) $(GO) vet ./...

# ── Dev mode ──
dev:
	@echo "Starting LucX Core + Web Dev Server..."
	@echo "Core: http://localhost:8744"
	@echo "Web:  http://localhost:5173"
	@$(GOENV) $(GO) run ./cmd/$(APP)/ -db ./lucx.db &
	@sleep 2
	@cd lucx-web && npm run dev

# ── Clean ──
clean:
	rm -rf $(OUT_DIR)

# ══════════════════════════════════════════════════════
# Cross-compilation targets
# ══════════════════════════════════════════════════════

# Standard cross-compile (4 targets)
cross: cross-amd64 cross-arm64 cross-mipsle cross-armv7
	@echo "=== Cross-compilation complete ($(VERSION)) ==="

# All architectures including MIPS Big Endian
build-all: cross cross-mips
	@echo "=== All architectures built ($(VERSION)) ==="

# ── Individual targets ──

cross-amd64:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=amd64 $(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-amd64 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-amd64"

cross-arm64:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=arm64 $(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-arm64 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-arm64"

cross-armv7:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=arm GOARM=7 $(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-armv7 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-armv7"

# MIPS Little Endian — primary Keenetic target
cross-mipsle:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS) -buildid=" -o $(OUT_DIR)/$(APP)-keenetic-mipsel ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-keenetic-mipsel"

# MIPS Big Endian — older OpenWrt / some Keenetic models
cross-mips:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=mips GOMIPS=softfloat $(GOENV) $(GO) build -trimpath -ldflags="$(LDFLAGS) -buildid=" -o $(OUT_DIR)/$(APP)-openwrt-mips ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-openwrt-mips"

# ── Router builds (UPX compressed) ──
router-builds: cross
	@echo "=== Compressing router builds with UPX ==="
	@if command -v upx >/dev/null; then \
		upx --best --lzma $(OUT_DIR)/$(APP)-linux-arm64; \
		upx --best --lzma $(OUT_DIR)/$(APP)-linux-mipsle; \
		upx --best --lzma $(OUT_DIR)/$(APP)-linux-armv7; \
	else \
		echo "UPX not installed — skipping compression"; \
	fi
	@ls -lh $(OUT_DIR)/

# ══════════════════════════════════════════════════════
# Keenetic-specific
# ══════════════════════════════════════════════════════

keenetic: web cross-mipsle cross-mips
	@echo "=== Building Keenetic packages ==="
	@if command -v upx >/dev/null; then \
		upx --best --lzma $(OUT_DIR)/$(APP)-keenetic-mipsel; \
		upx --best --lzma $(OUT_DIR)/$(APP)-openwrt-mips; \
	fi
	@chmod +x $(OUT_DIR)/$(APP)-keenetic-mipsel $(OUT_DIR)/$(APP)-openwrt-mips
	@echo ""
	@echo "Keenetic binaries ready:"
	@ls -lh $(OUT_DIR)/$(APP)-keenetic-mipsel $(OUT_DIR)/$(APP)-openwrt-mips
	@echo ""
	@echo "To install on Keenetic:"
	@echo "  scp build/$(APP)-keenetic-mipsel root@<keenetic>:/opt/bin/$(APP)"
	@echo ""
	@echo "To create .ipk package, run: ./scripts/package-keenetic.sh $(VERSION)"

keenetic-package: keenetic
	@./scripts/package-keenetic.sh $(VERSION)

# ══════════════════════════════════════════════════════
# Release pipeline
# ══════════════════════════════════════════════════════

release: test web build-all keenetic
	@echo "=== Creating release tarballs ($(VERSION)) ==="
	@mkdir -p $(OUT_DIR)/release
	@for target in linux-amd64 linux-arm64 linux-armv7 openwrt-mips keenetic-mipsel; do \
		BIN="$(OUT_DIR)/$(APP)-$$target"; \
		if [ -f "$$BIN" ]; then \
			TAR_NAME="$(APP)-$(VERSION)-$$target"; \
			TAR_DIR="$(OUT_DIR)/release/$$TAR_NAME"; \
			mkdir -p "$$TAR_DIR"; \
			cp "$$BIN" "$$TAR_DIR/$(APP)"; \
			cp README.md "$$TAR_DIR/"; \
			chmod +x "$$TAR_DIR/$(APP)"; \
			tar czf "$(OUT_DIR)/release/$$TAR_NAME.tar.gz" -C "$(OUT_DIR)/release" "$$TAR_NAME"; \
			rm -rf "$$TAR_DIR"; \
			echo "  → release/$$TAR_NAME.tar.gz"; \
		fi; \
	done
	@echo ""
	./scripts/package-keenetic.sh $(VERSION) 2>/dev/null && \
		cp $(OUT_DIR)/*.ipk $(OUT_DIR)/release/ 2>/dev/null || true
	@echo ""
	@echo "=== Release files ==="
	@ls -lh $(OUT_DIR)/release/

size:
	@ls -lh $(OUT_DIR)/
