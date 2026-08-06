DIST   := build
BINARY := benchmark
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: build clean

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp ./configs/questions.json $(DIST)/questions.json
	@cp ./configs/config.example.yml $(DIST)/config.yml

clean:
	@go clean -cache
	@rm -rf $(DIST)/
