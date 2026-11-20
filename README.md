# ARES Telemetry Dashboard

A real-time telemetry dashboard for Affordable Rocket Engineering Services (ARES) launch vehicles. A Go backend identifies Featherweight GPS and StratoLoggerCF LoRa altitude streams on two USB ports, records CSV logs, and streams telemetry over WebSockets to a JavaScript frontend built with Leaflet and Chart.js.

## Features

- **Map tracking:** Vehicle position and flight path, with the last known valid position restored on reconnect.
- **Telemetry display:** Altitude, horizontal and vertical velocity, estimated vertical G, peak readings, GPS status, battery, and signal strength.
- **Customizable graphs:** Add and remove altitude, velocity, battery, and signal-strength graphs.
- **Flight monitoring:** Color-coded flight phases and a shared mission clock driven by fresh receiver measurements.
- **Platform reference:** Descriptions of Harmonia, Shelly, Scylla, and Phobos with catalog estimates for altitude and flight time. Missing figures appear as `--`. Platform selection changes the reference panel, not the connected tracker.
- **CSV logging:** Per-flight CSVs for each receiver (`logs/lora/` and `logs/featherweight_gps/`) beside the executable, from 5 s before launch to 5 s after landing.

## Documentation

- [Setup guide](docs/SETUP.md): install, clone, build, connect receivers and get ready for launch
- [Developer documentation](docs/DEVELOPMENT.md): how every file and feature works, tests, and how to start coding
- [Command reference](docs/COMMANDS.md): every command in one place

## Getting Started

### Prerequisites

