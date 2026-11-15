# KubeZero Makefile
# Builds and manages KubeZero operator and wake-proxy components

.PHONY: all build test vet clean docker-build test-e2e run-proxy-mock run-controller-mock

BIN_DIR := bin
GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')

all: test build

build: $(BIN_DIR)/controller $(BIN_DIR)/wake-proxy

$(BIN_DIR)/controller: $(GO_FILES)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags="-w -s" -o $(BIN_DIR)/controller ./cmd/controller

$(BIN_DIR)/wake-proxy: $(GO_FILES)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags="-w -s" -o $(BIN_DIR)/wake-proxy ./cmd/wake-proxy

test:
	go test -v -race ./...

test-e2e: docker-build
	./hack/test-e2e.sh

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)

docker-build:
	docker build -t kube-zero/controller:latest -f Dockerfile.controller .
	docker build -t kube-zero/wake-proxy:latest -f Dockerfile.proxy .

run-proxy-mock: build
	./$(BIN_DIR)/wake-proxy --mock --port=8080 --metrics-port=9090 --target-url=http://127.0.0.1:3000

run-controller-mock: build
	./$(BIN_DIR)/controller --mock --metrics-port=9091
