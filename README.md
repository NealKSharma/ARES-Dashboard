# ARES Telemetry Dashboard

A real-time telemetry dashboard for Affordable Rocket Engineering Services (ARES) launch vehicles. 

Built with a Go backend that reads live telemetry data from a Featherweight GPS tracker over a serial port, and a responsive, dark-mode web frontend using WebSockets, Leaflet.js, and Chart.js.

## Features
- **Real-time Map Tracking**: Live GPS tracking with a pulsating beacon and flight path rendering.
- **Dynamic Telemetry Graphs**: Visualize Altitude, Velocity, Battery, and Signal Strength.
- **Platform Specs**: Quick reference for Harmonia, Shelly, Scylla, and Phobos rockets.

## Getting Started

### Prerequisites
- [Go](https://golang.org/doc/install) (1.20+)
- A Featherweight GPS tracker connected via USB (optional for viewing, required for live data).

### Running Locally
1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/ARES_Dashboard.git
   cd ARES_Dashboard
   ```

2. Download Go dependencies:
   ```bash
   go mod download
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
  *Only prints valid, human-readable telemetry packets (filters out binary garbage).*

- **Raw Stream Mode**
  ```bash
  go run main.go --debug=all
  ```
  *Prints absolutely every byte coming over the USB port.*

### Compiling for Production
To create a standalone executable that you can easily double-click on launch day without needing to use the terminal:

```bash
go build -o "ARES Dashboard.exe" main.go
```

**Important:** When moving or running the compiled `ARES Dashboard.exe`, you must always ensure the `public/` folder is in the exact same directory as the `.exe`, as it contains all the necessary fonts, scripts, and layout files for the dashboard to render!
