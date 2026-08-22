BINARY_NAME=fdtop

.PHONY: build

build:
	go build -trimpath -o $(BINARY_NAME) .

dev:
	DEBUG=1 go run .

