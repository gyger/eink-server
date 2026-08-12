# Configuration

The server uses a small TOML configuration file. TOML is readable when edited
by hand and avoids YAML's implicit type conversions and whitespace rules.

## File discovery and precedence

On startup, the server looks for `eink-server.toml` in the same directory as
the running executable. The automatically discovered file is optional. A
missing file or an empty/whitespace-only file uses all defaults.

Use `--config /path/to/config.toml` to select a different file. An explicitly
selected file must exist, though it may be empty. Relative paths passed to
`--config` are resolved from the process working directory.

Settings are applied in this order, with later sources taking precedence:

1. Built-in defaults.
2. Values present in the TOML file.
3. Explicit command-line flags.

For example, this loads a file but changes only its HTTP listener at runtime:

```sh
./eink-server --config /etc/eink-server.toml --http-listen 127.0.0.1:9090
```

## Format

All keys are optional. Unknown keys, malformed TOML, invalid listener
addresses, an empty database path, and unsupported log formats stop startup
with an error. This strict handling catches misspelled settings.

```toml
device_listen = ":11113"
http_listen = ":8080"
database = "./data/eink.db"
log_format = "text"
system_name = "eink-server"
design_directory = "./designs"
default_design = "builtin:status"
default_rendering = "eink"
default_timezone = "Europe/Berlin"
default_locale = "de-DE"
font_directory = "./fonts"
use_system_fonts = true
```

`default_timezone` must be an IANA timezone name. `default_locale` currently
accepts `de-DE` and `en-GB`. They are persisted when a tablet enrolls and may
then be changed per tablet.

An example file is included as `eink-server.example.toml`.

| Setting | Default | Command-line override | Meaning |
| --- | --- | --- | --- |
| `device_listen` | `:11113` | `--device-listen` | TCP address for tablet connections. |
| `http_listen` | `:8080` | `--http-listen` | HTTP address for the UI and REST API. |
| `database` | `./data/eink.db` | `--database` | SQLite database path. Relative paths use the process working directory. |
| `log_format` | `text` | `--log-format` | `text` for human-readable logs or `json` for structured logs. |
| `system_name` | `eink-server` | — | Value available to SVG designs as `${system.name}`. |
| `design_directory` | `./designs` | — | Optional directory scanned for top-level SVG designs. |
| `default_design` | `builtin:status` | — | Design assigned when an unknown tablet first enrolls; set to an empty string to disable. |
| `default_rendering` | `eink` | — | Image rendering mode assigned when a tablet first enrolls: `eink` or `smooth`. |
| `default_timezone` | `Europe/Berlin` | — | IANA timezone assigned when a tablet first enrolls. |
| `default_locale` | `de-DE` | — | Calendar locale assigned when a tablet first enrolls: `de-DE` or `en-GB`. |
| `font_directory` | `./fonts` | — | Optional directory containing additional TTF, OTF, WOFF, or WOFF2 fonts. |
| `use_system_fonts` | `true` | — | Include operating-system font directories after embedded and application fonts. |

Named webhook actions may also be registered in TOML. They are synchronized to
SQLite at startup and override same-named actions created through the API:

```toml
[actions.lights_on]
type = "webhook"
url = "http://automation.local/hooks/lights-on"
timeout = "5s"

[actions.lights_on.headers]
Authorization = "Bearer secret"
```

Timeouts must be positive and no longer than 30 seconds. Header values are
stored in SQLite and redacted by the management API, but the configuration and
database should still be protected as secrets.

`default_rendering` affects newly enrolled tablets. Their selected value is
then stored in SQLite and can be changed independently through the management
page or device API. `eink` uses native-resolution SVG rendering and the E Ink
pixel-preparation path; `smooth` retains 3× supersampling.

An empty file uses all defaults. To listen only on the local machine, use
`127.0.0.1:PORT`; an address beginning with `:` listens on all available
interfaces.

Only stable operational settings are configurable for now. Protocol behavior,
image encoding, timeouts, and storage schema remain code-defined so a small
installation cannot accidentally select an unsupported tablet mode. These can
be promoted to configuration later when a concrete deployment needs them.
