DEV_BIN_NAME ?= tf-mankeli
DEV_PATH ?= $(HOME)/tools/bin

.PHONY: dev build-dev tf-test

dev:
	go run cmd/main.go

build-dev:
	go build -ldflags="-s -w" -o "$(DEV_PATH)/$(DEV_BIN_NAME)" cmd/main.go

tf-test:
	go run ./cmd tf-test
