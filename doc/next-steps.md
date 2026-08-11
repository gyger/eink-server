# Next steps

The server is implemented and verified with captured protocol fixtures, unit and
integration tests, the race detector, static analysis, a local process smoke
test, and a first end-to-end test with the physical Joan 6. The tablet connected,
auto-enrolled, accepted a generated image transfer, and later confirmed the
server-assigned frame ID. The most important remaining work is broadening that
hardware validation beyond the successful first path.

## Immediate improvements

The 2026-08-11 physical captures identified two application payloads that need
dedicated dispatch before broader feature work.

Completed:

- Application-message dispatch uses the logical message type at payload
   offset 20. Known values are `1` for acknowledgement, `3` for full status,
   `5` for a server image, and `6` for touch. Validate the complete record shape
   before selecting a decoder; do not dispatch from a single word alone.
- The tablet's type-1 post-image acknowledgement has a strict decoder. Its
   44-byte payload is `reserved`, UUID, message type `1`, echoed image/status
   sequence, then `8, 0, 1, 0`. Correlate the echoed sequence for diagnostics,
   but continue using a later status frame-ID echo as the authoritative
   `delivered` signal. The precise names of the `8, 0, 1` fields remain unknown.
- Type-6 touch records have a strict fixture-tested decoder. Native coordinates
  are converted to physical display coordinates and written to the logfile as
  `touch event`; they are deliberately not persisted, published, or rendered.

Still to implement:

1. Publish the observed touch abstraction as `touch.tap` when the project is
   ready to consume touch outside the logfile. Quick taps, a
   three-second hold, and a slow drag each produced one record; the drag reported
   its initial contact and no down/move/up stream was observed. Do not synthesize
   phases that the device did not send.

The byte layouts and capture observations are documented in
`server/doc/protocol.md` and `Discovery/codex/README.md`.

The Joan 6 native 180-degree framebuffer correction was physically retested on
2026-08-11 and passed: the same dashboard source displayed upright with the
user-facing setting still at `rotation=0`.

## 1. Complete the physical display acceptance matrix

The first physical protocol test passed on 2026-08-10 with the Joan disconnected
from USB and configured for `10.42.0.1:11113`.

Completed:

- Stopped the official VSS gateway and ran this server on port 11113 with an
  isolated SQLite database.
- Disconnected the tablet's USB/FTDI data cable before image delivery.
- Confirmed the Joan auto-enrolled as
  `30004f00-0650-4858-5239-312000000000` and reported firmware `7.4.4407`,
  battery, temperature, and 1024×758 resolution.
- Captured the initial 552-byte tablet status record and the server's 85-byte
  compressed initial response.
- Uploaded a known 1024×758 grayscale PNG through the native API. Assignment 1
  transitioned from `queued` to `sent`.
- Confirmed the tablet later echoed generated frame ID `1898519895` as its
  display state. The assignment transitioned to `delivered`, validating that
  the image checksum/frame-ID field can be treated as the assigned opaque frame
  ID on this firmware.
- Observed normal disconnect/reconnect polling without losing the device's
  online state.
- On 2026-08-11, sent `prior-art/joan-dashboard/docs/preview.png` with
  `rotation=0`. The tablet displayed the recognizable dashboard, but it was
  physically upside down. This confirms image display while exposing a Joan 6
  orientation/default-rotation mismatch in the server.
- After adding the native 180-degree packed-framebuffer correction, repeated
  the same dashboard test with `rotation=0`. The user physically confirmed that
  it displayed upright. The correction is therefore accepted on Joan 6
  firmware 7.4.4407.
- Saved the complete 110-packet capture at
  `Discovery/codex/captures/2026-08-10-test-server-01.pcapng` and the isolated
  test database at `Discovery/codex/runs/test-server-01/joan.db`.

Remaining:

- Finish visual inspection of grayscale levels and absence of corruption. Image
  content and corrected orientation are now physically confirmed.
- Send a deliberately simple black-and-white test pattern and record a visual
  result.
- Repeat with a grayscale photograph using Floyd–Steinberg dithering and inspect
  the physical result.
- Promote the successful capture into the documented golden-fixture set and add
  its observations to `Discovery/codex/README.md`.

Exit criteria:

- Generated images are visually confirmed to display correctly without VSS.
- Assignments transition through `queued`, `sent`, and `delivered` (completed
  for one image on Joan 6 firmware 7.4.4407).
- The capture is saved as a new golden fixture with observations documented in
  `Discovery/codex/README.md`.

If the image is rejected, compare the generated logical message and chunk stream
against the two successful VSS image captures. Investigate the image checksum
field first; do not guess at unrelated fields.

## 2. Exercise recovery and multi-device behavior

After the successful first transfer, validate the service behavior expected in
normal unattended operation:

- Queue an image while the tablet is offline and verify delivery after reconnect.
- Restart the server with a queued assignment and verify SQLite recovery.
- Replace an active connection with a new connection for the same UUID.
- Upload several images rapidly and verify only the newest desired image matters.
- Interrupt a transfer and confirm it retries safely on a later connection.
- Connect two or more tablets and verify independent images and broadcast.
- Run continuously for at least several heartbeat cycles and inspect memory,
  database size, event pruning, and reconnect logs.

Exit criteria:

- No duplicate or interleaved PV3 writes.
- A failed tablet does not affect other devices.
- Restart and reconnect behavior requires no manual database repair.

## 3. Turn remaining protocol assumptions into tests

Use physical captures to tighten the current implementation:

- Add the observed same-connection image delivery after the initial status
  exchange as a regression test; this succeeded in the 2026-08-10 hardware run.
