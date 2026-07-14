APP      := buscalogo-agent
PKG      := buscalogo-agent
GO       := go
VERSION  ?= $(shell tr -d ' \n' < VERSION 2>/dev/null || echo 0.1.0)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X buscalogo-agent/internal/version.Version=$(VERSION) -X buscalogo-agent/internal/version.Commit=$(COMMIT)

# IPv6 Ygg do registry público (opcional).
# Override: make REGISTRY_YGG_IP=205:... build
# Ou grave uma linha em ../registy/ygg.ip
REGISTRY_DIR ?= ../registy
REGISTRY_YGG_IP ?= $(shell \
  if [ -f "$(REGISTRY_DIR)/ygg.ip" ]; then head -1 "$(REGISTRY_DIR)/ygg.ip" | tr -d '[] \n'; \
  elif [ -f "$(REGISTRY_DIR)/data/data/registry-bootstrap.txt" ]; then grep -E '^YGG_IP=' "$(REGISTRY_DIR)/data/data/registry-bootstrap.txt" | head -1 | cut -d= -f2- | tr -d '[] \n'; \
  elif [ -f "$(REGISTRY_DIR)/data/registry-bootstrap.txt" ]; then grep -E '^YGG_IP=' "$(REGISTRY_DIR)/data/registry-bootstrap.txt" | head -1 | cut -d= -f2- | tr -d '[] \n'; \
  else echo ""; fi)
ifneq ($(strip $(REGISTRY_YGG_IP)),)
LDFLAGS  += -X buscalogo-agent/internal/config.DefaultRegistryYggIP=$(REGISTRY_YGG_IP)
endif

YGG_VERSION := 0.5.14
DNS_VERSION := 1.14.4
COUCH_VERSION := 3.5.2
# CouchDB sempre ~jammy (glibc 2.35): roda em Ubuntu 22.04/24.04/26.04 e Debian 12+.
# Binário compilado contra glibc antiga executa em sistemas com glibc mais nova.
DEB_CODENAME ?= jammy
COUCH_DEB_URL := https://apache.jfrog.io/artifactory/couchdb-deb/pool/C/CouchDB/couchdb_$(COUCH_VERSION)~$(DEB_CODENAME)_amd64.deb

ASSETS_DIR := assets/linux

.PHONY: all build build-windows build-agent-registry assets assets-couchdb run test vet fmt clean tidy dist deb release desktop desktop-icons desktop-neutralino desktop-run desktop-build desktop-build-windows msi-stage msi

all: build

build:
	@if [ -n "$(REGISTRY_YGG_IP)" ]; then echo ">> registry seed: $(REGISTRY_YGG_IP)"; else echo ">> registry seed: (nenhum — use ../registy/ygg.ip)"; fi
	$(GO) build -ldflags "$(LDFLAGS)" -o $(APP) ./cmd/agent

# Cross-compile Windows amd64 (não embute CouchDB). Baixa assets/windows se faltarem.
build-windows:
	@mkdir -p dist assets/windows
	@if [ ! -s assets/windows/coredns.exe ] || [ ! -s assets/windows/yggdrasil.exe ] || [ ! -s assets/windows/wintun.dll ]; then \
		echo ">> assets/windows incompletos — rodando scripts/fetch-windows-assets.sh"; \
		chmod +x scripts/fetch-windows-assets.sh; \
		./scripts/fetch-windows-assets.sh; \
	fi
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o dist/$(APP).exe ./cmd/agent
	@echo ">> Windows: dist/$(APP).exe"

# Atalho: Agent + seed do registry (via registy/scripts)
build-agent-registry:
	@$(REGISTRY_DIR)/scripts/build-agent.sh

run: build
	./$(APP)

