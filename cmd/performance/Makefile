DIST   := build
BINARY := performance
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: build clean

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp configs/config.example.yaml $(DIST)/config.yaml

clean:
	@go clean -cache
	@rm -rf $(DIST)/
