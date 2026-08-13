set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

binary := "eink-server"
package := "./cmd/eink-server"
dist := "dist"

# List available recipes.
default:
    @just --list

# Build for the current operating system and architecture.
build:
    mkdir -p {{dist}}
    go build -buildvcs=false -trimpath -o {{dist}}/{{binary}} {{package}}

# Build a static Linux AMD64 binary.
build-linux-amd64:
    mkdir -p {{dist}}
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o {{dist}}/{{binary}}-linux-amd64 {{package}}

# Build a static Linux ARM64 binary.
build-linux-arm64:
    mkdir -p {{dist}}
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -o {{dist}}/{{binary}}-linux-arm64 {{package}}

# Build all supported Linux release binaries.
build-linux: build-linux-amd64 build-linux-arm64

# Run the complete test suite.
test:
    go test ./...
