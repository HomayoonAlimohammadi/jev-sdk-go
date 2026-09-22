.PHONY: build vet fmt lint test race cover vulncheck all

all: fmt vet test

build:
	go build ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run gofmt -w ." && exit 1)

lint:
	golangci-lint run

test:
	go test ./... -race

cover:
	go test ./... -race -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

integration:
	go test ./... -tags=integration -run Integration -v

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
