APP      := buscalogo-agent
PKG      := buscalogo-agent
GO       := go
VERSION  ?= 0.1.0
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X buscalogo-agent/internal/version.Version=$(VERSION) -X buscalogo-agent/internal/version.Commit=$(COMMIT)

YGG_VERSION := 0.5.14
DNS_VERSION := 1.14.4
COUCH_VERSION := 3.5.2
# Codename Debian/Ubuntu (bookworm, noble, jammy, …) — detecta do sistema.
DEB_CODENAME := $(shell . /etc/os-release 2>/dev/null && echo $${VERSION_CODENAME:-bookworm})
COUCH_DEB_URL := https://apache.jfrog.io/artifactory/couchdb-deb/pool/C/CouchDB/couchdb_$(COUCH_VERSION)~$(DEB_CODENAME)_amd64.deb

ASSETS_DIR := assets/linux

.PHONY: all build assets assets-couchdb run test vet fmt clean tidy dist deb desktop desktop-icons desktop-run desktop-build

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(APP) ./cmd/agent

run: build
	./$(APP)

dist: build
	@rm -rf dist/$(APP)
	@mkdir -p dist/$(APP)
	@cp $(APP) dist/$(APP)/
	@cp -R www dist/$(APP)/
	@cp -R sites dist/$(APP)/
	@cp assets/icons/logo.png dist/$(APP)/buscalogo-agent.png
	@cp dist/install.sh dist/$(APP)/install.sh
	@chmod +x dist/$(APP)/install.sh
	@cd dist && tar -czf $(APP)-linux-amd64.tar.gz $(APP)
	@echo ">> Distribuição portátil: dist/$(APP)-linux-amd64.tar.gz"

deb: build desktop-icons
	@command -v neu >/dev/null || { echo ">> Erro: neu CLI não encontrado (npm i -g @neutralinojs/neu)"; exit 1; }
	@cp -f $(DESKTOP_DIR)/neutralino.config.json /tmp/buscalogo-neutralino.config.bak
	@sed 's/"version": "[^"]*"/"version": "$(VERSION)"/' $(DESKTOP_DIR)/neutralino.config.json > /tmp/buscalogo-neutralino.config.json
	@mv /tmp/buscalogo-neutralino.config.json $(DESKTOP_DIR)/neutralino.config.json
	@cp -f $(APP) $(DESKTOP_DIR)/$(DAEMON_BIN)
	@cd $(DESKTOP_DIR) && neu build --release
	@mv -f /tmp/buscalogo-neutralino.config.bak $(DESKTOP_DIR)/neutralino.config.json
	@rm -rf dist/deb
	@mkdir -p dist/deb/DEBIAN dist/deb/opt/buscalogo/data/bin dist/deb/usr/local/bin dist/deb/usr/share/applications dist/deb/etc/xdg/autostart
	@cp $(DESKTOP_DIR)/dist/buscalogo-agent/buscalogo-agent-linux_x64 dist/deb/opt/buscalogo/buscalogo-agent
	@cp $(APP) dist/deb/opt/buscalogo/$(DAEMON_BIN)
	@cp $(DESKTOP_DIR)/dist/buscalogo-agent/resources.neu dist/deb/opt/buscalogo/
	@cp $(DESKTOP_DIR)/resources/icons/systrayIcon.png dist/deb/opt/buscalogo/trayIcon.png
	@cp dist/buscalogo-agent-launch.sh dist/deb/opt/buscalogo/launch.sh
	@cp dist/update-install.sh dist/deb/opt/buscalogo/update-install.sh
	@chmod 755 dist/deb/opt/buscalogo/update-install.sh
	@cp -R www dist/deb/opt/buscalogo/
	@cp -R sites dist/deb/opt/buscalogo/
	@cp assets/icons/logo.png dist/deb/opt/buscalogo/buscalogo-agent.png
	@cp assets/linux/coredns dist/deb/opt/buscalogo/data/bin/coredns
	@cp assets/linux/yggdrasil dist/deb/opt/buscalogo/data/bin/yggdrasil
	@if [ -d assets/linux/couchdb/bin ]; then \
		rm -rf dist/deb/opt/buscalogo/data/bin/couchdb; \
		cp -a assets/linux/couchdb dist/deb/opt/buscalogo/data/bin/couchdb; \
	fi
	@chmod 755 dist/deb/opt/buscalogo/buscalogo-agent dist/deb/opt/buscalogo/$(DAEMON_BIN) dist/deb/opt/buscalogo/launch.sh
	@ln -sf /opt/buscalogo/launch.sh dist/deb/usr/local/bin/buscalogo-agent
	@cp dist/buscalogo-agent.desktop dist/deb/usr/share/applications/buscalogo-agent.desktop
	@cp dist/buscalogo-agent.desktop dist/deb/etc/xdg/autostart/buscalogo-agent.desktop
	@sed 's/^Version: .*/Version: $(VERSION)/' dist/control > dist/deb/DEBIAN/control
	@cp dist/postinst dist/deb/DEBIAN/postinst
	@cp dist/postrm dist/deb/DEBIAN/postrm
	@chmod +x dist/deb/DEBIAN/postinst dist/deb/DEBIAN/postrm
	@cd dist && fakeroot dpkg-deb --build deb $(APP)_$(VERSION)_amd64.deb
	@echo ">> Pacote .deb: dist/$(APP)_$(VERSION)_amd64.deb"

