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

# Rust 1.97.1; extism-pdk 1.4.1.
build-departures:
    cd plugins/departures && cargo build --locked --release --target wasm32-wasip1
    install -m 0644 plugins/departures/target/wasm32-wasip1/release/eink_departures_widget.wasm internal/widget/departures.wasm

# Launch the interactive tablet Wi-Fi configurator in an ephemeral uv environment.
configure-wifi:
    uv run --no-project --with-requirements tools/requirements-serial.txt tools/configure_tablet_wifi.py
