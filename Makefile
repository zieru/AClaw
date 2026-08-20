BINARY_NAME=goassistant
CMD_DIR=./cmd/goassistant
DIST_DIR=./dist

.PHONY: all build build-linux-static build-all test clean run

all: build

## Build binary untuk sistem saat ini
build:
	@echo "==> Mengompilasi $(BINARY_NAME)..."
	go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME) $(CMD_DIR)

## Build Static Binary untuk Linux x86_64 (Zero-CGO, 100% manylinux_2_28 / glibc lama compatible)
build-linux-static:
	@echo "==> Mengompilasi Static Binary untuk Linux (manylinux_2_28 / pure Go)..."
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-s -w -extldflags '-static'" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	@echo "✅ Selesai: $(DIST_DIR)/$(BINARY_NAME)-linux-amd64"

## Cross-compile untuk semua arsitektur target
build-all:
	@echo "==> Mengompilasi untuk Linux amd64, arm64, dan Windows amd64..."
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)
	@echo "✅ Seluruh binary berhasil dibuat di folder $(DIST_DIR)/"

## Menjalankan daemon secara lokal
run:
	go run $(CMD_DIR)/main.go -config configs/default_config.yaml

## Menjalankan unit tests
test:
	go test -v ./...

## Membersihkan folder build
clean:
	@rm -rf $(DIST_DIR)
