# ARES Telemetry Dashboard

A real-time telemetry dashboard for Affordable Rocket Engineering Services (ARES) launch vehicles. 

Built with a Go backend that reads live telemetry data from a Featherweight GPS tracker over a serial port, and a responsive, dark-mode web frontend using WebSockets, Leaflet.js, and Chart.js.

## Features
- **Real-time Map Tracking**: Live GPS tracking with a pulsating beacon and flight path rendering.
- **Dynamic Telemetry Graphs**: Visualize Altitude, Velocity, Battery, and Signal Strength.
- **Platform Specs**: Quick reference for Harmonia, Shelly, Scylla, and Phobos rockets.

## Getting Started

### Prerequisites
- [Go](https://golang.org/doc/install) (1.21+)
- A Featherweight GPS tracker connected via USB (optional for viewing, required for live data).

### Running Locally
1. Clone the repository:
   ```bash
   git clone https://github.com/ARES-Rocketry/ARES_Dashboard.git
   cd ARES_Dashboard
   ```

2. Download Go dependencies:
   ```bash
   go mod tidy
   ```

3. Start the server:
   ```bash
   go run main.go
   ```

4. Open your browser and navigate to `http://localhost:8080`.

### Debug Modes
If you are troubleshooting a hardware connection or want to view the raw data coming from the Featherweight tracker, you can launch the server with debug flags:

- **Clean Telemetry Mode**
  ```bash
  go run main.go --debug=gps
  ```
  *Only prints lines starting with `@` (valid GPS telemetry strings).*

- **Raw Stream Mode**
  ```bash
  go run main.go --debug=all
  ```
  *Prints every line received from the serial scanner.*

### Compiling for Production
To create a standalone executable for field use on launch day:

```bash
go build -o "ARES Dashboard.exe" main.go
```

Thanks to the new `//go:embed` implementation, the `public/` folder (HTML, CSS, JS) is entirely compiled directly into the binary! 
You can take `ARES Dashboard.exe` and drop it directly onto the desktop of any field laptop—no other files or folders are required. Flight logs will automatically generate in a `logs/` folder right next to wherever the executable is run.


## Reliability and review checks

The server now listens on `127.0.0.1:8080` by default. To deliberately share on a
trusted LAN, specify `--listen <local-LAN-IP>:8080`. There is no login or TLS;
do not expose this endpoint to the public Internet. Browser WebSockets require
the same origin as the dashboard.

Auto-discovery proceeds only when exactly one serial port is available. With
multiple ports, use `--port COM3` (substitute the actual tracker port). This
prevents different devices from silently merging into one rocket record.

Use Ctrl+C for normal shutdown: the logger drains queued samples and syncs the
CSV before exit. Forced termination, power loss, disk errors, and overloaded
queues can still lose data. Check the console for CSV errors and drop counts.
When using `go run`, logs are beside Go's temporary executable; build the
executable for durable, predictable field logging.

Map tiles and Google fonts still require Internet access; the embedded assets
do not include offline map tiles. Fonts have local fallbacks.

Flight phases and vertical G are estimates from GPS samples, not direct motor,
parachute, landing, or accelerometer measurements. Thresholds still require
validation against representative tracker captures and actual flight profiles.
GPS_STAT uses feet and feet/second per the vendor manual. The parser ignores
GS/FND GPS packets, rejects GPS packets flagged CRC_ERR, and validates numeric
GPS fields. It does not independently calculate the serial packet CRC.

Run automated checks from the repository root:

```text
go test ./...
go test -race ./...
go vet ./...
node --test browser.test.cjs
```

The browser tests execute the actual app logic with DOM/chart/map stubs. They
cover timestamp rollover, snapshots, radio/GPS separation, reconnects, resets,
graph cleanup, invalid positions, and a synthetic flight. They do not replace
real-browser rendering checks or USB tracker tests. The Go tests cover packet
parsing, invalid/non-tracker GPS, WebSockets, CSV escaping/errors, and draining
the log queue at shutdown.
