# EInk Tablet Server

A small self-hosted Go gateway for Joan/Visionect protocol-v3 tablets. It
accepts PNG/JPEG images and interactive SVG designs, converts them to the
tablet's packed 4-bit grayscale format, and delivers the newest desired frame
when the tablet checks in.

> [!IMPORTANT]
> This project has been heavily vibe coded with guidance from a human.
> The generated code has been accepted without line-by-line human validation.
> The protocol was reverse engineered from packet captures, so parts of its
> behavior may remain unknown or incomplete.

Detailed documentation is available in [doc/README.md](doc/README.md).

The captured Joan 6 (1024×758, firmware 7.4.4407) is the verified target. Other
PV3 devices are handled using dimensions reported in status, but are not yet
claimed as tested.

## Development

The host workspace does not contain Go. Use the default Fedora Toolbox:

```sh
toolbox enter
cd /var/home/gyger/Projects/JoanTablet/server
sudo dnf install golang
go test ./...
go run ./cmd/joan-server
```

With no configuration file or flags, the defaults are:

- `:11113` — EInk tablet TCP protocol
- `:8080` — web UI and REST API
- `./data/eink.db` — SQLite state
- `eink` — default rendering mode for newly enrolled tablets

At startup, the server looks for `eink-server.toml` beside its executable. A
missing or empty file uses all defaults. Use `--config /path/to/config.toml` to
select another file; command-line settings override values from the file. See
[configuration](doc/configuration.md) for the format and complete behavior.

Point the tablet at the server's LAN address and port 11113. Open
`http://SERVER:8080/` after the tablet has connected.
Newly enrolled tablets are automatically sent the `builtin:status` clock and
calendar dashboard. Set the tablet's name and location in the web UI; both are
available to SVG designs. Set `default_design = ""` in TOML to leave a new
tablet's current screen unchanged instead.
Set `default_rendering = "smooth"` to make newly enrolled tablets use the
supersampled renderer; the shipped default is `eink`.

This release has no authentication or TLS. Do not expose either listener to the
public internet.

## Native API

```sh
# List devices
curl http://localhost:8080/api/v1/devices

# Queue a PNG for one device
curl -X PUT -H 'Content-Type: image/png' \
  --data-binary @screen.png \
  'http://localhost:8080/api/v1/devices/DEVICE_UUID/image?fit=contain&dither=none'

# Queue a JPEG for every enrolled device
curl -X POST -H 'Content-Type: image/jpeg' \
  --data-binary @photo.jpg \
  'http://localhost:8080/api/v1/images:broadcast?fit=cover&dither=floyd-steinberg'

# Follow lifecycle and delivery events
curl -N http://localhost:8080/api/v1/events/stream

# Activate an SVG design with dynamic text and touch actions
curl -X PUT -H 'Content-Type: image/svg+xml' \
  --data-binary @dashboard.svg \
  http://localhost:8080/api/v1/devices/DEVICE_UUID/image
```

Image options are `fit=contain|cover|stretch|exact`,
`background=white|black`, `rotation=0|90|180|270`, `invert=true|false`,
`dither=none|floyd-steinberg`, and `rendering=eink|smooth`. The default `eink`
mode renders SVGs at native resolution, suppresses high-contrast antialiased
edges, and applies the recovered VSS grayscale preparation. Use `smooth` for
photographs or designs where ordinary antialiasing is preferred. Per-device
defaults can be changed with
`PATCH /api/v1/devices/{uuid}` or the web interface.

Compatibility routes are intentionally limited to `POST /login`,
`GET /api/device/`, and multipart `PUT /backend/{uuid}`. VSS sessions, HTML/URL
rendering, firmware, sleep schedules, and device commands are not implemented.

## Hardware acceptance

Before relying on generated frames, test with the Joan physically disconnected
from USB (USB changes its normal display-transfer behavior):

1. Confirm the tablet auto-enrolls and reports its status.
2. Upload a high-contrast test pattern and verify it displays.
3. Confirm a later status echoes the assigned frame ID and the UI changes the
   assignment from `sent` to `delivered`.
4. Restart the server and confirm an offline queued image survives.

WASM widgets, multi-page navigation, general multi-rectangle/waveform control,
URL rendering, encryption, bootloader/firmware updates, and verified
larger-device support are future work. Current delivery can update one changed
bounding rectangle with a white precursor.
