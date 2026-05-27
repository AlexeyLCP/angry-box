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

INSTALL      := install
INSTALL_BIN  := $(INSTALL) -m 755
INSTALL_DATA := $(INSTALL) -m 644
INSTALL_DIR  := $(INSTALL) -d -m 755

.PHONY: build
build:
	@echo "==> Building $(BINARY)..."
	go build -o $(BINARY) $(CMD_DIR)
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
