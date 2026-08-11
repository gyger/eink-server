# PV3 protocol notes

This implementation is based on captured traffic and fixtures under
`Discovery/codex/captures`. It does not treat TCP read boundaries as message
boundaries.

## Outer record

Every observed application record begins with five little-endian `uint32`
values:

| Offset | Meaning |
|---:|---|
| 0 | Packet type (`2` bootloader, `3` application) |
| 4 | Word 1, observed as zero |
| 8 | Word 2/direction (`0` tablet, `1` server) |
| 12 | Payload byte length |
| 16 | Check word |

Tablet records use CRC32 of the first 16 header bytes. Server records use CRC32
of the payload. The decoder reads exactly the declared payload length and rejects
payloads above 32 MiB.

## Status records

An application status payload contains:

```text
uint32  reserved
byte[16] device UUID
uint32  protocol version (3)
uint32  status kind
uint32  table byte length
[]entry fixed status table
uint32  trailer/check word
```

Each table entry is `[value uint32, fieldID uint32]`. Known fields currently
used by the server are:

| Field ID | Meaning |
|---:|---|
| 10 | Current display-state/frame ID |
| 11 | Battery percent |
| 13 | Temperature |
| 15 | Relative humidity percent |
| 17–19 | Firmware major, minor, revision |
| 29 | Heartbeat setting |
| 39 | Display width |
| 40 | Display height |

Unknown fields are preserved in the stored raw status map. These IDs come from
the checked-in 552-byte fixture and deliberately supersede older notes whose
field map was shifted.

## Status response

The logical server response is 44 bytes:

```text
uint32   reserved
byte[16] device UUID
uint32   constant 1
uint32   matching status kind
uint32   field/command 8
uint32   value 0
uint32   constant 1
uint32   reserved
```

The initial response is sent in raw-LZ4 chunks; subsequent heartbeat responses
are sent uncompressed, matching observed VSS traffic.

## Image message

The server sends a type-3 record containing an LZ4-chunked logical image:

| Logical offset | Meaning |
|---:|---|
| 4 | Device UUID |
| 20 | Message type `5` |
| 24 | Matching status kind |
| 36 | Opaque frame ID |
| 40 | Primitive count `1` |
| 56 | Image type `1` |
| 60 | Screen ID `0` |
| 64 | Width and height as two `uint16` values |
| 68 | Rectangle flags `0x102` |
| 72 | Encoding `4` |
| 76 | Packed pixel length |
| 80 | Packed pixels |

Encoding 4 stores two horizontal pixels per byte. The high nibble is the first
pixel, the low nibble the second, and each nibble maps to intensity `n × 17`.
The current encoder therefore requires an even display width.

The verified Joan 6 panel presents this raw framebuffer rotated by 180 degrees
relative to the intended screen orientation. Immediately before PV3 encoding,
the server reverses the packed byte order and swaps each byte's nibbles. This
native panel correction is separate from the user-configurable rotation: the UI
preview remains in the intended physical orientation, and `rotation=0` displays
upright on the tablet.

Logical data is split into chunks of at most 4,800 bytes. Each chunk has a
24-byte header containing its index, logical header size `80`, compressed size,
uncompressed size, flags, and reserved word. Normal chunks use raw LZ4. If noisy
data does not compress smaller, the server emits a valid LZ4 literal-only block.

The image checksum used by VSS did not match ordinary CRC32 calculations over
the captured pixels or message body. This server treats the field as an opaque,
server-generated frame identifier. A physical Joan 6 accepted such an ID and
later echoed it as display state, confirming delivery on firmware 7.4.4407.

## Delivery acknowledgement

The tablet sends an immediate, uncompressed 44-byte logical reply after each
image. It is a type-3 outer record with tablet-direction framing and this
payload:

| Logical offset | Meaning |
|---:|---|
| 0 | Reserved `0` |
| 4 | Device UUID (16 bytes) |
| 20 | Message type `1` (generic acknowledgement) |
| 24 | Echoed image/status sequence |
| 28 | Field/body size `8` |
| 32 | Value `0` |
| 36 | Constant `1` |
| 40 | Reserved `0` |

Two replies captured after consecutive VSS images were identical except for
the echoed sequence, which changed from `3` to `4` with the image message. The
reply does not contain the frame ID. Its precise `8, 0, 1` field semantics are
still unnamed, so it proves receipt/processing of the sequenced message but is
not used as proof that the panel refresh completed. Delivery is confirmed only
when a later full status reports the assignment's frame ID as display state.

The gateway dispatches this record by logical message type before attempting
status decoding, strictly validates all observed constants, and correlates the
echoed sequence with the assignment most recently sent using that sequence. A
match stores `acknowledged_at` and publishes `image.acknowledged`; it does not
change the assignment from `sent` to `delivered`.

## Touch event

On Joan 6 firmware 7.4.4407, a completed contact produces one uncompressed
76-byte type-3 tablet record (20-byte outer header and 56-byte logical payload):

| Logical offset | Meaning |
|---:|---|
| 0 | Reserved `0` |
| 4 | Device UUID (16 bytes) |
| 20 | Message type `6` (touch) |
| 24 | Sentinel `0xffffffff` |
| 28 | Event-body size `20` |
| 32 | Reserved/unknown `0` |
| 36 | Frame ID of the image being touched (`0` if no assigned frame is displayed) |
| 40 | Event word 1, observed `0` |
| 44 | Event word 2, observed `0` |
| 48 | Native-panel X coordinate |
| 52 | Native-panel Y coordinate |

The coordinates use the panel's native 180-degree-rotated space. For this
1024x758 Joan 6, intended physical coordinates are therefore
`x = 1023 - rawX`, `y = 757 - rawY`.

The frame ID at offset 36 was identified during a later live-server test. After
the server displayed frame `3946585767` (`0xeb3c1ea7`), every touch carried that
same value at offset 36. Earlier captures contained zero because display state
was zero. The server can use this field to associate input with the exact frame
the user saw and identify stale touches after a screen change.

Three isolated taps, a three-second hold, and a slow horizontal drag each
produced exactly one record. The hold carried no observed duration or phase
change. The drag reported its initial contact position and produced no movement
records. This firmware therefore exposes a completed contact/click abstraction,
not separate down, move, and up events in the tested configuration. The server
sent no response to these records and the tablet continued normally.

The gateway strictly decodes this record, exposes the variable frame ID,
validates the UUID and coordinate
bounds against the connected device's latest status, converts it to physical
coordinates, and writes one `touch event` entry containing the frame ID to the
service log. Touches are
not persisted, published through SSE, acknowledged, or passed to a renderer yet.

## Unsupported protocol areas

Bootloader type-2 connections are logged and closed. Button, file, other input,
GPS, command, firmware, encryption, partial rectangles, and device-control
messages are not decoded or generated.
