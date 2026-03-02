SHELL=/usr/bin/env bash -o pipefail

NAME      := $(shell basename ${CURDIR})
BUILD_DIR := build

# BUILD_OS is the current host's OS
ifeq ($(OS),Windows_NT)
	BUILD_OS := windows
else
	UNAME_S := $(shell uname -s)
	ifeq (${UNAME_S},Linux)
		BUILD_OS := linux
	endif
	ifeq (${UNAME_S},Darwin)
		BUILD_OS := darwin
	endif
endif

# BUILD_ARCH is the current host's architecture
UNAME_M ?= $(shell uname -m)
ifeq (${UNAME_M},aarch64)
	BUILD_ARCH := arm64
endif
ifeq (${UNAME_M},x86_64)
	BUILD_ARCH := amd64
endif


LINUX_AMD64   := ${BUILD_DIR}/${NAME}-linux-amd64
LINUX_ARM64   := ${BUILD_DIR}/${NAME}-linux-arm64
DARWIN_AMD64  := ${BUILD_DIR}/${NAME}-darwin-amd64
DARWIN_ARM64  := ${BUILD_DIR}/${NAME}-darwin-arm64
WINDOWS_AMD64 := ${BUILD_DIR}/${NAME}-windows-amd64.exe

LINUX   := ${LINUX_AMD64} ${LINUX_ARM64}
DARWIN  := ${DARWIN_AMD64} $(DARWIN_ARM64)
WINDOWS := ${WINDOWS_AMD64}

GO_FILES := $(shell find * -type f '(' -name '*.go' -o -name go.mod -o -name go.sum ')' -a '!' -name '*_test.go' | sort)

VERSION  := $(shell git describe --tags --always --dirty)
LDFLAGS  := -ldflags="-s -w -X main.version=${VERSION}"


.PHONY: info
info:
	@echo "NAME             = $(NAME)"
	@echo "VERSION          = $(VERSION)"
	@echo "uname -s         = ${UNAME_S}"
	@echo "uname -m         = ${UNAME_M}"
	@echo

	@echo "BUILD_OS         = $(BUILD_OS)"
	@echo "BUILD_ARCH       = $(BUILD_ARCH)"
	@echo

	@echo "LINUX_AMD64      = ${LINUX_AMD64}"
	@echo "LINUX_ARM64      = ${LINUX_ARM64}"
	@echo "DARWIN_AMD64     = ${DARWIN_AMD64}"
	@echo "DARWIN_ARM64     = $(DARWIN_ARM64)"
	@echo "WINDOWS_AMD64    = ${WINDOWS_AMD64}"
	@echo

	@echo "GO_FILES         = ${GO_FILES}"
	@echo

.PHONY: install
install:
	go install ${LDFLAGS}

.PHONY: all
all: build

.PHONY: build.all
build.all: linux darwin windows

.PHONY: clean
clean:
	-rm -rf ${BUILD_DIR} ${NAME}

.PHONY: linux
linux: ${LINUX}

.PHONY: darwin
darwin: ${DARWIN}

.PHONY: windows
windows: ${WINDOWS}

.PHONY: build
build: ${GO_FILES}
	@$(MAKE) compile file=${NAME} GOOS=${BUILD_OS} GOARCH=${BUILD_ARCH}

${LINUX_AMD64}: ${GO_FILES}
	@$(MAKE) compile file=$@ GOOS=linux GOARCH=amd64

${LINUX_ARM64}: ${GO_FILES}
	@$(MAKE) compile file=$@ GOOS=linux GOARCH=arm64

${DARWIN_AMD64}: ${GO_FILES}
	@$(MAKE) compile file=$@ GOOS=darwin GOARCH=amd64

$(DARWIN_ARM64): ${GO_FILES}
	@$(MAKE) compile file=$@ GOOS=darwin GOARCH=arm64

${WINDOWS_AMD64}: ${GO_FILES}
	@$(MAKE) compile file=$@ GOOS=windows GOARCH=amd64

.PHONY: compile
compile:
ifndef file
	$(error "file" variable is not defined)
endif
	go build -o ${file} ${LDFLAGS} main.go

.PHONY: tests
tests:
	go test -race ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: format
format:
	go fmt ./...
