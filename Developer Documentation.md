# ARES Telemetry Dashboard - Developer Documentation

This document outlines the architecture and implementation details of the ARES Telemetry Dashboard to assist developers and contributors.

## 1. System Architecture

The dashboard is split into a Go backend and a vanilla JavaScript frontend. 
- **Backend (`main.go`)**: Handles serial port communication, telemetry parsing, websocket broadcasting, and CSV file logging.
- **Frontend (`app.js`)**: Handles live physics calculations, the flight state machine, DOM rendering, and charting.

Communication between the two systems is done via JSON payloads over a single bidirectional websocket connection.

## 2. Backend Implementation (`main.go`)

### Serial Parsing
The backend uses `go.bug.st/serial` to connect to the Featherweight GPS tracker. 
The parsing logic (`extractAfter`, `extractVal`, `indexOf`) dynamically splits incoming lines by whitespace and searches for specific keys (e.g., `Alt`, `Vel`, `lt`). This approach prevents the parser from crashing or reading incorrect data if the GPS momentarily loses a lock and drops fields.

All incoming telemetry data is mapped to a `currentData` struct. This struct is protected by a `sync.RWMutex` to prevent race conditions during websocket broadcasts.

### WebSockets
`handleBroadcasts` iterates through connected browser clients and sends the latest telemetry JSON. 
To prevent a slow or disconnected client from hanging the server, the broadcast logic copies the active client list into a local slice, releases the global mutex, and applies a strict 1-second write deadline to the network socket. 
On initial connection, the server immediately pushes a `Snapshot: true` JSON payload to provide the UI with the most recently known tracker state without falsely triggering a "new packet" connection event.

### CSV Logging
The `csvLogger` buffers telemetry data in memory and flushes it to disk (`logs/ARES_FlightLog_*.csv`) every 5 seconds to minimize disk I/O.
Logging begins continuously the moment the Go server initializes to ensure no pre-flight data is lost. The logs folder is automatically generated relative to the `os.Executable()` path, making the compiled binary fully portable.

## 3. Frontend Implementation (`app.js`)

### Physics Engine
The Featherweight tracker does not output live G-force. `app.js` calculates this dynamically in `updateUI` by taking the change in vertical velocity over the timestamp delta (`dt`) between packets.
To accurately reflect physical resting states, this coordinate acceleration is transformed via `(accel / 32.174) + 1.0` to yield Net Acceleration (where 1.0 G equals sitting on the pad).

### Flight Phase State Machine
The `checkFlightPhase` function manages a deterministic state machine:
- **PAD:** The default state. On liftoff, it stores the ground's MSL altitude as `padRawAlt`.
- **Liftoff:** If Net Accel exceeds 3.0 G or vertical velocity exceeds 60 ft/s, it transitions to `BOOST`. This transition immediately calls `clearAllData()` to wipe pre-flight graph history.
- **Subsequent Phases:** The state machine continues evaluating kinematic thresholds to progress through `COAST`, `APOGEE`, `DROGUE`, `MAIN`, and `LANDED`. The `LANDED` state determines touchdown by subtracting `padRawAlt` from current altitude to yield AGL (Above Ground Level).

### Peak Tracking
The frontend natively caches peak values for altitude, velocities, and G-force in local variables (`maxAlt`, `maxVel`, etc.) and displays them. Calling `clearAllData()` resets these trackers.

## 4. UI Layout and Rendering

- **Grid Layout:** The tactical table uses a CSS Grid to strictly align labels and values. This prevents horizontal shifting as decimal lengths change. If no data is available on boot, the JavaScript injects `--` placeholders to maintain column widths.
- **Charting:** Powered by Chart.js. Users can dynamically add line graphs. To prevent browser memory exhaustion, arrays are hard-capped at 10,000 data points using array shifting.
- **Mapping:** Powered by Leaflet.js. The coordinates are appended to a polyline array, and the map automatically pans to keep the active marker centered. Null-island `0,0` coordinates are ignored.
