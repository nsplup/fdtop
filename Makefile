BINARY_NAME=fdtop

.PHONY: build

build:
	go build -o $(BINARY_NAME) .

dev:
	DEBUG=1 go run .

