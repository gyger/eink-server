# SVG designs

SVG designs provide self-contained, interactive tablet screens. They can be
uploaded directly to a device, stored for reuse in SQLite, loaded from the
configured design directory, or selected from the built-in designs.

SVG designs use the tablet's `rendering` image setting. The shipped `eink`
mode renders at native panel resolution before four-bit conversion. Select
`smooth` for 3× supersampled output. Whole-pixel coordinates and strokes give
the most predictable E Ink result.
`default_rendering` chooses the value stored when a tablet first enrolls; it
does not retroactively overwrite existing per-tablet settings.

## Dynamic values

A `data-value` attribute on a `text` element replaces its text content during
rendering while leaving useful placeholder text for an SVG editor:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 758">
  <text x="40" y="80" data-value="${system.name}">eink-server</text>
  <text x="40" y="160" data-value="${device.temperature} °C">-- °C</text>
  <text x="40" y="240" data-value="${device.humidity}% RH">--% RH</text>
</svg>
```

Supported variables are `system.name`, `system.time`, `system.date`,
`system.locale`, `device.name`, `device.uuid`, `device.location`, `device.battery`, `device.temperature`,
`device.humidity`, `device.width`, `device.height`, `device.firmware`, and
`device.display_state`. `system.time` is the tablet-local time in `HH:MM`
format in the tablet's configured IANA timezone; `system.date` uses
`YYYY-MM-DD`. Unknown values reject the design. A new frame is queued only when a
value actually referenced by the active design changes.

## Calendar widget

The native calendar provider expands an empty `g` element into a localized
six-week month grid:

```svg
<g data-widget="calendar"
   id="main-calendar" data-navigation="true"
   data-x="520" data-y="55" data-width="460" data-height="570"
   data-week-start="monday" data-spillover="true"/>
```

The four geometry attributes are required. Week start is `monday` or `sunday`;
spillover is `true` or `false`. The provider emits `calendar-title`,
`calendar-weekday`, `calendar-day`, `calendar-outside`, `calendar-today`, and
`calendar-today-text` classes for styling. Calendar widgets depend on the
tablet-local date and locale, not on the clock text.

Calendar navigation is opt-in with `data-navigation="true"` and requires a
stable `id`. It adds understated previous/next controls with larger header tap
regions; tapping the title returns to the current month. Navigation is limited
to twelve months in either direction and returns to the current month five
minutes after the last tap.

## Fonts

Noto Sans and Noto Serif variable fonts are embedded in the server binary under
the SIL Open Font License. Generic `sans-serif` and `serif` SVG families map to
them, and an SVG without a family defaults to Noto Sans.

Additional font files can be placed in `font_directory`. When
`use_system_fonts` is enabled, the operating-system font cache is searched after
the embedded and application directories. Font directories are scanned at
startup; restart the server after adding or removing font files.

## Actions and regions

Use `data-action` on a rectangle, image, circle, or ellipse. Other SVG elements
must provide an explicit `data-hitbox="x y width height"`:

```svg
<rect id="lights" x="40" y="300" width="300" height="120"
      data-action="lights_on" data-region="primary-button"/>
<path d="…" data-action="details" data-hitbox="400 300 300 120"/>
```

`translate`, `scale`, and matrix transforms are applied to hit areas. When hit
areas overlap, the later element in document order wins. `data-region` records
a rectangular slot for future general-purpose Go or WASM providers. The
built-in calendar uses the separate `data-widget` contract.

Touch dispatch is tied to the frame ID reported by the tablet, so touches made
during a screen transition use the interaction map for the screen the user
actually saw. An unregistered action does nothing but is logged and published
as `action.unresolved` with the action name.

The frame interaction map uses a common recipient model. `data-action` targets
are delivered to configured webhooks. Widget-generated targets carry a provider,
instance ID, and event name and are delivered through the registered widget
handler. Both recipient types use the same transformed hit testing and frame
correlation. Widget handlers receive opaque persisted JSON state and can request
a new render; this is the integration boundary intended for future Go and WASM
providers. Widget targets cannot be declared by ordinary uploaded SVG source.

Webhook actions receive a JSON POST:

```json
{
  "action": "lights_on",
  "device_uuid": "…",
  "design_id": "db:room",
  "page_id": "main",
  "frame_id": 123,
  "element_id": "lights",
  "region": "primary-button",
  "x": 100,
  "y": 350,
  "timestamp": "2026-08-11T12:00:00Z"
}
```

## Sources and pages

Design IDs use `builtin:`, `file:`, and `db:` prefixes. The server includes
`builtin:status`, `builtin:touch-demo`, and `builtin:eink-verification`. New
tablets receive `builtin:status` by default; `default_design` can select another
design or be set to an empty string to disable automatic assignment. The status
design contains a device-local clock and the localized calendar widget.

`builtin:eink-verification` is a native 1024×758 diagnostic screen containing
all 16 protocol grayscale values, one- and two-pixel horizontal and vertical
patterns, diagonals, circles, isolated pixels, reversed text, and Noto Sans at
several sizes and weights. Photograph it close up on the tablet and compare it
with the server preview. The grayscale swatches are numbered by their packed
nibble value, where displayed source intensity is `n × 17`.
Filesystem loading is top-level and occurs at startup or through the reload
API.

Top-level groups may use `data-page="name"`. Shared root content and the first
page are rendered in this release; later pages are retained as valid SVG but
not displayed. Page navigation is reserved for a future action type.

A root SVG may request a whole-minute refresh interval from `1m` through `24h`,
for example `data-refresh="1m"`. The scheduler aligns work to interval
boundaries and renders connected tablets only. Dependency hashing suppresses
unchanged frames; reconnect and status handling render current values
immediately. Designs without the attribute remain event-driven.

## Security and limits

SVG source is limited to 2 MiB. Scripts, event attributes, `foreignObject`,
animation, external files, and network resources are rejected. Binary assets
may be embedded as `data:` URIs for PNG/JPEG images; the encoded bytes count
toward the same source limit. Fonts should use the embedded families or the
configured font directory. PDF, HTML, PostScript, and other document formats
are not supported.
