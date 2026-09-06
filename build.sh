#!/bin/sh
# Cross-compile for the Miyoo Mini Plus and stage the OnionOS app folder.
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
cd "$root"

go vet ./...
go test ./...

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
    go build -buildvcs=false -trimpath -mod=readonly \
    -ldflags='-s -w -buildid=' \
    -o App/Antenna/antenna .

chmod 0755 App/Antenna/antenna App/Antenna/launch.sh
echo "Built App/Antenna/antenna"
file App/Antenna/antenna 2>/dev/null || true
