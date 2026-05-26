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

# ── Optimized ARM64 (PIE build) ──
arm64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOENV) $(GO) build -trimpath \
		-ldflags="$(LDFLAGS)" -buildmode=pie \
		-o $(OUT_DIR)/$(APP)-linux-arm64 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-arm64 (PIE)"

# ── Router builds (UPX compressed) ──
router-builds: cross
	@echo "=== Compressing router builds with UPX ==="
	@if command -v upx >/dev/null; then \
		upx --best --lzma $(OUT_DIR)/$(APP)-linux-arm64; \
		upx --best --lzma $(OUT_DIR)/$(APP)-linux-armv7; \
	else \
		echo "UPX not installed — skipping compression"; \
	fi
	@ls -lh $(OUT_DIR)/

# ══════════════════════════════════════════════════════
# Keenetic (mipsel via dockcross + CGO)
# ══════════════════════════════════════════════════════

keenetic: web
	@echo "=== Building for Keenetic (mipsel via QEMU) ==="
	@mkdir -p $(OUT_DIR)
	docker run --rm --platform linux/mipsle \
		-v "$(PWD)":/work -w /work \
		-e "VERSION=$(VERSION)" \
		golang:1.26-bookworm \
		bash -c ' \
			set -e; \
			apt-get update -qq && apt-get install -y -qq gcc 2>/dev/null || true; \
			CGO_ENABLED=1 GOOS=linux GOARCH=mipsle GOMIPS=softfloat \
			go build -tags sqlite_cgo -trimpath \
				-ldflags "-s -w -X github.com/alexeylcp/lucx-core/internal/api.Version=$${VERSION}" \
				-o /tmp/lucx-core \
				./cmd/lucx-core/; \
			upx --best --lzma /tmp/lucx-core 2>/dev/null || true; \
			cp /tmp/lucx-core /work/$(OUT_DIR)/$(APP)-keenetic-mipsel; \
		'
	@chmod +x $(OUT_DIR)/$(APP)-keenetic-mipsel
	@echo ""
	@echo "Keenetic binary ready:"
	@ls -lh $(OUT_DIR)/$(APP)-keenetic-mipsel
	@echo ""
	@echo "To install on Keenetic:"
	@echo "  scp build/$(APP)-keenetic-mipsel root@<keenetic>:/opt/bin/$(APP)"

keenetic-package: keenetic
	@./scripts/package-keenetic.sh $(VERSION)

# ══════════════════════════════════════════════════════
# Release pipeline
# ══════════════════════════════════════════════════════

release: test web build-all keenetic
	@echo "=== Creating release tarballs ($(VERSION)) ==="
	@mkdir -p $(OUT_DIR)/release
	@for target in linux-amd64 linux-arm64 linux-armv7 linux-arm64-v8 keenetic-mipsel; do \
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
	@./scripts/package-keenetic.sh $(VERSION) 2>/dev/null && \
		cp $(OUT_DIR)/*.ipk $(OUT_DIR)/release/ 2>/dev/null || true
	@echo ""
	@echo "=== Release files ==="
	@ls -lh $(OUT_DIR)/release/

size:
	@ls -lh $(OUT_DIR)/
