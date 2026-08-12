# Protocol discovery backlog

This document records protocol hypotheses and experiments that remain after the
successful Joan 6 firmware 7.4.4407 interoperability tests. It is intentionally
separate from `protocol.md`: that file describes observed, implemented behavior,
whereas this file includes guesses that must not become protocol constants
without new captures.

## Current baseline

The following path is sufficiently understood for the current server:

- PV3 outer framing, direction-dependent CRC32, and TCP stream boundaries.
- Initial and heartbeat status exchange.
- Full-screen and one changed-bounding-rectangle 1024×758, 4-bit grayscale
  delivery using raw-LZ4 chunks.
- Joan 6 native 180-degree framebuffer correction.
- Type-1 post-image acknowledgement and sequence correlation.
- Later delivery confirmation through the display-state/frame-ID status field.
- Type-6 completed-contact touch records, frame correlation, and physical
  coordinate conversion.
- Battery, temperature, dimensions, firmware, heartbeat, and display-state
  telemetry.
- Status field 15 as relative-humidity percentage on Joan 6 firmware 7.4.4407.
- A structured full-screen probe established the Encoding-4 nibble order: the
  left pixel is stored in the low nibble and the right pixel in the high nibble.
  Exact replay of VSS packed pixels through the Go transport also confirmed the
  image framing and native orientation path.

None of the hypotheses below blocks basic full-screen image delivery or touch
logging on this device and firmware.

## 1. Partial display updates and waveform selection

Known:

- The current image message contains a primitive count, image type, packed
  rectangle origin, dimensions, rectangle flags `0x102`, encoding `4`, and
  packed pixel length.
- Encoding 4 is full 4-bit grayscale, two horizontal pixels per byte.
- Full-screen 4-bit delivery works on the physical tablet.
- VSS has sent three ordered, overlapping rectangles in one logical image;
  later primitives overwrite earlier ones. Odd-width rectangles are packed
  continuously rather than padded per row.
- The Go server sends one even-X/even-width changed bounding rectangle, or a
  full frame when no connection-local framebuffer is available.
- Joan's published specification advertises a faster 1-bit partial refresh in
  addition to its 4-bit full-screen refresh.

Hypotheses:

- Another encoding value selects packed 1-bit pixels.
- Bits in `0x102` select full/partial update, waveform, inversion, or refresh
  behavior.
- The tablet may require occasional full refreshes after repeated partial
  updates to clear ghosting.

Experiments:

1. Capture official VSS using a confirmed 1-bit waveform and compare it with
   the known 4-bit multi-rectangle transfer.
2. Identify which flag or encoding selects 1-bit content.
3. Change exactly one rectangle flag or encoding field at a time only after a
   known-good capture exists.
4. Test repeated clock-digit updates and record latency, ghosting, battery use,
   acknowledgement, and display-state behavior.
5. Determine whether partial updates carry a new frame ID and whether touch
   records refer to that new ID.

This is the highest-value remaining discovery for clocks and other frequently
changing screens.

## 2. Wake, sleep, check-in, and forced refresh

Known:

- The tablet initiates the TCP connection; the server cannot write when no
  connection exists.
- A newly assigned image is pushed immediately while the connection is active.
- If disconnected, the newest desired assignment waits for the next check-in.
- Resending a newly assigned full image causes a physical panel refresh.
- No standalone redraw-current-frame command has been observed.
- Status field 29 is associated with heartbeat/check-in configuration and has
  been observed with value `3`, but its unit and mutability over PV3 have not
  been independently proven.

Hypotheses:

- A generic device-command message can alter heartbeat, sleep, or wake policy.
- The value of field 29 may be minutes, a profile index, or a server-selected
  policy rather than a literal interval.
- Official VSS may keep a connection active for a bounded window and rely on a
  later Wi-Fi wake schedule rather than actively waking the tablet.
- A flag in the image primitive may request a panel redraw even when pixels are
  unchanged.

Experiments:

