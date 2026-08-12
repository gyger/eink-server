# Tablet setup and validation

## Prerequisites

- The server and tablet are on the same trusted LAN.
- The tablet is configured to connect to the server's LAN IP on TCP port 11113.
- The host firewall permits inbound TCP 11113 and HTTP 8080 as appropriate.
- TCP 11113 is mandatory in the firewall zone attached to the tablet-facing
  network. TCP 8080 is optional unless the management UI must be reached from
  another host.
- For normal image transfer, disconnect the tablet's USB/FTDI data cable after
  configuration. Captures show that USB attachment changes display behavior.

Configuration itself is performed through the Visionect configurator or the
tablet's serial CLI and is outside the Go server.

## Start the server

From the repository root:

```sh
go run ./cmd/eink-server
```

Expected startup logs mention both listeners:

```text
management server listening address=:8080
tablet gateway listening address=:11113
```

Open `http://SERVER:8080/`. A valid PV3 tablet auto-enrolls after its first
application status record.

## First-image acceptance test

1. Verify UUID, firmware, battery, and 1024×758 dimensions in the UI.
2. Confirm the tablet uses the default `eink` renderer, then upload or assign
   `builtin:eink-verification` with the default contain/white settings.
3. Confirm the assignment moves from `queued` to `sent`.
4. Verify the image appears correctly oriented on the tablet.
5. Wait for the following status and confirm the assignment becomes `delivered`.
6. Queue another image while the tablet is offline, restart the server, and
   verify the desired assignment survives and is sent after reconnect.
7. With multiple tablets, verify per-device images remain independent and a
   broadcast creates one assignment per device.
8. Compare `eink` and `smooth` on small text and thin lines. `eink` should use
   native-resolution SVG rendering; `smooth` should retain visibly softer 3×
   supersampling.

The verified Joan 6 needs a built-in 180-degree native framebuffer correction.
Leave the image setting at `rotation=0` for normally oriented source images. The
rotation setting is an additional user-facing transform, not a workaround for
the panel orientation.

## Diagnostics

Follow structured service activity:

```sh
curl -N http://SERVER:8080/api/v1/events/stream
curl 'http://SERVER:8080/api/v1/events?limit=100'
```

Common symptoms:

- **Tablet never appears:** verify its saved server IP/port, Wi-Fi route, host
  firewall, and whether another process already owns port 11113.
- **Bootloader connection appears then closes:** expected; v1 deliberately does
  not answer bootloader/update traffic. The application should connect later.
- **Assignment remains queued:** the tablet is not currently connected or has
  not completed its initial status reply.
- **Assignment is sent but not delivered:** inspect later status events. This may
  indicate that the generated opaque frame ID is not accepted by that firmware.
- **No screen update while USB is connected:** disconnect the USB data cable and
  let the device reconnect over Wi-Fi.
- **Image rejected by API:** for PNG/JPEG, confirm content type, input size,
  decoded pixel count, even target width, and `exact` dimensions. For SVG,
  confirm `image/svg+xml`, a positive `viewBox`, self-contained resources, and
  the 2 MiB source limit. SVG uploads do not accept image-processing query
  overrides.
- **Small text has white channels or soft stems:** select `eink`, reassign the
  design so it is rasterized again, and compare the processed preview. Use
  `smooth` for photographs or artwork that benefits from ordinary antialiasing.

## Touch logging check

Tap the connected display and inspect the server log. A valid contact produces
one entry similar to:

```text
level=INFO msg="touch event" uuid=... frame_id=3946585767 x=99 y=54 raw_x=924 raw_y=703
```

`frame_id` identifies the image visible when contact occurred. `x` and `y` use
the physical top-left-origin screen coordinate system. `raw_x`
and `raw_y` retain the panel-native 180-degree-rotated values for diagnostics.
Touch records are saved as `touch.tap` events and sent through the HTTP event
stream. SVG frames may additionally dispatch a named action.

## Safety and recovery

The gateway never writes tablet configuration or firmware. Stopping it leaves
the current E Ink image in place. To return to official VSS, restore the tablet's
previous server address with the same configurator/serial method used to point it
at this server.
