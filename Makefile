TAGS   := novulkan,nox11
PREFIX ?= $(HOME)/.local

.PHONY: build run install uninstall vet

build:
	go build -tags $(TAGS) -trimpath -ldflags "-s -w" -o bin/omavnc .

run: build
	./bin/omavnc

vet:
	go vet -tags $(TAGS) ./...

install: build
	install -Dm755 bin/omavnc $(PREFIX)/bin/omavnc

uninstall:
	rm -f $(PREFIX)/bin/omavnc
