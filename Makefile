BINARY_NAME := kiro-proxy
BUILD_DIR   := build
CMD_DIR     := ./cmd

# 版本信息
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME  := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS     := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

.PHONY: all build run dev clean test fmt vet lint help setup

## 默认目标
all: build

## 编译
build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)"

## 编译（当前平台，不含版本信息，速度更快）
build-fast:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)

## 直接运行（使用默认配置文件）
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

## 开发模式：指定配置文件运行
dev:
	go run $(CMD_DIR) --config config.json --credentials credentials.json

## 指定配置文件运行（已编译的二进制）
start: build
	./$(BUILD_DIR)/$(BINARY_NAME) --config config.json --credentials credentials.json

## 初始化配置文件（从示例复制）
setup:
	@if [ ! -f config.json ]; then \
		cp config.example.json config.json; \
		echo "已创建 config.json，请编辑填入你的配置"; \
	else \
		echo "config.json 已存在，跳过"; \
	fi
	@if [ ! -f credentials.json ]; then \
		cp credentials.example.json credentials.json; \
		echo "已创建 credentials.json，请编辑填入你的凭据"; \
	else \
		echo "credentials.json 已存在，跳过"; \
	fi

## 格式化代码
fmt:
	go fmt ./...

## 静态检查
vet:
	go vet ./...

## 运行测试
test:
	go test ./... -v

## 运行测试（带覆盖率）
test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告: coverage.html"

## 整理依赖
tidy:
	go mod tidy

## 清理构建产物
clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

## 交叉编译：Linux amd64
build-linux:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

## 交叉编译：Windows amd64
build-windows:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

## 交叉编译：macOS arm64
build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_DIR)
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64"

## 编译所有平台
build-all: build-linux build-windows build-darwin-arm64

## 帮助
help:
	@echo "kiro-proxy Makefile"
	@echo ""
	@echo "常用命令:"
	@echo "  make setup          初始化配置文件（从示例复制）"
	@echo "  make dev            开发模式运行（go run，无需预编译）"
	@echo "  make build          编译二进制到 build/"
	@echo "  make start          编译并运行"
	@echo "  make run            同 start"
	@echo "  make fmt            格式化代码"
	@echo "  make vet            静态检查"
	@echo "  make test           运行测试"
	@echo "  make clean          清理构建产物"
	@echo "  make build-all      交叉编译所有平台"
	@echo ""
	@echo "调试技巧:"
	@echo "  make dev            最快启动方式，修改代码后重新执行即可"
	@echo "  go run ./cmd -h     查看命令行参数"
