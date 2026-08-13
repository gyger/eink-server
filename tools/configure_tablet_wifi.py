#!/usr/bin/env python3
"""Cross-platform Textual UI for configuring Joan tablet Wi-Fi over USB."""

from __future__ import annotations

import ipaddress
import time

try:
    import serial
    from serial.tools import list_ports
    from textual import work
    from textual.app import App, ComposeResult
    from textual.containers import Horizontal, Vertical
    from textual.widgets import Button, Checkbox, Footer, Header, Input, Label, RichLog, Select, Static
except ImportError as exc:
    raise SystemExit(
        "Dependencies are missing. Install them with:\n"
        "  python -m pip install -r tools/requirements-serial.txt"
    ) from exc


BAUD_RATE = 115200
QUIET_SECONDS = 0.4
COMMAND_TIMEOUT = 4.0


def serial_ports() -> list[tuple[str, str]]:
    ports = sorted(list_ports.comports(), key=lambda item: item.device.lower())
    usb_ports = [
        port
        for port in ports
        if port.vid is not None
        or "ttyUSB" in port.device
        or "ttyACM" in port.device
        or port.device.upper().startswith("COM")
    ]
    if usb_ports:
        ports = usb_ports
    return [
        (f"{port.device} — {port.description or 'Serial device'}", port.device)
        for port in ports
    ]


def validate_cli_token(label: str, value: str) -> str:
    if not value:
        raise ValueError(f"{label} must not be empty.")
    if any(character.isspace() for character in value):
        raise ValueError(
            f"{label} contains whitespace, which the confirmed tablet CLI format cannot safely represent."
        )
    return value


def read_until_quiet(port: serial.Serial, quiet_seconds: float = QUIET_SECONDS) -> bytes:
    output = bytearray()
    deadline = time.monotonic() + COMMAND_TIMEOUT
    quiet_deadline = time.monotonic() + quiet_seconds
    while time.monotonic() < deadline:
        waiting = port.in_waiting
        if waiting:
            output.extend(port.read(waiting))
            quiet_deadline = time.monotonic() + quiet_seconds
        elif time.monotonic() >= quiet_deadline:
            break
        else:
            time.sleep(0.03)
    return bytes(output)


def exchange(port: serial.Serial, command: str, quiet: float = QUIET_SECONDS) -> bytes:
    port.reset_input_buffer()
    port.write(command.encode("utf-8") + b"\r\n")
    port.flush()
    return read_until_quiet(port, quiet)


