# Development and testing

## Fedora Toolbox

Development uses the default Fedora Toolbox. The current environment has
`fedora-toolbox-44` with Go 1.26 installed.

```sh
toolbox enter
cd /var/home/gyger/Projects/JoanTablet/server
go test ./...
```

Useful verification commands:

```sh
go test -race ./...
go vet ./...
go build -o /tmp/eink-server ./cmd/joan-server
```

Run locally with isolated ports and state:

```sh
go run ./cmd/joan-server \
  --device-listen=127.0.0.1:11114 \
  --http-listen=127.0.0.1:18080 \
  --database=/tmp/eink-server.db
```

## Dependencies

- `github.com/pierrec/lz4/v4` provides pure-Go raw LZ4 block compression.
- `modernc.org/sqlite` provides a CGO-free `database/sql` SQLite driver.
- `github.com/tdewolff/canvas` validates and rasterizes SVG designs.
- Pillow is used only by the optional VSS golden-raster comparison tool.

The HTTP server, UI embedding, image codecs, CRC32, logging, and TCP handling use
the Go standard library.

## Tests and evidence

`internal/pv3` tests consume the real fixtures in
`Discovery/codex/captures`. They verify:

- Fragmentation-safe outer record reads and checksum validation.
- UUID, status, firmware, battery, and display dimension decoding.
- Status response structure.
- Image message headers, frame ID, encoding, packed pixels, and LZ4 round trips.
- Literal-only LZ4 fallback for incompressible data.

Other package tests cover image processing and both rendering modes, SQLite restart persistence,
assignments, native uploads, legacy device shapes, and input rejection.

To compare candidate pixel preparation with the captured full-screen VSS
reference, run from `server/`:

```sh
python3 tools/compare_vss_raster.py \
  ../debug/test_pattern/runs/identical-frame-01/assignment-12-source.png \
  ../debug/test_pattern/runs/vss-fullscreen-diagnostic-01/decoded/02-type-3-length-70140.png \
  --output /tmp/vss-comparison
```

The report ranks orientation, gamma, recovered native-VSS, and crisp-edge
candidates. `--assert-exact` exits unsuccessfully unless packed bytes match.

## Adding protocol support

Protocol changes should begin with a reproducible capture and binary fixture.
Add a decoder test before extending the gateway. Do not infer application record
boundaries from `Read` calls or reuse the unrelated USB TCLV framing.

For touch support, the intended sequence is:

1. Capture a known tap, press, and movement through official VSS.
2. Identify the complete record envelope and coordinate/event fields.
3. Add golden fixtures and a decoder in `internal/pv3`.
4. Persist and publish normalized events through the existing event hub.
5. Resolve events against the interaction map stored for their frame ID and
   dispatch the matching SVG action.
