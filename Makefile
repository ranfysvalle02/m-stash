.PHONY: ui-build build test

ui-build:
	npm --prefix frontend run build

build: ui-build
	go build ./...

test: ui-build
	go test ./...