dist: build
	$(require-extensions)
	@rm -rf dist/$(APP)
	@mkdir -p dist/$(APP)
	@cp $(APP) dist/$(APP)/
	@cp -R www dist/$(APP)/
	@cp -R sites dist/$(APP)/
	@mkdir -p dist/$(APP)/extension
	@cp -a "$(EXTEN_DIR)/chrome" dist/$(APP)/extension/chrome
	@cp -a "$(EXTEN_DIR)/firefox" dist/$(APP)/extension/firefox
	@cp assets/icons/logo.png dist/$(APP)/buscalogo-agent.png
	@cp dist/install.sh dist/$(APP)/install.sh
	@chmod +x dist/$(APP)/install.sh
	@cd dist && tar -czf $(APP)-linux-amd64.tar.gz $(APP)
	@echo ">> Distribuição portátil: dist/$(APP)-linux-amd64.tar.gz"

deb: build desktop-icons desktop-neutralino
	$(require-extensions)
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
	@mkdir -p dist/deb/opt/buscalogo/extension
	@cp -a "$(EXTEN_DIR)/chrome" dist/deb/opt/buscalogo/extension/chrome
	@cp -a "$(EXTEN_DIR)/firefox" dist/deb/opt/buscalogo/extension/firefox
	@command -v zip >/dev/null && (cd dist/deb/opt/buscalogo/extension && zip -qr chrome.zip chrome && zip -qr firefox.zip firefox) || true
	@test -f dist/deb/opt/buscalogo/extension/chrome/manifest.json
	@test -f dist/deb/opt/buscalogo/extension/firefox/manifest.json
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
	@echo ">> Extensões no pacote:"
	@dpkg-deb -c dist/$(APP)_$(VERSION)_amd64.deb | grep -E 'extension/.*/manifest.json' || (echo ">> ERRO: manifests ausentes no .deb"; exit 1)

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

# Publica release: make release BUMP=patch|minor|major|0.1.4
BUMP ?= patch
release:
	@chmod +x scripts/release.sh
	@./scripts/release.sh $(BUMP)

