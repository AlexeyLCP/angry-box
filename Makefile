# Angry-BOX Makefile
# Standard install/uninstall for Linux with systemd.

PREFIX       ?= /usr/local
DESTDIR      ?=
BINDIR       ?= $(PREFIX)/bin
CONFDIR      ?= /etc/angry-box
DATADIR      ?= /var/lib/angry-box
SYSTEMD_DIR  ?= /etc/systemd/system

BINARY       := angry-box
CMD_DIR      := ./cmd/angry-box
SERVICE_FILE := scripts/angry-box.service

# Version info (injected at build time)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

INSTALL      := install
INSTALL_BIN  := $(INSTALL) -m 755
INSTALL_DATA := $(INSTALL) -m 644
INSTALL_DIR  := $(INSTALL) -d -m 755

.PHONY: generate
generate:
	@echo "==> Generating templ components (web UI)..."
	@export PATH="$$HOME/.local/go/bin:$$PATH"; \
	go run github.com/a-h/templ/cmd/templ@latest generate ./web/templates
	@echo "    templates generated"
	@echo "==> Fixing duplicate imports in generated files (templ generator v0.3.x quirk)..."
	@cd web/templates && \
	for f in *_templ.go; do \
		awk 'BEGIN{seen=0} /^import "github.com\/a-h\/templ"$$/ {if(++seen>1) next} {print}' "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; \
	done
	@echo "    imports cleaned"

.PHONY: build
build: generate
	@echo "==> Building $(BINARY) (version=$(VERSION), commit=$(COMMIT))..."
	@export PATH="$$HOME/.local/go/bin:$$PATH"; \
	go build $(LDFLAGS) -o $(BINARY) $(CMD_DIR)
	@echo "    $(BINARY) built"

.PHONY: install
install: build
	@echo "==> Installing $(BINARY) to $(DESTDIR)$(BINDIR)..."
	$(INSTALL_DIR) "$(DESTDIR)$(BINDIR)"
	$(INSTALL_BIN) $(BINARY) "$(DESTDIR)$(BINDIR)/$(BINARY)"

	@echo "==> Creating directories..."
	$(INSTALL_DIR) "$(DESTDIR)$(CONFDIR)"
	$(INSTALL_DIR) "$(DESTDIR)$(DATADIR)"

	@if [ ! -f "$(DESTDIR)$(CONFDIR)/store.json" ]; then \
		echo '{"hosts":[],"chains":[]}' > "$(DESTDIR)$(CONFDIR)/store.json"; \
		echo "    Default store created at $(DESTDIR)$(CONFDIR)/store.json"; \
	fi

	@echo ""
	@echo "  Angry-BOX installed to $(DESTDIR)$(BINDIR)/$(BINARY)"
	@echo "  Config: $(DESTDIR)$(CONFDIR)/"
	@echo "  Data:   $(DESTDIR)$(DATADIR)/"
	@echo ""
	@echo "  Next:  sudo make install-systemd"

.PHONY: install-systemd
install-systemd:
	@echo "==> Installing systemd unit..."
	$(INSTALL_DIR) "$(DESTDIR)$(SYSTEMD_DIR)"
	$(INSTALL_DATA) $(SERVICE_FILE) "$(DESTDIR)$(SYSTEMD_DIR)/angry-box.service"
	@if [ -z "$(DESTDIR)" ]; then \
		systemctl daemon-reload; \
		echo "    Run 'systemctl enable --now angry-box' to start the service"; \
	else \
		echo "    Unit installed to $(DESTDIR)$(SYSTEMD_DIR)/angry-box.service"; \
	fi

.PHONY: install-all
install-all: install install-systemd
	@echo ""
	@echo "  ==> Full install complete."
	@echo "  Start:  systemctl start angry-box"
	@echo "  Status: systemctl status angry-box"
	@echo "  API:    http://localhost:8090/health"

.PHONY: uninstall
uninstall:
	@echo "==> Removing $(BINARY) from $(DESTDIR)$(BINDIR)..."
	rm -f "$(DESTDIR)$(BINDIR)/$(BINARY)"
	@echo "    $(BINARY) removed"
	@echo ""
	@echo "  Config and data directories left intact:"
	@echo "    $(DESTDIR)$(CONFDIR)/"
	@echo "    $(DESTDIR)$(DATADIR)/"
	@echo "  Remove manually if no longer needed:"
	@echo "    rm -rf $(DESTDIR)$(CONFDIR) $(DESTDIR)$(DATADIR)"

.PHONY: uninstall-systemd
uninstall-systemd:
	@echo "==> Removing systemd unit..."
	@if [ -z "$(DESTDIR)" ]; then \
		systemctl stop angry-box 2>/dev/null || true; \
		systemctl disable angry-box 2>/dev/null || true; \
	fi
	rm -f "$(DESTDIR)$(SYSTEMD_DIR)/angry-box.service"
	@if [ -z "$(DESTDIR)" ]; then \
		systemctl daemon-reload; \
	fi
	@echo "    systemd unit removed"

.PHONY: clean
clean:
	@echo "==> Cleaning..."
	rm -f $(BINARY)
	@echo "    $(BINARY) removed"

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test ./...

.PHONY: version
version:
	@echo "version: $(VERSION)"
	@echo "commit:  $(COMMIT)"
	@echo "date:    $(DATE)"

# ──────────────────────────────────────────────────────────────────────────────
# Cross-compilation targets (CGO disabled for simplicity and portability)
# ──────────────────────────────────────────────────────────────────────────────

.PHONY: build-linux-amd64
build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/angry-box-linux-amd64 $(CMD_DIR)

.PHONY: build-linux-arm64
build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/angry-box-linux-arm64 $(CMD_DIR)

.PHONY: build-linux-armv7
build-linux-armv7:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o dist/angry-box-linux-armv7 $(CMD_DIR)

# Keenetic MIPS (mipsel) - uses pure Go mode
.PHONY: build-keenetic-mipsel
build-keenetic-mipsel:
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle go build $(LDFLAGS) -o dist/angry-box-keenetic-mipsel $(CMD_DIR)

.PHONY: build-all
build-all:
	@mkdir -p dist
	$(MAKE) build-linux-amd64
	$(MAKE) build-linux-arm64
	$(MAKE) build-linux-armv7
	$(MAKE) build-keenetic-mipsel
	@echo "==> All cross builds complete in dist/"