- [Go](https://golang.org/doc/install) 1.21 or later to run from source or build the executable.
- A Featherweight GPS ground station and/or the LoRa ground receiver connected over USB for live telemetry. The dashboard can open without either.

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

Start the executable with no flags. Plug in either or both USB receivers in either order. Hover over each receiver indicator to see its port. Featherweight is identified by valid tracker packets. LoRa is identified by at least three complete decimal altitude records spanning at least 150 ms. Repeated values and negative heights are valid. Startup banners, range-test tables, and incomplete lines do not qualify. Use the production altitude receiver firmware. The PING/range sketches are diagnostics.

With LoRa and a 3-D Featherweight fix both live on the pad, the GPS reference sets itself. Each stationary GPS fix (up to 10 ft/s vertical and 15 ft/s horizontal) contributes GPS altitude minus the mean LoRa height over the last second. The reference is the mean of the last 10 such offsets and appears after 3. It stops updating at detected launch, so a LoRa dropout in flight falls back to GPS on the same reference. Without LoRa, wait for a fresh 3-D fix and click **Tare GPS at pad**. Manual tare is accepted once per receiver per running session, with a fresh 3-D fix and low motion (up to 10 ft/s vertical and 15 ft/s horizontal). Observed vertical speed over 60 ft/s in either direction or height over 100 ft from the pad locks calibration for the rest of the session. Start a new session at the pad for another flight. The backend shares that baseline with all browser windows for this running session. Restarting the executable requires a new pad tare. LoRa expects numeric feet above pad level, with CR, LF, or CRLF separators at a USB setting of 115200. The initial confirmation excludes the first OnPad ground-elevation record from the live altitude. Verify that the receiver forwards the original units. No LoRa tare is applied.

Fresh LoRa altitude takes priority. A closed USB reader or two seconds without a valid altitude makes LoRa ineligible. The backend checks freshness every 100 ms. Fresh, tared 3-D GPS (less than three seconds old) can take over, otherwise the height is unavailable. Restored LoRa must qualify again after silence. The vertical speed is a slope over recent LoRa heights, or Featherweight upward velocity during GPS fallback. Timing assumes the ground receiver emits a new line only for a newly received radio sample. Bench-check the windows against the actual stream.

When multiple devices are actively streaming the same recognizable format, that role shows an ambiguity and resumes automatically once only one matching stream is fresh. A lone unrelated device streaming plain numbers is indistinguishable from this LoRa protocol. Disconnect unrelated numeric USB streams for the bench test. The optional legacy `--port COM3` pins only Featherweight. Other ports are still checked for LoRa.

### Network Access

The server listens on `127.0.0.1:8080` by default. To share on a trusted LAN, set `--listen <local-LAN-IP>:8080`. Browser WebSockets require the same origin as the dashboard. There is no login or TLS; do not expose the server to the public Internet.

### Debug Modes

Print each valid Featherweight packet:

```bash
go run . --debug=gps
```

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

Featherweight altitude and velocity require a fresh 3-D GPS fix. LoRa altitude works independently. No-fix and 2-D samples show unavailable flight measurements and cannot set altitude tare. Valid 2-D positions can still appear on the map. Radio packets update signal and battery information without creating flight peaks or advancing the flight phase. Reconnect snapshots restore available readings and the last known valid map position without treating them as fresh flight samples.

The header shows the laptop's local time (24-hour) while waiting for launch. Detected ascent starts `T+`; the clock freezes at estimated landing. LoRa alone supports PAD, ASCENT, APOGEE, DESCENT and LANDED. GPS vertical velocity supports additional estimated phases. Motor and parachute events cannot be confirmed from these streams. LANDED means the active altitude source stayed within a 20 ft spread for 5 s, inside 30 ft of pad level, with no dropout or source change during that time. Source changes and dropouts break the trace between yellow markers on the altitude and vertical velocity graphs. The server retains the header across page reloads. Clearing local chart history preserves the mission clock. On each graph, hovering within the plot selects a reading by its horizontal position.

Without LoRa, use **Tare GPS at pad** to set the Featherweight altitude reference (with LoRa live it is set automatically). Use **Clear data** to reset graphs, peaks, the map path, and flight state. Clear is available at the pad or after local landing detection. The server enforces GPS tare eligibility across all clients.

## Logging

The browser averages each plotted measurement over 500 ms of receiver time (about ten LoRa readings on this setup). Live altitude and climb use the same means. Source switches, reconnects and data loss break the averaging window. Peaks retain raw extrema.

The flight recorder starts armed. On the pad it keeps the last five seconds of raw rows (up to 2,048) in memory and creates no file. Recording starts when the mission header detects launch, the same moment the phase leaves PAD. Either confirmed LoRa altitude or advancing 3-D Featherweight GPS can trigger it. The detector looks for a 20 ft gain above two recent readings within a 2.5 s window, followed by at least three qualifying samples spanning 150 ms and 5 ft further gain from the first qualifying sample. The file starts with the five-second bank, records every raw row through the flight, and closes five seconds after detected landing, even if the receivers go silent. The indicator then reads `CSV saved · flight complete`. The graphs follow the same window: they run live on the pad, clear at launch and restart from the five-second bank, then stop adding points five seconds after landing. Restart the executable for a new flight.

Each flight writes two CSVs with the same timestamp in their names: `logs/lora/ARES_LoRa_<time>.csv` and `logs/featherweight_gps/ARES_FeatherweightGPS_<time>.csv`. Every row starts with `ReceivedUTC`, `EventID`, `Event`, `Phase`, `Port` and `Record` (the exact serial line). The LoRa file adds `Altitude(ft)`. The GPS file adds tracker time, position, altitude, velocities, satellites and fix on position rows, RSSI and battery on radio rows, and the GPS pad reference and whether it came from LoRa auto-tare or a manual tare. Both files end with the dashboard's selected source, selected altitude, climb rate and reason. Status changes with no receiver input, such as a LoRa dropout or a GPS tare, appear in both files so each one shows when the selected source changed. Use `ReceivedUTC` to line the two files up.

Numeric lines received before device identification appear as `NUMERIC_PROBE`. After a known receiver goes silent, early recovery values appear as `LORA_REQUALIFY`. These rows have no display event ID and retain the preceding display snapshot. A probe can contain launch-site elevation or belong to an unrelated device. Only confirmed `LORA_ALT` events are eligible for displayed altitude.

The CSV indicator reports writer state. GPS and LoRa counts advance after a batch is written and synced; updates normally arrive in five seconds. GPS counts include receiver radio packets. Numeric probe and status-only rows have no confirmed receiver attribution and are excluded from the counts. A write error is shown even when the receivers keep sending. Duplicate or older GPS epochs are logged as GPS_NONADVANCING; they cannot renew fallback freshness. The tracker clock handles midnight and remembers the last identified tracker across USB reconnects.

The altitude and vertical velocity charts mark source transitions and gaps. Open `/?simulate=1` for a browser-only preview with a simulated LoRa outage and recovery; this page does not open a live telemetry connection or write flight samples.

Use Ctrl+C for normal shutdown to drain queued rows and sync the CSV. A slow disk applies backpressure to the bounded input queue. Forced termination, disk failure or upstream serial-buffer overflow can still lose samples. Check the console for disk errors.

When running through `go run`, logs are stored in the working directory’s logs folder. Build and run the standalone executable for predictable field logging.

## Testing

Run checks from the repository root:

```text
go test ./...
go test -race ./...
go vet ./...
node --test Testing/browser.test.cjs
```

Backend tests live in `main_test.go` (Go requires them beside the source files). Browser tests live in `Testing/browser.test.cjs` and execute the application logic with DOM, chart, and map stubs. They cover timestamps, invalid fixes, snapshots, radio/GPS separation, reconnects, resets, launch peaks, graph cleanup, platform descriptions, and regular and sparse synthetic flight sequences. Go tests cover packet parsing, invalid/non-tracker GPS, WebSockets, CSV escaping and write errors, and shutdown queue draining.

Automated checks supplement real-browser rendering checks and tests with the actual USB tracker and firmware.
