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

Updates the friendly name, image defaults, or both. When changing defaults,
send the complete settings object:

```json
{
  "name": "Kitchen",
  "image_defaults": {
    "fit": "contain",
    "background": "white",
    "rotation": 0,
    "invert": false,
    "dither": "none"
  }
}
```

## Images

### `PUT /api/v1/devices/{uuid}/image`

The request body is raw PNG or JPEG bytes with `Content-Type: image/png` or
`image/jpeg`. A successful request returns `202` with one assignment.

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

Uploads are limited to 20 MiB and decoded images to 20 megapixels. `exact`
rejects a source whose post-rotation dimensions differ from the display.

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

Future decoded touch events will use this same event mechanism.

## VSS compatibility routes

These routes are deliberately incomplete:

- `POST /login` returns a redirect and dummy `VServer` cookie.
- `GET /api/device/` exposes the VSS-shaped fields used by checked-in prior art.
- `PUT /backend/{uuid}` accepts multipart form field `image` containing PNG or
  JPEG data and enters the normal native image pipeline.

There are no compatibility sessions, apps, URL renderer, users, firmware, or
device-control APIs.