class WifiConfigurator(App[None]):
    TITLE = "Joan Wi-Fi Configurator"
    SUB_TITLE = "USB serial provisioning"
    CSS = """
    Screen { align: center middle; }
    #panel { width: 72; height: auto; border: round $accent; padding: 1 2; }
    #intro { margin-bottom: 1; color: $text-muted; }
    Label { margin-top: 1; }
    Input, Select { width: 100%; }
    #actions { margin-top: 2; height: 3; align-horizontal: right; }
    #actions Button { margin-left: 1; }
    #status { margin-top: 1; min-height: 1; }
    #log { margin-top: 1; height: 10; border: solid $surface-lighten-2; }
    """
    BINDINGS = [("q", "quit", "Quit"), ("ctrl+r", "refresh_ports", "Refresh ports")]

    def compose(self) -> ComposeResult:
        ports = serial_ports()
        with Vertical(id="panel"):
            yield Static(
                "Connect the tablet over USB. Configuration is persisted with flash_save; "
                "only the verified WPA2-PSK / band-selector-0 mode is used.",
                id="intro",
            )
            yield Label("Serial port")
            yield Select(ports, prompt="Select the tablet serial port", id="port")
            yield Label("Wi-Fi SSID")
            yield Input(placeholder="Network name", id="ssid")
            yield Label("Wi-Fi password")
            yield Input(placeholder="Password", password=True, id="password")
            yield Label("Repeat password")
            yield Input(placeholder="Repeat password", password=True, id="password-repeat")
            yield Label("Server IPv4 address (optional)")
            yield Input(placeholder="Leave blank to keep the current server", id="server-ip")
            yield Label("Server TCP port")
            yield Input(value="11113", placeholder="11113", type="integer", id="server-port")
            yield Checkbox(
                "Disable application-level outbound encryption",
                value=True,
                id="disable-encryption",
            )
            yield Checkbox("Save without rebooting", id="no-reboot")
            yield Static("", id="status")
            yield RichLog(id="log", markup=True, wrap=True)
            with Horizontal(id="actions"):
                yield Button("Refresh ports", id="refresh", variant="default")
                yield Button("Write configuration", id="write", variant="primary")
                yield Button("Quit", id="quit", variant="error")
        yield Header()
        yield Footer()

    def on_mount(self) -> None:
        if not serial_ports():
            self.set_status("No serial ports detected. Connect the tablet, then refresh.", error=True)

    def set_status(self, message: str, *, error: bool = False) -> None:
        style = "bold red" if error else "bold green"
        self.query_one("#status", Static).update(f"[{style}]{message}[/]")

    def log_line(self, message: str) -> None:
        self.query_one("#log", RichLog).write(message)

    def finish_configuration(self, message: str, *, error: bool = False) -> None:
        self.set_status(message, error=error)
        self.query_one("#write", Button).disabled = False

    def action_refresh_ports(self) -> None:
        self.refresh_ports()

    def refresh_ports(self) -> None:
        widget = self.query_one("#port", Select)
        widget.set_options(serial_ports())
        widget.clear()
        count = len(serial_ports())
        self.set_status(f"Found {count} serial port{'s' if count != 1 else ''}.", error=count == 0)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "quit":
            self.exit()
        elif event.button.id == "refresh":
            self.refresh_ports()
        elif event.button.id == "write":
            self.start_configuration()

    def start_configuration(self) -> None:
        port_value = self.query_one("#port", Select).value
        ssid = self.query_one("#ssid", Input).value
        password = self.query_one("#password", Input).value
        repeated = self.query_one("#password-repeat", Input).value
        server_ip = self.query_one("#server-ip", Input).value.strip()
        server_port_text = self.query_one("#server-port", Input).value.strip()
        try:
            if port_value is Select.BLANK:
                raise ValueError("Select a serial port.")
            ssid = validate_cli_token("SSID", ssid)
            password = validate_cli_token("Password", password)
            if password != repeated:
                raise ValueError("Passwords do not match.")
            server_port: int | None = None
            if server_ip:
                try:
                    parsed_ip = ipaddress.ip_address(server_ip)
                except ValueError as exc:
                    raise ValueError("Server address must be a valid IPv4 address.") from exc
                if parsed_ip.version != 4:
                    raise ValueError("Server address must be IPv4; this tablet command has only been verified with IPv4.")
                if not server_port_text:
                    raise ValueError("Enter a server TCP port.")
                server_port = int(server_port_text)
                if not 1 <= server_port <= 65535:
                    raise ValueError("Server TCP port must be between 1 and 65535.")
        except ValueError as exc:
            self.set_status(str(exc), error=True)
            return
        self.query_one("#write", Button).disabled = True
        self.query_one("#log", RichLog).clear()
        self.set_status("Writing configuration…")
        self.configure(
            str(port_value),
            ssid,
            password,
            server_ip or None,
            server_port,
            self.query_one("#disable-encryption", Checkbox).value,
            self.query_one("#no-reboot", Checkbox).value,
        )

    @work(thread=True, exclusive=True)
    def configure(
        self,
        port_name: str,
        ssid: str,
        password: str,
        server_ip: str | None,
        server_port: int | None,
        disable_encryption: bool,
        no_reboot: bool,
    ) -> None:
        def report(message: str) -> None:
            self.call_from_thread(self.log_line, message)

        try:
            report(f"Opening [bold]{port_name}[/] at {BAUD_RATE} baud")
            with serial.Serial(
                port_name,
                BAUD_RATE,
                bytesize=serial.EIGHTBITS,
                parity=serial.PARITY_NONE,
                stopbits=serial.STOPBITS_ONE,
                timeout=0,
                write_timeout=3,
            ) as port:
                port.write(b"\r\n")
                port.flush()
                time.sleep(0.25)
                port.reset_input_buffer()
                report("Reading current configuration (response hidden because it may contain credentials)")
                exchange(port, "wifi_conf_get")
                report(f"Writing SSID [bold]{ssid}[/] with WPA2-PSK (password and response hidden)")
                exchange(port, f"wifi_conf_set {ssid} {password} wpa2 0")
                if server_ip is not None and server_port is not None:
                    report(f"Writing server endpoint [bold]{server_ip}:{server_port}[/]")
                    exchange(port, f"server_tcp_set {server_ip} {server_port}")
                if disable_encryption:
                    report("Disabling application-level outbound encryption")
                    exchange(port, "encryption_mode_set 0")
                report("Persisting settings to flash")
                exchange(port, "flash_save", quiet=1.0)
                report("Reading saved configuration (response hidden because it may contain credentials)")
                exchange(port, "wifi_conf_get")
                if server_ip is not None:
                    report("Reading saved server endpoint")
                    response = exchange(port, "server_tcp_get").decode(
                        "utf-8", errors="backslashreplace"
                    )
                    if response.strip():
                        report(response.strip())
                if disable_encryption:
                    report("Checking saved encryption configuration (response hidden because it may contain key material)")
                    exchange(port, "encryption_config_get")
                if not no_reboot:
                    report("Rebooting tablet")
                    exchange(port, "reboot", quiet=0.2)
            self.call_from_thread(self.finish_configuration, "Configuration saved successfully.")
        except (serial.SerialException, OSError, ValueError) as exc:
            self.call_from_thread(
                self.finish_configuration, f"Configuration failed: {exc}", error=True
            )


if __name__ == "__main__":
    WifiConfigurator().run()
