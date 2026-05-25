.PHONY: all build clean test vet cross cross-all router-builds web dev

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
	$(GOENV) $(GO) build -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP) ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP) ($(VERSION))"

# ── Tests ──
test:
	$(GOENV) $(GO) test ./... -count=1 -timeout 60s

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

# ── Cross-compile ──
cross: cross-amd64 cross-arm64 cross-mipsle cross-armv7
	@echo "=== All cross-compilation targets built ($(VERSION)) ==="

cross-amd64:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=amd64 $(GOENV) $(GO) build -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-amd64 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-amd64"

cross-arm64:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=arm64 $(GOENV) $(GO) build -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-arm64 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-arm64"

cross-mipsle:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GOENV) $(GO) build -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-mipsle ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-mipsle"

cross-armv7:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=arm GOARM=7 $(GOENV) $(GO) build -ldflags="$(LDFLAGS)" -o $(OUT_DIR)/$(APP)-linux-armv7 ./cmd/$(APP)/
	@echo "  → $(OUT_DIR)/$(APP)-linux-armv7"

# ── Router builds (UPX compressed) ──
router-builds: cross
	@echo "=== Compressing router builds with UPX ==="
	@which upx > /dev/null && upx --best $(OUT_DIR)/$(APP)-linux-arm64 || echo "UPX not installed"
	@which upx > /dev/null && upx --best $(OUT_DIR)/$(APP)-linux-mipsle || echo "UPX not installed"
	@which upx > /dev/null && upx --best $(OUT_DIR)/$(APP)-linux-armv7 || echo "UPX not installed"
	@ls -lh $(OUT_DIR)/

size:
	@ls -lh $(OUT_DIR)/
