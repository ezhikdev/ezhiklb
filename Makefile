VERSION ?= 2.0.6
DIST := $(CURDIR)/dist

.PHONY: build web clean bundle

build: web
	mkdir -p $(DIST)/bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/bin/ezhiklb ./cmd/ezhiklb
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/bin/ezhiklb-agent ./cmd/ezhiklb-agent
	cp scripts/install.sh $(DIST)/install.sh
	cp scripts/uninstall-node.sh scripts/uninstall-panel.sh $(DIST)/
	chmod +x $(DIST)/install.sh

web:
	cd web && npm ci && npm run build
	mkdir -p $(DIST)/web
	cp -a web/dist/. $(DIST)/web/

bundle: build
	cd $(DIST) && tar -czf ezhiklb_$(VERSION)_linux_amd64.tar.gz bin web install.sh uninstall-node.sh uninstall-panel.sh
	cd $(DIST) && sha256sum ezhiklb_$(VERSION)_linux_amd64.tar.gz > ezhiklb_$(VERSION)_linux_amd64.tar.gz.sha256

clean:
	rm -rf $(DIST) web/dist web/node_modules
