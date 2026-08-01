DIST   := build
BINARY := benchmark
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: build clean

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp questions.json $(DIST)/questions.json
	@cp run.sh $(DIST)/run.sh

clean:
	@go clean -cache
	@rm -rf $(DIST)/
