# Architecture

## Runtime

The server is one Go process with two listeners and one SQLite database:

```text
PNG/JPEG client ──HTTP :8080──> API / image processing
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
  upload renderer is v1; later touch-driven renderers can implement the same
  interface.
- `internal/store` owns the SQLite schema and all persistent state.
- `internal/httpapi` provides native and compatibility HTTP routes and the UI.
- `internal/events` fans persisted events out to live SSE subscribers.

## Image flow

1. A client uploads a PNG or JPEG for one device or all enrolled devices.
2. The server validates the encoded and decoded size before storing it.
3. SQLite stores the original once and creates one assignment per target.
4. Each assignment snapshots that device's processing settings and gets an
   opaque non-zero 32-bit frame ID.
5. If the tablet is connected, delivery is triggered immediately. Otherwise,
   the newest desired assignment is sent at its next status exchange.
6. The renderer produces a device-sized grayscale preview and packed 4-bit
   pixels. The PV3 layer wraps and LZ4-compresses the frame.
7. The assignment becomes `sent`. An immediate type-1 reply records
   `acknowledged_at`; a later tablet status that echoes the frame ID changes it
   to `delivered`.

Only the newest undelivered assignment is relevant to a device. Old assignments
remain historical metadata but are never replayed ahead of a newer frame.

## Connection and concurrency model

- A newly valid PV3 device is auto-enrolled.
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

- Device identity, name, first/last seen, dimensions, firmware, battery,
  temperature, relative humidity,
  display-state ID, raw status fields, and image defaults.
- Original uploaded image blobs.
- Per-device assignments, settings snapshots, frame IDs, and delivery state.
- Status samples when values change or after a 15-minute sampling interval.
- Lifecycle and delivery events.

SQLite runs in WAL mode with foreign keys and a busy timeout. Status samples and
events are pruned after seven days; events are additionally capped at 10,000.

## Security boundary

Version 1 is for a trusted LAN. It has no authentication or TLS. The compatibility
login route only issues a dummy cookie so existing VSS clients can proceed. Do
not expose ports 8080 or 11113 to the public internet.
