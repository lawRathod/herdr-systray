BINARY   := herdr-systray
GO       := CGO_ENABLED=1 go
BUILDDIR := build

.PHONY: all build clean install run test vet lint integration-test

all: clean build test vet

$(BINARY): *.go icons/*.go go.mod go.sum
	$(GO) build -o $(BINARY) .

build: $(BINARY)

$(BUILDDIR):
	mkdir -p $(BUILDDIR)

install: $(BINARY)
	install -m 755 $(BINARY) $(GOPATH)/bin/$(BINARY)

run: build
	./$(BINARY)

test:
	$(GO) test -count=1 ./...

vet:
	$(GO) vet ./...

lint:
	command -v golangci-lint >/dev/null 2>&1 || (echo "install golangci-lint first"; exit 1)
	golangci-lint run

integration-test:
	bash test_integration.sh

clean:
	rm -f $(BINARY)
	rm -rf $(BUILDDIR)
