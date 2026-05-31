BIN := bin/reflet

.PHONY: build
build:
	go build -o $(BIN) ./cmd/reflet
