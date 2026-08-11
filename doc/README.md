# EInk Tablet Server documentation

This directory documents the replacement server implemented under `server/`.
It is intentionally narrower than Visionect Software Suite: it provides direct
image delivery and device status for a small trusted-LAN installation without a
browser renderer, Redis, Postgres, or a multi-user administration system.

## Documents

- [Architecture](architecture.md) — components, data flow, persistence, and
  concurrency model.
- [PV3 protocol](protocol.md) — observed framing, status, responses, image
  encoding, acknowledgements, and current protocol limits.
- [Protocol discovery backlog](protocol-discovery.md) — remaining hypotheses,
  evidence gaps, and proposed capture experiments.
- [HTTP API](api.md) — native REST API, processing options, SSE, and legacy
  compatibility routes.
- [Configuration](configuration.md) — TOML format, defaults, discovery, and
  command-line overrides.
- [Development and testing](development.md) — Fedora Toolbox workflow, test
  commands, fixtures, and project structure.
- [Tablet setup and validation](tablet-validation.md) — connecting a Joan,
  first-image validation, troubleshooting, and safety notes.
- [Next steps](next-steps.md) — prioritized roadmap and completion criteria for
  physical validation, protocol hardening, touch, rendering, and deployment.

## Supported scope

The verified target is the captured Joan 6 running firmware `7.4.4407` at
1024×758. Device identity and dimensions are parsed dynamically, so other PV3
models may work, but they are not yet claimed as verified.

Implemented:

- PV3 status and heartbeat exchange.
- Auto-enrollment and current telemetry.
- PNG/JPEG upload, processing, persistence, and delivery.
- Per-device and broadcast assignments.
- Native REST API, event history, SSE, and embedded UI.
- A small compatibility layer for image-oriented VSS clients.

Not implemented:

- Touch-driven screens (touch packets are decoded and logged).
- Partial display updates or waveform selection.
- HTML/URL rendering and VSS sessions/apps.
- Device configuration, reboot, sleep, firmware, or bootloader updates.
- PV3 encryption, public-internet security, or multi-user access.