assets:
	@echo ">> Baixando binários para $(ASSETS_DIR)/"
	@mkdir -p $(ASSETS_DIR)
	@echo "   yggdrasil v$(YGG_VERSION) (via .deb)"
	@curl -sL -o /tmp/ygg.deb https://github.com/yggdrasil-network/yggdrasil-go/releases/download/v$(YGG_VERSION)/yggdrasil-$(YGG_VERSION)-amd64.deb
	@rm -rf /tmp/ygg_extract && dpkg-deb -x /tmp/ygg.deb /tmp/ygg_extract
	@cp -f /tmp/ygg_extract/usr/bin/yggdrasil $(ASSETS_DIR)/yggdrasil
	@echo "   coredns v$(DNS_VERSION)"
	@curl -sL -o /tmp/coredns.tgz https://github.com/coredns/coredns/releases/download/v$(DNS_VERSION)/coredns_$(DNS_VERSION)_linux_amd64.tgz
	@tar -xzf /tmp/coredns.tgz -C $(ASSETS_DIR) coredns
	@chmod +x $(ASSETS_DIR)/yggdrasil $(ASSETS_DIR)/coredns
	@echo ">> Concluído:"
	@ls -lh $(ASSETS_DIR)/yggdrasil $(ASSETS_DIR)/coredns

assets-couchdb:
	@echo ">> Baixando CouchDB v$(COUCH_VERSION) (~$(DEB_CODENAME)) para $(ASSETS_DIR)/couchdb/"
	@mkdir -p $(ASSETS_DIR)/couchdb
	@echo "   URL: $(COUCH_DEB_URL)"
	@curl -fsSL -o /tmp/couch.deb "$(COUCH_DEB_URL)"
	@rm -rf /tmp/couch_extract && dpkg-deb -x /tmp/couch.deb /tmp/couch_extract
	@rm -rf $(ASSETS_DIR)/couchdb && mkdir -p $(ASSETS_DIR)/couchdb
	@cp -a /tmp/couch_extract/opt/couchdb/. $(ASSETS_DIR)/couchdb/
	@echo "COUCHDB_VERSION=$(COUCH_VERSION)" > $(ASSETS_DIR)/couchdb/MANIFEST
	@echo "COUCHDB_CODENAME=$(DEB_CODENAME)" >> $(ASSETS_DIR)/couchdb/MANIFEST
	@chmod -R a+rX $(ASSETS_DIR)/couchdb
	@echo ">> CouchDB: $$(du -sh $(ASSETS_DIR)/couchdb | cut -f1)"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -s -w .

tidy:
	$(GO) mod tidy

clean:
	rm -f $(APP)
	rm -rf data/bin/* data/cache/* data/logs/*

DESKTOP_DIR := desktop/buscalogo-desktop
DESKTOP_ICONS := $(DESKTOP_DIR)/resources/icons
LOGO_SRC := assets/icons/logo.png
DAEMON_BIN := buscalogo-agentd

desktop-icons:
	@mkdir -p $(DESKTOP_ICONS)
	@command -v convert >/dev/null || { echo ">> Instale imagemagick (convert) para gerar ícones"; exit 1; }
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -resize 200x200 -strip $(DESKTOP_ICONS)/appIcon.png
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -filter Lanczos -resize 24x24 -strip $(DESKTOP_ICONS)/trayIcon.png
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -resize 128x128 -define png:color-type=6 -strip $(DESKTOP_ICONS)/systrayIcon.png
	@cp -f $(DESKTOP_ICONS)/systrayIcon.png $(DESKTOP_DIR)/trayIcon.png
	@echo ">> Ícones desktop gerados (appIcon 200px, trayIcon 24px, systrayIcon 128px PNG32)"

# App desktop Neutralinojs (requer `neu` global: npm i -g @neutralinojs/neu)
desktop-run: build desktop-icons
	cp -f $(APP) $(DESKTOP_DIR)/$(DAEMON_BIN)
	cd $(DESKTOP_DIR) && neu run

desktop-build: build desktop-icons
	cp -f $(APP) $(DESKTOP_DIR)/$(DAEMON_BIN)
	cd $(DESKTOP_DIR) && neu build --release
	@cp -f $(APP) $(DESKTOP_DIR)/dist/buscalogo-agent/
	@cp -f $(DESKTOP_DIR)/resources/icons/systrayIcon.png $(DESKTOP_DIR)/dist/buscalogo-agent/trayIcon.png
	@echo ">> Desktop: $(DESKTOP_DIR)/dist/buscalogo-agent/"
