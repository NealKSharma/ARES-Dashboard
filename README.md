# ARES Telemetry Dashboard

A real-time telemetry dashboard for Affordable Rocket Engineering Services (ARES) launch vehicles. A Go backend reads Featherweight GPS tracker packets over serial, records CSV logs, and streams telemetry over WebSockets to a JavaScript frontend built with Leaflet and Chart.js.

## Features

- **Map tracking:** Vehicle position and flight path, with the last known valid position restored on reconnect.
- **Telemetry display:** Altitude, horizontal and vertical velocity, estimated vertical G, peak readings, GPS status, battery, and signal strength.
- **Customizable graphs:** Add and remove altitude, velocity, battery, and signal-strength graphs.
- **Flight monitoring:** GPS-based flight-phase estimates and a mission clock.
- **Platform reference:** Descriptions of Harmonia, Shelly, Scylla, and Phobos. Unconfirmed performance figures and dimensions appear as `--`. Platform selection changes the reference panel, not the connected tracker.
- **CSV logging:** Timestamped telemetry logs stored beside the executable.

## Getting Started

### Prerequisites

- [Go](https://golang.org/doc/install) 1.21 or later to run from source or build the executable.
- A Featherweight GPS tracker connected over serial for live telemetry. The dashboard can open without a tracker.

### Running Locally

1. Clone the repository:

   ```bash
   git clone https://github.com/NealKSharma/ARES-Dashboard.git
   cd ARES-Dashboard
   ```

2. Download dependencies:

   ```bash
   go mod download
   ```

3. Start the server:

   ```bash
   go run .
   ```

4. Open `http://127.0.0.1:8080` in a browser.

Auto-discovery connects only when exactly one serial port is available. If several ports are present, select the tracker explicitly:

```bash
go run . --port COM3
```

Replace `COM3` with the tracker's port. Configure the ground station for the intended tracker; the dashboard does not manage multiple rocket identities.

### Network Access

The server listens on `127.0.0.1:8080` by default. To share on a trusted LAN, set `--listen <local-LAN-IP>:8080`. Browser WebSockets require the same origin as the dashboard. There is no login or TLS; do not expose the server to the public Internet.

### Debug Modes

Print incoming lines beginning with `@`:

```bash
go run . --debug=gps
```

These are candidate telemetry packets; the prefix alone does not establish validity.

Print the complete incoming serial stream:

```bash
go run . --debug=all
```

### Building the Windows Executable

```bash
go build -o "Ares Dashboard.exe" .
```

The executable embeds the `public/` HTML, CSS, and JavaScript assets with `//go:embed`. Rebuild after changing source or web assets; editing `public/js/app.js` does not update an existing executable.

Copy the executable to the field laptop and run it directly. Logs are created in a `logs/` folder beside it. Map tiles and Google fonts require Internet access; offline map tiles are not included, and fonts have local fallbacks.

## Telemetry and Flight Monitoring

`GPS_STAT` uses feet and feet per second. The parser ignores GS/FND GPS packets, rejects GPS packets flagged `CRC_ERR`, and validates numeric GPS fields. It does not independently calculate the serial packet CRC.

Altitude and velocity displays, flight peaks, and flight graphs require a 3-D GPS fix. No-fix and 2-D samples show unavailable flight measurements and cannot set altitude tare. Valid 2-D positions can still appear on the map. Radio packets update signal and battery information without creating flight peaks or advancing the flight phase. Reconnect snapshots restore available readings and the last known valid map position without treating them as fresh flight samples.

Launch detection starts the mission clock and resets flight history while preserving the triggering altitude and velocity samples. Sparse telemetry can skip an unobserved coast phase and move from BOOST into observed descent, then landing. Flight phases and vertical G are estimates derived from GPS; they do not directly confirm motor burnout, parachute deployment, landing, or accelerometer measurements. Validate thresholds against representative tracker captures and actual flight profiles before field reliance.

Use **Tare altitude** to set the displayed altitude reference and **Clear data** to reset graphs, peaks, the map path, and flight state. These controls are available in PAD or LANDED state.

## Logging

Use Ctrl+C for normal shutdown so the logger drains queued samples and syncs the CSV. Forced termination, power loss, disk errors, and overloaded queues can lose data. Check the console for logging errors and dropped-sample counts.

When running through `go run`, logs are stored beside Go's temporary executable. Build and run the standalone executable for predictable field logging.

## Testing

Run checks from the repository root:

```text
go test ./...
go test -race ./...
go vet ./...
node --test browser.test.cjs
```

Browser tests execute the application logic with DOM, chart, and map stubs. They cover timestamps, invalid fixes, snapshots, radio/GPS separation, reconnects, resets, launch peaks, graph cleanup, platform descriptions, and regular and sparse synthetic flight sequences. Go tests cover packet parsing, invalid/non-tracker GPS, WebSockets, CSV escaping and write errors, and shutdown queue draining.

Automated checks supplement real-browser rendering checks and tests with the actual USB tracker and firmware.