- Confirm frame-ID generation rules across multiple firmware versions.
- Extend type-1 acknowledgement fixture coverage if another firmware changes
  the known shape. Its sequence correlation is implemented; the precise
  name/semantics of the trailing `8, 0, 1` fields remain unknown.
- Dispatch the now-identified type-6 touch record before the status parser.
  During the 2026-08-11 captures it was harmlessly logged as `unsupported
  protocol version 6`, but this warning is misleading.
- Verify the status field map on every available Joan firmware and preserve
  model-specific differences explicitly.
- Capture malformed or interrupted transfers where practical and document the
  tablet's retry behavior.
- Add golden encode comparisons, not only decode tests, wherever VSS output is
  deterministic.

Exit criteria:

- Every field used to generate traffic is either observed, fixture-tested, or
  clearly labeled as an opaque value.
- The protocol document matches the implementation and current captures.

## 4. Improve operation and packaging

Once hardware delivery works reliably:

- Add a `Makefile` or small task script that runs formatting, tests, race tests,
  vet, and the build inside the default Fedora Toolbox.
- Produce versioned Linux binaries and include build/version information in
  `/api/v1/health` and startup logs.
- Add a systemd user or system service example with a persistent data directory,
  restart policy, and restricted permissions.
- Add graceful database backup instructions and a documented schema migration
  policy before the first upgrade changes the schema.
- Add optional Prometheus-style counters only if operational experience shows
  logs and the event API are insufficient.
- Add configurable event/status retention and upload limits if real usage needs
  values other than the current defaults.

Keep the normal deployment as one binary plus one SQLite file. A container image
is not required unless a real deployment needs it.

## 5. Refine the management experience

The current embedded UI is intentionally small. Useful incremental additions are:

- Show connection and image-delivery events per device.
- Show clear queued/sent/delivered/error timestamps and retry errors.
- Allow previewing processing changes before saving or sending.
- Add confirmation and per-device results for broadcasts.
- Expose the latest raw known/unknown status fields in a diagnostics section.
- Add an explicit retry action that requeues the desired assignment without
  requiring another upload.

Avoid adding VSS-style sessions, application catalogs, or HTML rendering unless
a concrete use case justifies their complexity.

## 6. Capture and decode touch events

The first protocol-research milestone was completed on 2026-08-11. Three known
taps, a hold, and a drag established the type-6 record boundary, UUID,
native-panel coordinates, and 180-degree coordinate transform. This firmware
emitted one completed-contact record per gesture and no separate movement or
phase records. No acknowledgement was required against the test server.

Remaining work before UI code:

1. Repeat through official VSS to determine whether it returns an optional
   response or enables richer touch reporting.
2. Test repeated contacts and edge/extreme coordinates on a coordinate grid.
3. Add the raw fixtures and a strict decoder in `internal/pv3`.
4. Normalize the observed abstraction as `touch.tap`; do not invent down/move/up
   phases unless another capture actually produces them.
5. Persist events in the bounded event log and publish them through the existing
   SSE endpoint.

Exit criteria:

- Known contacts decode to the expected physical coordinates and observed
  completed-contact abstraction.
- Unknown variants are preserved or rejected safely rather than misdecoded.
- Receiving touch events does not disturb heartbeat or image delivery.

## 7. Add touch-driven Go rendering

After touch decoding is stable, use the existing renderer boundary to build one
small interactive proof of concept:

- Define a renderer with explicit per-device state.
- Render a screen containing two or three large buttons.
- Feed normalized touch events to the renderer.
- Produce and queue the next frame using the normal assignment/delivery path.
- Debounce duplicate input and account for E Ink refresh latency.

A first useful screen might be a simple menu, room-status panel, or page switcher.
Do not start with a general template language. Let one or two real screens reveal
which layout, font, state, and interaction primitives are actually needed.

Exit criteria:

- A physical tap causes a deterministic state change and updated frame.
- Renderer state survives reconnects and, if needed, server restarts.
- Application rendering remains separate from PV3 transport code.

## 8. Broaden hardware support carefully

Support additional tablets only with device-specific captures and acceptance
tests:

- Test another Joan 6 firmware revision.
- Capture and verify a Joan 13 or other Visionect PV3 display.
- Confirm resolution, orientation, encoding, display count, and update flags.
- Move the currently verified Joan 6 native 180-degree framebuffer correction
  into an explicit per-model capability before supporting a model with a
  different native orientation.
- Add model/firmware compatibility notes and fixtures.
- Keep dynamic dimensions, but introduce explicit capability records if models
  require different encoding or message behavior.

Until those tests pass, documentation and releases should continue to say:
"Joan 6 firmware 7.4.4407 verified; other PV3 devices experimental."

## 9. Add security only when the deployment boundary changes

For a trusted isolated LAN, the present no-authentication model is intentional.
Before exposing the management API to a larger network:

- Add real administrator authentication and CSRF protection.
- Terminate TLS directly or document a supported reverse-proxy configuration.
- Restrict tablet TCP access by network policy and optionally approved UUIDs.
- Add request-rate limits and stricter audit logging.
- Review image decoders, multipart handling, SSE limits, and database permissions
  as externally reachable attack surfaces.

Do not mistake the compatibility `/login` cookie for authentication.

## Recommended milestone order

1. Finish visual, black-and-white, and dithered-image acceptance; document the
   successful capture as a golden fixture.
2. Offline queue, restart recovery, interruption, reconnect, and multi-device
   soak testing.
3. Protocol corrections backed by fixtures.
4. Versioned binary and systemd deployment.
5. UI diagnostics and retry improvements.
6. Touch capture and decoding.
7. One touch-driven Go renderer.
8. Additional tablet models and security as demanded by deployment.
