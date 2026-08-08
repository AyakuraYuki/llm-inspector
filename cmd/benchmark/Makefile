DIST   := build
BINARY := benchmark
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: build clean

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp ./configs/aime25.json $(DIST)/aime25.json
	@cp ./configs/aime26.json $(DIST)/aime26.json
	@cp ./configs/config.example.yml $(DIST)/config.yml

clean:
	@go clean -cache
	@rm -rf $(DIST)/
