# Roadmap

The basic path is working on a Joan 6 tablet running firmware 7.4.4407: status,
telemetry, full and changed-rectangle images, acknowledgements, delivery
confirmation, touch events, SVG actions, and the dynamic status dashboard.

## Reliability and packaging

- Exercise offline queueing, restart recovery, interrupted transfers, rapid
  replacement uploads, and multiple simultaneous tablets.
- Reconcile the desired frame after reconnect when the tablet reports a
  different display-state ID.
- Add release version information, reproducible Linux builds, a systemd service
  example, and SQLite backup/restore instructions.
- Add explicit retry and delivery diagnostics to the management page.

## Protocol discovery

- Identify the fast 1-bit partial-refresh encoding or waveform and determine
  whether periodic full refreshes are needed to control ghosting.
- Confirm heartbeat units and any supported sleep, wake, redraw, or reboot
  commands from official captures.
- Test acknowledgement, touch, telemetry, frame-ID, orientation, and image
  behavior on other firmware and display models before generalizing constants.
- Preserve unknown records and fields rather than assigning semantics without
  captured evidence. See [protocol discovery](protocol-discovery.md).

## Designs and integrations

- Add page navigation while retaining frame-correlated interaction maps.
- Define a bounded provider contract for `data-region`; use a native Go
  provider before introducing a WASM runtime or ABI.
- Add scheduler jitter and configurable refresh limits if deployments use many
  tablets.
- Improve previews and per-device event history in the embedded UI.

## Security

The current server is intended for a trusted LAN. Before broader exposure, add
real administrator authentication, CSRF protection, TLS or a documented reverse
proxy, request limits, and an explicit tablet allow-list. The compatibility
`/login` cookie is not authentication.
