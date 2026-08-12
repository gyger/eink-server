# Architecture

## Runtime

The server is one Go process with two listeners and one SQLite database:

```text
PNG/JPEG/SVG client ──HTTP :8080──> API / design and image processing
                                      │
                                      v
                                  SQLite state
                                      │
Joan tablet <────PV3 TCP :11113──── gateway / frame delivery
```

The HTTP listener serves the native API, compatibility routes, SSE, and the
embedded management page. Joan tablets initiate outbound TCP connections to the
PV3 listener.

## Packages

- `cmd/joan-server` owns flags, logging, shutdown, and listener startup.
- `internal/pv3` implements record framing, status parsing, responses, image
  messages, and raw LZ4 chunks.
- `internal/gateway` owns live tablet connections and delivery ordering.
- `internal/imageproc` decodes, resizes, rotates, quantizes, and packs images.
- `internal/render` separates content generation from protocol delivery. The
  upload renderer converts queued raster frames for PV3 delivery.
- `internal/design` validates and rasterizes SVG, substitutes dynamic values,
  compiles interaction maps, and manages active designs.
- `internal/action` executes bounded asynchronous webhook actions.
- `internal/store` owns the SQLite schema and all persistent state.
- `internal/httpapi` provides native and compatibility HTTP routes and the UI.
- `internal/events` fans persisted events out to live SSE subscribers.

## Image flow

1. A client uploads PNG/JPEG content or an SVG design for one device or all
   enrolled devices, or assigns a reusable SVG from the design catalog.
2. The server validates encoded/decoded image limits or compiles and sanitizes
   the SVG before storing it.
3. SQLite stores the original once and creates one assignment per target.
4. Each assignment snapshots that device's processing settings and gets an
   opaque non-zero 32-bit frame ID.
5. If the tablet is connected, delivery is triggered immediately. Otherwise,
   the newest desired assignment is sent at its next status exchange.
6. SVG dynamic values and interaction regions are resolved before the normal
   image renderer produces a device-sized grayscale preview and packed 4-bit
   pixels. The PV3 layer wraps and LZ4-compresses the frame.
   The shipped `eink` mode uses native-resolution SVG output, edge-aware
   antialias suppression, and the recovered VSS grayscale range mapping;
   `smooth` retains 3× supersampling and ordinary grayscale quantization.
7. The gateway sends the changed rectangle as one logical image. The first
   update after a connection is full-screen.
8. The assignment becomes `sent`. An immediate type-1 reply records
   `acknowledged_at`; a later tablet status that echoes the frame ID changes it
   to `delivered`.

Only the newest undelivered assignment is relevant to a device. Old assignments
remain historical metadata but are never replayed ahead of a newer frame.

## Connection and concurrency model

- A newly valid PV3 device is auto-enrolled.
- A newly enrolled device is assigned the configured default SVG design
  (`builtin:status` by default).
- A newly enrolled device snapshots the configured `default_rendering`
  (`eink` by default); later changes are per-device and persisted in SQLite.
- It also snapshots `default_timezone` and `default_locale`. The design
  scheduler rerenders connected periodic designs on aligned minute boundaries.
- There is at most one active session per UUID. A newer connection closes and
  replaces the older one.
- Each session serializes writes so heartbeat replies and image messages cannot
  interleave on the TCP stream.
- Image delivery is also serialized per device to prevent duplicate concurrent
  sends from an HTTP upload and heartbeat.
- The first status reply must complete before an API-triggered image can write.
- SQLite intentionally uses one database connection. Device listing closes its
  row cursor before performing per-device assignment lookups.

## Persistence

The default database is `./data/eink.db`. It contains:

- Device identity, name, location, first/last seen, dimensions, firmware, battery,
  temperature, relative humidity,
  display-state ID, raw status fields, and image defaults.
- Original uploaded image blobs.
- Per-device assignments, settings snapshots, frame IDs, and delivery state.
- Status samples when values change or after a 15-minute sampling interval.
- Lifecycle and delivery events.
- Reusable and active SVG designs, webhook actions, and interaction maps tied
  to exact frame IDs.

SQLite runs in WAL mode with foreign keys and a busy timeout. Status samples and
events are pruned after seven days; events are additionally capped at 10,000.
The singleton `schema_version` row is currently version 1. Startup applies
pending migrations transactionally and refuses databases created by a newer
server. Version 1 remains editable until the first release; after that, schema
changes must append a new numbered migration.

## Security boundary

Version 1 is for a trusted LAN. It has no authentication or TLS. The compatibility
login route only issues a dummy cookie so existing VSS clients can proceed. Do
not expose ports 8080 or 11113 to the public internet.
