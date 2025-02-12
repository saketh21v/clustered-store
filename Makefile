VERSION := $(shell git describe --tags --always --dirty)
OUTPUT_DIR := .build
PLATFORMS := linux/amd64,linux/arm64
DOCKER_IMAGE_PRE := store
BINS ?= main

# Go build settings
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOFLAGS := -ldflags "-X main.Version=$(VERSION) -s -w"

SHELL := /usr/bin/env bash -o errexit -o pipefail -o nounset

.PHONY: all build clean docker docker-multiarch help proto

all: clean build container

## build: Build Go binary
build:
	@mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) @go build $(GOFLAGS) -o $(OUTPUT_DIR)/$(APP_NAME) .


## run-(bin): Runs the specified binary
run-%:
	@go run "cmd/$*/main.go"


## clean: Clean build artifacts
clean:
	@rm -rf $(OUTPUT_DIR)


## image: Build Docker image (default architecture)
container:
	@for bin in $(BINS); do                                   \
		IMAGE="$(DOCKER_IMAGE_PRE)_$$bin:$(VERSION)"; \
		echo "Using tag $$IMAGE"; \
		docker buildx build --load \
		--build-arg BIN=$$bin \
		-t $$IMAGE . ; \
		docker tag $$IMAGE $(DOCKER_IMAGE_PRE)_$$bin:latest; \
		done
	@for bin in $(BINS); do                                   \
		echo "Generated image $(DOCKER_IMAGE_PRE)_$$bin:$(GOOS)_$(GOARCH)_$(VERSION)"; \
		done

## container-multiarch: Build and push multi-arch Docker image using buildx
container-multiarch:
	@for bin in $(BINS); do 																						\
		docker buildx build \
		--build-arg BIN=$$bin 	\
		--platform $(PLATFORMS) \
		-t $(DOCKER_IMAGE_PRE)_$$bin:$(VERSION) . ; \
		done

## help: Show this help
help:
	@echo "Usage:\tGOOS=[os] GOARCH=[arch] make [target]\n\tGOOS,GOOARCH are optional"
	@echo ""
	@echo "Available targets:"
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/##//g' | column -s ':' -t


