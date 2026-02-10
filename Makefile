BINARY_NAME = discord-purge
SRC_DIR     = ./src
BIN_DIR     = ./bin

# Default: build for the current platform
.PHONY: all build windows linux darwin clean help

all: windows linux

help:
	@echo ""
	@echo "  discord-purge build targets"
	@echo "  ─────────────────────────────────────────"
	@echo "  make build     Build for current OS/arch"
	@echo "  make windows   Cross-compile for Windows (amd64)"
	@echo "  make linux     Cross-compile for Linux (amd64)"
	@echo "  make darwin    Cross-compile for macOS (amd64)"
	@echo "  make all       Build for Windows + Linux"
	@echo "  make clean     Remove compiled binaries"
	@echo ""

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) $(SRC_DIR)
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

windows:
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME).exe $(SRC_DIR)
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME).exe  (Windows amd64)"

linux:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME) $(SRC_DIR)
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)  (Linux amd64)"

darwin:
	@mkdir -p $(BIN_DIR)
	GOOS=darwin GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY_NAME)-macos $(SRC_DIR)
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)-macos  (macOS amd64)"

clean:
	rm -rf $(BIN_DIR)
	@echo "Cleaned build artifacts."
