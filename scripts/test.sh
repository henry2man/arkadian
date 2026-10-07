#!/bin/sh
# Same checks on a laptop, in a container, and in CI.
set -e

for tool in go gofmt hf python3 rsync ssh; do
	command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
rsync --version | grep -q 'version [3-9]\.' || {
	echo "GNU rsync 3 or later is required; on macOS run: brew install rsync" >&2
	exit 1
}

test -z "$(gofmt -l .)" || { echo "run: gofmt -w ." >&2; exit 1; }
go vet ./...
go test ./... -count=1
go build -o bin/ark ./cmd/ark
bin/ark version