1. Record connection open/close and status times for at least 24 hours without
   USB attached.
2. Change the configured heartbeat through the supported configurator/serial
   interface and correlate it with field 29 and observed reconnect timing.
3. Capture official VSS sleep-schedule and reboot actions through a transparent
   proxy.
4. Send identical pixels with a new frame ID and compare with retransmitting the
   same frame ID.
5. Determine whether a server response can extend the active connection window.

Without a wake mechanism, a clock can be current whenever connected and fresh
on reconnect, but cannot guarantee a new frame every minute while the tablet is
asleep.

## 3. Connection recovery and interrupted transfers

Known:

- TCP reads cannot be treated as PV3 record boundaries.
- The server preserves a newest desired assignment in SQLite and sends it after
  a valid status exchange.
- A successful immediate type-1 acknowledgement is distinct from the later
  display-state delivery confirmation.
- The tablet can replace its display with an offline-symbol screen while the
  server is unavailable.

Hypotheses:

- The tablet discards an incomplete compressed message when the connection
  closes and safely accepts the complete frame on a later connection.
- A reconnect status may reset display state to zero or another sentinel after
  showing the offline screen.
- Resending an assignment already marked delivered is necessary when reported
  display state no longer matches its frame ID.
- The tablet may have a retry/error application message that has not appeared in
  successful captures.

Experiments:

1. Terminate transfers at the outer header, chunk header, middle chunk, and final
   chunk, then observe reconnect behavior.
2. Corrupt one CRC, LZ4 block, chunk index, compressed length, and logical length
   in isolated tests.
3. Restart the server with queued, sent-but-unacknowledged, acknowledged, and
   delivered assignments.
4. Leave the server offline long enough for the offline symbol, then compare the
   reconnect display state with the stored desired frame.
5. Capture all tablet replies rather than assuming only status, acknowledgement,
   and touch message types exist.

## 4. Type-1 acknowledgement semantics

Known:

- The tablet sends a 44-byte logical message after an image.
- Message type is `1`.
- The sequence field matches the image/status sequence (`3`, then `4` in the VSS
  capture).
- The remaining observed words are `8, 0, 1, 0`.
- The acknowledgement does not contain the frame ID and is not sufficient proof
  that the panel refresh completed.

Hypotheses:

- `8` is a nested body length rather than a command identifier.
- `0` is a result/status code and `1` is a success or accepted count.
- The same envelope acknowledges messages other than images.
- Failure variants change one of these words or append an error body.

Experiments:

1. Capture acknowledgement records after other official VSS commands.
2. Cause a safely malformed image and check for a type-1 failure variant.
3. Compare another firmware revision and device model.
4. Preserve unknown words in the decoder and logs rather than assigning names
   before evidence exists.

## 5. Touch variants and stale input

Known:

- Message type `6` carries one completed contact.
- Offsets 48 and 52 contain native-panel X/Y.
- Offset 36 contains the frame ID visible when the contact occurred.
- Quick tap, three-second hold, and slow drag each generated one record.
- A drag reported its initial coordinate; no down/move/up stream was observed.
- The test server sent no acknowledgement and touch reporting continued.

Hypotheses:

- Official VSS may send a response that enables richer reporting.
- Other firmware may use the currently zero words for event phase, contact ID,
  pressure, duration, or button/input subtype.
- Multiple contacts may produce multiple records or a larger payload.
- A touch whose frame ID is no longer current should be marked stale rather than
  applied to the new screen.

Experiments:

1. Repeat tap, hold, drag, and multi-touch through official VSS and compare both
   directions.
2. Test extreme corners, repeated contacts, and two simultaneous fingers.
3. Queue a new image while touching the previous frame and observe which frame
   ID is reported.
4. Define and test server policy for stale input: ignore, log, or route to the
   renderer state associated with the reported frame.
5. Do not invent down/move/up events unless a capture actually contains them.

## 6. Remaining status-field map

