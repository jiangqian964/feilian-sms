# 飞连短信事件转发通用短信网关 — 构建基线
# 目标：vet / test / race / build-local / build-linux-amd64 / build-linux-arm64 / smoke

BIN_DIR := bin
BIN := $(BIN_DIR)/sms-gateway
MOCK := $(BIN_DIR)/mockvendor
GO := go
GOFLAGS := -trimpath

.PHONY: all vet test race build-local build-linux-amd64 build-linux-arm64 smoke clean tidy

all: vet test build-local

## 静态检查
vet:
	$(GO) vet ./...

## 单元测试
test:
	$(GO) test ./...

## 带竞态检测的测试
race:
	$(GO) test -race ./...

## 本机构建
build-local:
	mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/sms-gateway
	$(GO) build $(GOFLAGS) -o $(MOCK) ./cmd/mockvendor

## Ubuntu x86_64 交叉编译（CGO-free）
build-linux-amd64:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BIN)-linux-amd64 ./cmd/sms-gateway
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(MOCK)-linux-amd64 ./cmd/mockvendor

## Ubuntu arm64 交叉编译（CGO-free）
build-linux-arm64:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BIN)-linux-arm64 ./cmd/sms-gateway
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(MOCK)-linux-arm64 ./cmd/mockvendor

## 全链路冒烟：全新临时目录起 mockvendor + sms-gateway，curl 串联
## 设置→预置→凭证→绑定→challenge→事件→幂等重放→回执→records 全流程
smoke: build-local
	bash deploy/smoke/run.sh

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)
