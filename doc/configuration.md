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
./joan-server --config /etc/eink-server.toml --http-listen 127.0.0.1:9090
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
```

An example file is included as `eink-server.example.toml`.

| Setting | Default | Command-line override | Meaning |
| --- | --- | --- | --- |
| `device_listen` | `:11113` | `--device-listen` | TCP address for tablet connections. |
| `http_listen` | `:8080` | `--http-listen` | HTTP address for the UI and REST API. |
| `database` | `./data/eink.db` | `--database` | SQLite database path. Relative paths use the process working directory. |
| `log_format` | `text` | `--log-format` | `text` for human-readable logs or `json` for structured logs. |

An empty file uses all defaults. To listen only on the local machine, use
`127.0.0.1:PORT`; an address beginning with `:` listens on all available
interfaces.

Only stable operational settings are configurable for now. Protocol behavior,
image encoding, timeouts, and storage schema remain code-defined so a small
installation cannot accidentally select an unsupported tablet mode. These can
be promoted to configuration later when a concrete deployment needs them.
