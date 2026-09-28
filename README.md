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
