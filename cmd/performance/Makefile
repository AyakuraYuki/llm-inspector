DIST   := build
BINARY := performance
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: build clean

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp configs/models.example.json $(DIST)/models.json
	@cp configs/tokens.example.json $(DIST)/tokens.json

clean:
	@go clean -cache
	@rm -rf $(DIST)/
