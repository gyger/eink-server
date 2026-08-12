# HTTP API

The default base URL is `http://SERVER:8080`. Native errors use this shape:

```json
{"error":{"code":"device_not_found","message":"unknown device"}}
```

## Devices

### `GET /api/v1/devices`

Lists enrolled devices, current status, connection state, image defaults, and
the latest desired assignment. Telemetry includes battery percentage,
temperature in degrees Celsius, and relative-humidity percentage.

### `GET /api/v1/devices/{uuid}`

Returns one device or `404 device_not_found`.

### `PATCH /api/v1/devices/{uuid}`

Updates the friendly name, location, and image defaults. When changing
defaults, send the complete settings object:

```json
{
  "name": "Kitchen",
  "location": "Ground floor",
  "timezone": "Europe/Berlin",
  "locale": "de-DE",
  "image_defaults": {
    "fit": "contain",
    "background": "white",
    "rotation": 0,
    "invert": false,
    "dither": "none",
    "rendering": "eink"
  }
}
```

## Images

### `PUT /api/v1/devices/{uuid}/image`

The request body is raw PNG, JPEG, or SVG bytes with `Content-Type: image/png`,
`image/jpeg`, or `image/svg+xml`. SVG activates a persistent dynamic design.
Uploading PNG/JPEG later deactivates that design. SVG does not accept image
processing query overrides. A successful request returns `202`.

### `POST /api/v1/images:broadcast`

Uses the same raw body and creates an assignment for every enrolled device.
The original upload is stored once. Returns `409 no_devices` if none exist.

Both upload routes accept optional query overrides:

| Parameter | Values | Default |
|---|---|---|
| `fit` | `contain`, `cover`, `stretch`, `exact` | Device setting; initially `contain` |
| `background` | `white`, `black` | Device setting; initially `white` |
| `rotation` | `0`, `90`, `180`, `270` | Device setting; initially `0` |
| `invert` | Boolean | Device setting; initially `false` |
| `dither` | `none`, `floyd-steinberg` | Device setting; initially `none` |
| `rendering` | `eink`, `smooth` | Device setting; enrollment default is configured by `default_rendering` (`eink` when omitted) |

Uploads are limited to 20 MiB and decoded images to 20 megapixels. `exact`
rejects a source whose post-rotation dimensions differ from the display.
`eink` uses native-resolution SVG rendering, hardens high-contrast edge
coverage, and applies the recovered VSS grayscale range preparation. `smooth`
retains 3× SVG supersampling and ordinary grayscale quantization.
The configured `default_rendering` supplies this setting only when a tablet
first enrolls. Changing it does not overwrite enrolled tablets. Query
overrides apply to PNG/JPEG uploads; select the SVG rendering mode through the
tablet's stored `image_defaults`.

Assignment states are:

- `queued` — persisted, waiting for delivery.
- `sent` — written successfully to the tablet connection.
- `delivered` — echoed by the tablet as its display-state ID.
- `error` — processing or writing failed; a later check-in may retry it.

### `GET /api/v1/devices/{uuid}/image`

Returns the current processed preview as `image/png`, or `404 image_not_found`.

## Health and events

### `GET /api/v1/health`

Returns service status and current UTC time.

### `GET /api/v1/events?after_id=0&limit=100`

Returns persisted events in ascending order. The limit is capped at 500.

### `GET /api/v1/events/stream`

Opens a Server-Sent Events stream. Event names currently include:

- `device.connected`, `device.disconnected`, `device.enrolled`, `device.status`
- `image.queued`, `image.sent`, `image.acknowledged`, `image.delivered`,
  `image.failed`

SVG touch and action events include `touch.tap`, `action.unresolved`,
`action.started`, `action.succeeded`, and `action.failed`.

## SVG designs and actions

- `GET /api/v1/designs` lists built-in, filesystem, and database designs.
- `GET /api/v1/designs/{id}` returns compiled metadata.
- `PUT /api/v1/designs/{name}` stores a reusable database SVG.
- `DELETE /api/v1/designs/{name}` deletes a database SVG.
- `POST /api/v1/designs:reload` rescans the configured design directory.
- `PUT /api/v1/devices/{uuid}/design` assigns a design, for example
  `{"design_id":"builtin:status"}`. Use `builtin:eink-verification` for the
  native-resolution grayscale, line, and typography diagnostic screen.
- `DELETE /api/v1/devices/{uuid}/design` stops dynamic refresh without blanking
  the displayed frame.
- `GET /api/v1/actions` lists actions with header values redacted.
- `PUT /api/v1/actions/{name}` creates or replaces a dynamic webhook action.
- `DELETE /api/v1/actions/{name}` deletes a dynamic action; TOML-managed actions
  are read-only.

See [SVG designs](svg-designs.md) for the SVG attribute contract and webhook
payload.

## VSS compatibility routes

These routes are deliberately incomplete:

- `POST /login` returns a redirect and dummy `VServer` cookie.
- `GET /api/device/` exposes the VSS-shaped fields used by checked-in prior art.
- `PUT /backend/{uuid}` accepts multipart form field `image` containing PNG or
  JPEG data and enters the normal native image pipeline.

There are no compatibility sessions, apps, URL renderer, users, firmware, or
device-control APIs.
