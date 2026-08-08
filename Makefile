.PHONY: generate test build install

generate:
	go generate ./internal/skilldoc

test: generate
	go test ./...

build: generate
	go build -trimpath -o ship-it ./cmd/ship-it

install: build
	./ship-it install
