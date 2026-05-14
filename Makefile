BINARY := ./bin/hygge
PKG    := ./...

.PHONY: build test lint tidy clean run

build:
	go build -o $(BINARY) ./cmd/hygge

test:
	go test $(PKG) -race -count=1

lint:
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -rf ./bin

run: build
	$(BINARY)