Known mappings cover only part of the 62-entry status table. Unknown entries are
preserved in `status_json` and must remain available for later comparison.

Likely categories among the unknown fields include:

- Battery voltage, charging state, and power source.
- Wi-Fi signal strength, connection state, or retry counters.
- Hardware, bootloader, radio, and display-controller revisions.
- Front-light or display-mode state.
- Uptime, reset reason, or boot counter.
- Additional environmental or air-quality measurements.

These categories are guesses, not field assignments.

Confirmed humidity mapping:

- Field 13 is temperature in degrees Celsius.
- Field 15 is relative humidity in integer percent on the verified Joan 6.
- Observed pairs were `29°C/28%`, `23°C/39%`, `24°C/43%`, `23°C/41%`, and a
  later live API reading of `23°C/42%`.
- Joan states that its displays contain temperature and humidity sensors.
- The server now decodes, stores, samples, and exposes field 15 as `humidity` in
  its native/compatibility APIs and management UI.

Experiments:

1. Charge and discharge the tablet while recording all fields.
2. Vary Wi-Fi signal strength in controlled steps.
3. Toggle front light and supported configurator settings one at a time.
4. Reboot the device and identify counters or reset-reason changes.
5. Compare official VSS device/status APIs with a simultaneous raw PV3 capture.

## 7. Frame-ID generation

Known:

- VSS image identifiers did not match ordinary CRC32 calculations tried over
  captured pixels or message bodies.
- The server's opaque nonzero 32-bit generated IDs are accepted by Joan 6
  firmware 7.4.4407 and later echoed as display state.
- Touch records echo the visible frame ID.

Hypotheses:

- VSS uses a content hash, database identifier, monotonic value, random value,
  or checksum with an untested byte range/seed.
- The tablet treats the field purely as an opaque correlation token.

This is low priority for the verified firmware because arbitrary server IDs work.
It becomes relevant only if another firmware rejects them or if VSS-compatible
identity across servers is required.

## 8. Other image and device capabilities

Unobserved areas include:

- Multiple display/screen IDs.
- Multiple image primitives and mixed encodings.
- Rotation or mirroring performed by the tablet rather than the server.
- Front-light commands.
- Button, generic input, GPS, file-transfer, and device-control messages.
- Server-driven configuration, reboot, and diagnostics.
- Protocol encryption and key negotiation.
- Bootloader status, firmware metadata, transfer, verification, and rollback.

These should be investigated only for a concrete use case. Bootloader and
firmware experiments require a separate safety plan because a malformed update
could make the device unusable.

## 9. Other models and firmware

All current physical acceptance claims are limited to Joan 6 firmware 7.4.4407.
Before claiming broader PV3 support:

1. Capture another Joan 6 firmware revision.
2. Capture Joan 6 Pro, Joan 13, or another Visionect PV3 display.
3. Confirm resolution, orientation, touch transform, pixel packing, rectangle
   flags, status fields, and acknowledgement shapes.
4. Move Joan 6-specific behavior into explicit per-model capabilities.
5. Preserve unknown variants rather than silently applying Joan 6 constants.

## Recommended discovery order

1. Partial 1-bit rectangle updates and refresh flags.
2. Wake, sleep, check-in, and connection lifetime.
3. Interrupted-transfer and reconnect recovery.
4. Remaining unmapped telemetry fields.
5. Official-VSS touch comparison and stale-frame policy.
6. Another Joan firmware/model.
7. Encryption, device commands, and firmware only when required.

The first two items have the greatest practical value for a low-power clock:
partial updates reduce refresh cost, while wake/check-in behavior determines how
closely the displayed time can follow wall-clock minute boundaries.

## External references

- Joan 6 technical specifications, including advertised full and partial
  refresh performance:
  `https://support.getjoan.com/knowledge/joan-6-technical-specifications`
- Joan Air Quality Insights, confirming temperature and humidity sensing:
  `https://support.getjoan.com/knowledge/air-quality-insights-in-joan-analytics`
