#!/bin/sh
set -eu

APP_NAME="external-ip-monitor"
mkdir -p build

# go.mod pins the toolchain to Go 1.27.2. Go 1.21+ can automatically
# download the requested toolchain when GOTOOLCHAIN=auto (the default).
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

printf 'Using Go toolchain: '
go version

# Build a native executable for the current OS and architecture.
go build -trimpath -ldflags="-s -w" -o "build/${APP_NAME}" .

# Also create a portable Linux amd64 executable for common server deployments.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "build/${APP_NAME}-linux-amd64" .

echo "Built: build/${APP_NAME}"
echo "Built: build/${APP_NAME}-linux-amd64"
