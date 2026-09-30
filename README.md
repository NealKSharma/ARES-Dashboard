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
