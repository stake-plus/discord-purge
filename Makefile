BINARY_NAME ?= discord-purge
SRC_DIR     ?= ./src
BIN_DIR     ?= ./bin

WIN_OUT    := $(BIN_DIR)/$(BINARY_NAME).exe
LINUX_OUT  := $(BIN_DIR)/$(BINARY_NAME)
DARWIN_OUT := $(BIN_DIR)/$(BINARY_NAME)-macos

# Default: build for the current platform
.PHONY: all build windows win linux darwin mac clean help

ifeq ($(OS),Windows_NT)
MKDIR_BIN       := if not exist "$(BIN_DIR)" mkdir "$(BIN_DIR)"
RM_BIN          := if exist "$(BIN_DIR)" rmdir /S /Q "$(BIN_DIR)"
GO_BUILD_NATIVE := go build -o $(WIN_OUT) $(SRC_DIR)
GO_BUILD_WIN    := set "GOOS=windows" && set "GOARCH=amd64" && go build -o $(WIN_OUT) $(SRC_DIR)
GO_BUILD_LINUX  := set "GOOS=linux" && set "GOARCH=amd64" && go build -o $(LINUX_OUT) $(SRC_DIR)
GO_BUILD_DARWIN := set "GOOS=darwin" && set "GOARCH=amd64" && go build -o $(DARWIN_OUT) $(SRC_DIR)
NATIVE_OUT      := $(WIN_OUT)
else
MKDIR_BIN       := mkdir -p $(BIN_DIR)
RM_BIN          := rm -rf $(BIN_DIR)
GO_BUILD_NATIVE := go build -o $(LINUX_OUT) $(SRC_DIR)
GO_BUILD_WIN    := GOOS=windows GOARCH=amd64 go build -o $(WIN_OUT) $(SRC_DIR)
GO_BUILD_LINUX  := GOOS=linux GOARCH=amd64 go build -o $(LINUX_OUT) $(SRC_DIR)
GO_BUILD_DARWIN := GOOS=darwin GOARCH=amd64 go build -o $(DARWIN_OUT) $(SRC_DIR)
NATIVE_OUT      := $(LINUX_OUT)
endif

all: windows linux

help:
	@echo ""
	@echo "  discord-purge build targets"
	@echo "  -----------------------------------------"
	@echo "  make build     Build for current OS/arch"
	@echo "  make windows   Cross-compile for Windows (amd64)"
	@echo "  make linux     Cross-compile for Linux (amd64)"
	@echo "  make darwin    Cross-compile for macOS (amd64)"
	@echo "  make all       Build for Windows + Linux"
	@echo "  make clean     Remove compiled binaries"
	@echo ""

build:
	@$(MKDIR_BIN)
	$(GO_BUILD_NATIVE)
	@echo Built: $(NATIVE_OUT)

windows:
	@$(MKDIR_BIN)
	$(GO_BUILD_WIN)
	@echo Built: $(WIN_OUT)  (Windows amd64)

win: windows

linux:
	@$(MKDIR_BIN)
	$(GO_BUILD_LINUX)
	@echo Built: $(LINUX_OUT)  (Linux amd64)

darwin:
	@$(MKDIR_BIN)
	$(GO_BUILD_DARWIN)
	@echo Built: $(DARWIN_OUT)  (macOS amd64)

mac: darwin

clean:
	@$(RM_BIN)
	@echo Cleaned build artifacts.
