#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
python3 scripts/prepare-check.py
unformatted="$(gofmt -l main.go internal)"
if [ -n "$unformatted" ]; then
  printf '%s\n' "$unformatted"
  exit 1
fi
go vet ./...
go tool mygo vet .
CGO_ENABLED=0 go test ./... -count=1
go test -race ./internal/app/... ./internal/runtime/... ./internal/storage/... ./internal/update/... ./internal/logging/... ./internal/ui/...
