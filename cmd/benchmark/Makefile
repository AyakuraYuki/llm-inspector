DIST   := build
BINARY := benchmark
GOOS   ?= darwin
GOARCH ?= amd64

.PHONY: setup build clean

setup:
	@mkdir -p ./internal/dataset/hf/math-ai/aime25
	@hf download math-ai/aime25 --repo-type dataset --local-dir ./internal/dataset/hf/math-ai/aime25/
	@mkdir -p ./internal/dataset/hf/math-ai/aime26
	@hf download math-ai/aime26 --repo-type dataset --local-dir ./internal/dataset/hf/math-ai/aime26/
	@mkdir -p ./internal/dataset/hf/TIGER-Lab/MMLU-Pro
	@hf download TIGER-Lab/MMLU-Pro --repo-type dataset --local-dir ./internal/dataset/hf/TIGER-Lab/MMLU-Pro/

build:
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-s -w" -o $(DIST)/$(BINARY)-$(GOOS)_$(GOARCH) .
	@cp ./configs/config.example.yml $(DIST)/config.yml

clean:
	@go clean -cache
	@rm -rf $(DIST)/