clean:
	rm -f $(APP)
	rm -rf data/bin/* data/cache/* data/logs/*

DESKTOP_DIR := desktop/buscalogo-desktop
DESKTOP_ICONS := $(DESKTOP_DIR)/resources/icons
LOGO_SRC := assets/icons/logo.png
DAEMON_BIN := buscalogo-agentd
# Extensões embutidas no repo (caminho absoluto — CI e builds fora do cwd).
# Fallback: sibling ../exten em desenvolvimento local.
MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
EXTEN_DIR := $(if $(wildcard $(MAKEFILE_DIR)extension/chrome/manifest.json),$(MAKEFILE_DIR)extension,$(abspath $(MAKEFILE_DIR)../exten))

# Garante que chrome/firefox existam antes de empacotar.
define require-extensions
	@if [ ! -f "$(EXTEN_DIR)/chrome/manifest.json" ]; then \
		echo ">> Erro: extensão Chrome não encontrada em $(EXTEN_DIR)/chrome"; \
		echo ">> Esperado: extension/chrome no repo (ou ../exten/chrome)"; \
		exit 1; \
	fi
	@if [ ! -f "$(EXTEN_DIR)/firefox/manifest.json" ]; then \
		echo ">> Erro: extensão Firefox não encontrada em $(EXTEN_DIR)/firefox"; \
		exit 1; \
	fi
	@echo ">> Extensões: $(EXTEN_DIR)/chrome + $(EXTEN_DIR)/firefox"
endef

desktop-icons:
	@mkdir -p $(DESKTOP_ICONS)
	@command -v convert >/dev/null || { echo ">> Instale imagemagick (convert) para gerar ícones"; exit 1; }
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -resize 200x200 -strip $(DESKTOP_ICONS)/appIcon.png
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -filter Lanczos -resize 24x24 -strip $(DESKTOP_ICONS)/trayIcon.png
	convert $(LOGO_SRC) -background none -gravity center -crop 416x416+0+0 +repage -resize 128x128 -define png:color-type=6 -strip $(DESKTOP_ICONS)/systrayIcon.png
	@cp -f $(DESKTOP_ICONS)/systrayIcon.png $(DESKTOP_DIR)/trayIcon.png
	@echo ">> Ícones desktop gerados (appIcon 200px, trayIcon 24px, systrayIcon 128px PNG32)"

# neutralino.js não vai ao git (.gitignore) — neu update baixa client + binários.
desktop-neutralino:
	@command -v neu >/dev/null || { echo ">> Erro: neu CLI não encontrado (npm i -g @neutralinojs/neu)"; exit 1; }
	@if [ -f $(DESKTOP_DIR)/resources/js/neutralino.js ] && [ -f $(DESKTOP_DIR)/bin/neutralino-linux_x64 ]; then \
		echo ">> Neutralino já presente (skip neu update)"; \
	else \
		echo ">> Neutralino client e binários (neu update)"; \
		cd $(DESKTOP_DIR) && neu update; \
	fi

# App desktop Neutralinojs (requer `neu` global: npm i -g @neutralinojs/neu)
desktop-run: build desktop-icons desktop-neutralino
	cp -f $(APP) $(DESKTOP_DIR)/$(DAEMON_BIN)
	cd $(DESKTOP_DIR) && neu run

desktop-build: build desktop-icons desktop-neutralino
	cp -f $(APP) $(DESKTOP_DIR)/$(DAEMON_BIN)
	cd $(DESKTOP_DIR) && neu build --release
	@cp -f $(APP) $(DESKTOP_DIR)/dist/buscalogo-agent/
	@cp -f $(DESKTOP_DIR)/resources/icons/systrayIcon.png $(DESKTOP_DIR)/dist/buscalogo-agent/trayIcon.png
	@echo ">> Desktop: $(DESKTOP_DIR)/dist/buscalogo-agent/"

# Pacote Neutralino + Agent Windows (cross-compile Linux → win_x64).
# Requer neu (npm i -g @neutralinojs/neu) e assets/windows (make build-windows).
desktop-build-windows: build-windows desktop-icons desktop-neutralino
	@cp -f dist/$(APP).exe $(DESKTOP_DIR)/$(DAEMON_BIN).exe
	@cd $(DESKTOP_DIR) && neu build --release
	@mkdir -p dist/buscalogo-agent-win
	@cp -f $(DESKTOP_DIR)/dist/buscalogo-agent/buscalogo-agent-win_x64.exe dist/buscalogo-agent-win/buscalogo-agent.exe
	@cp -f $(DESKTOP_DIR)/dist/buscalogo-agent/resources.neu dist/buscalogo-agent-win/
	@cp -f dist/$(APP).exe dist/buscalogo-agent-win/$(DAEMON_BIN).exe
	@cp -f $(DESKTOP_DIR)/resources/icons/systrayIcon.png dist/buscalogo-agent-win/trayIcon.png
	@cp -f assets/windows/wintun.dll dist/buscalogo-agent-win/wintun.dll 2>/dev/null || true
	@cd dist && zip -qr buscalogo-agent-win-amd64.zip buscalogo-agent-win
	@echo ">> Desktop Windows: dist/buscalogo-agent-win/ e dist/buscalogo-agent-win-amd64.zip"
	@echo ">> No PC: rode buscalogo-agent.exe (Neutralino) como Admin na 1ª vez"

# Staging para MSI (WiX). O .msi em si só é gerado no Windows: packaging/windows/build.ps1
msi-stage: desktop-build-windows
	@rm -rf dist/msi-stage
	@mkdir -p dist/msi-stage
	@cp -f dist/buscalogo-agent-win/buscalogo-agentd.exe dist/msi-stage/
	@cp -f dist/buscalogo-agent-win/buscalogo-agent.exe dist/msi-stage/
	@cp -f dist/buscalogo-agent-win/resources.neu dist/msi-stage/
	@cp -f dist/buscalogo-agent-win/trayIcon.png dist/msi-stage/
	@cp -f dist/buscalogo-agent-win/wintun.dll dist/msi-stage/
	@echo ">> MSI stage: dist/msi-stage/"
	@echo ">> No Windows: .\\packaging\\windows\\build.ps1"

msi:
	@echo "O MSI é gerado no Windows com WiX Toolset v4/v5."
	@echo "  1) make msi-stage"
	@echo "  2) copie dist/msi-stage + packaging/windows para o PC Windows"
	@echo "  3) .\\packaging\\windows\\build.ps1"
	@echo "Ver packaging/windows/README.md"
