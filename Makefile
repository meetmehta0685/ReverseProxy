.PHONY: build test vet fmt check

build:
	go build -trimpath -o bin/reverse-proxy ./cmd/proxy

test:
	go test -race -cover ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

check: test vet
	test -z "$$(gofmt -l cmd internal)"
