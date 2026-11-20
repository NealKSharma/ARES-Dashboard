# ARES Dashboard Developer Documentation

This document explains how the dashboard works, file by file, and why it is built the way it is. Read it before changing code. For first-time installation see [SETUP.md](SETUP.md); for every command see [COMMANDS.md](COMMANDS.md).

- Repository: https://github.com/NealKSharma/ARES-Dashboard
- Language: Go 1.21+ backend, plain JavaScript frontend (no build step)
- Libraries: `github.com/gorilla/websocket`, `go.bug.st/serial`, Chart.js 4.5.1, Leaflet

---

## 1. Getting started as a developer

### Install

| Tool | Why | Get it |
|---|---|---|
| Git | Clone and version the code | `winget install --id Git.Git -e` or https://git-scm.com/downloads |
| Go 1.21+ | Build and test the backend | `winget install --id GoLang.Go -e` or https://go.dev/dl/ |
| Node.js 18+ | Run the browser tests | `winget install --id OpenJS.NodeJS.LTS -e` or https://nodejs.org/ |
| Arduino IDE | Flash the bench simulator board | `winget install --id ArduinoSA.IDE.stable -e` or https://www.arduino.cc/en/software |
| VS Code + Go extension (optional) | Editing, formatting on save, test buttons | https://code.visualstudio.com/ |

### First run

```powershell
git clone https://github.com/NealKSharma/ARES-Dashboard.git
cd ARES-Dashboard
go mod download
go test ./...
node --test Testing/browser.test.cjs
go run .
```

Open http://127.0.0.1:8080. With no receivers plugged in, the page loads and shows "No receiver data yet". Open http://127.0.0.1:8080/?simulate=1 for a fake flight that exercises the UI.

### Everyday loop

1. Make a branch: `git checkout -b my-change`
2. Edit code. Run `gofmt -w .` for Go files.
3. Run `go vet ./...`, `go test ./...` and `node --test Testing/browser.test.cjs`.
4. Rebuild the exe (`go build -o "Ares Dashboard.exe" .`) and try it with real receivers or the simulator board.
5. Commit and push.

**The web page is embedded in the exe** with `//go:embed public`. Changing `public/js/app.js` does nothing to an existing exe. Rebuild.

---

## 2. What the system does

Two ground receivers plug into the laptop by USB:

- **Featherweight GPS ground station:** text packets with position, GPS altitude, velocity, fix and radio status.
- **LoRa ground receiver:** forwards the StratoLoggerCF altimeter's altitude as one number per line, about 20 per second, in feet above the pad.

The Go backend finds both receivers, decodes their lines, decides which altitude to show, tracks the flight phase, records CSVs during the flight and streams the live state to every open browser tab over a WebSocket. The browser renders it.

```
USB ports ──► serial.go ──► telemetry.go ──► currentData ──► server.go ──► browser (app.js)
 (1 reader       (lines)      (decode, pick     │  (shared       (WebSocket
  per port)                    altitude, tare)  │   state)        broadcast)
                                   │            │
                               flight.go        └──────────► recorder.go ──► logs/lora/*.csv
                             (phase, launch,                 (pre-roll,       logs/featherweight_gps/*.csv
                              landing)                        flight window)
```

### Design rules

- **One source of truth.** The backend decides the altitude source, the flight phase and the recording window. The browser and the CSVs consume the same decision, so two browser tabs and the log never disagree.
- **Never trust a single line.** Receivers are identified from what they actually send, not from COM numbers. Bad lines are dropped without touching state.
- **Freshness is measured by time, not by the port being open.** A LoRa receiver can stay plugged in while the rocket's radio goes silent. A 100 ms backend timer expires stale data even when nothing arrives.
- **Raw data is never averaged in the logs.** Averaging only happens in the browser for readability.

---

## 3. Project layout

```
ARES-Dashboard/
  main.go          startup and shutdown
  serial.go        USB port discovery and line reading
  telemetry.go     decoding, receiver roles, altitude selection, GPS pad reference
  flight.go        flight phases, ascent detection, landing detection
  recorder.go      pad pre-roll, flight window, per-receiver CSVs, recording status
  server.go        web page and WebSocket streaming
  main_test.go     all backend tests
  public/          the web page (embedded in the exe)
    index.html
    css/style.css, css/leaflet.css
    js/app.js            dashboard logic
    js/simulate_flight.js  /?simulate=1 preview
    js/chart.js, js/leaflet.js   vendored libraries
    assets/ARES Logo.png
  Testing/
    browser.test.cjs     browser logic tests (Node)
    usb_altitude_sim.ino bench simulator sketch
  docs/            this file, setup guide, command reference
  logs/            flight CSVs (not committed)
  go.mod, go.sum, .gitignore, README.md
```

All Go files are one package (`package main`). Go requires tests for `package main` to sit beside the source, which is why `main_test.go` lives in the root.

---

## 4. Backend, file by file

### 4.1 `main.go`: startup and shutdown

- Defines the flags: `--listen` (default `127.0.0.1:8080`), `--port` (pin the Featherweight to one COM port) and `--debug` (`gps` or `all`).
- Embeds `public/` into the binary with `//go:embed public`.
- Starts four things: the serial reader (`serialReader`), the WebSocket broadcaster (`handleBroadcasts`), the CSV recorder (`csvLogger`) and the HTTP server.
- **Shutdown order matters.** On Ctrl+C (or the console window closing, which Windows delivers as SIGTERM):
  1. Stop the serial readers and wait for them, so nothing new is produced.
  2. Set `stopping = true` under `dataMutex`, then close the CSV queue and the broadcast queue.
  3. Close all browser connections and wait for their handlers.
  4. Wait for the recorder to flush and sync the CSVs (`<-csvDone`).
  5. Shut down the HTTP server.

  Closing the queues before the producers stop would panic; closing them after guarantees every queued row reaches disk.

### 4.2 `serial.go`: finding ports and reading lines

**`discoverPorts()`** asks Windows for detailed port info. It skips Bluetooth ports and non-USB ports (unless `--port` names them) and builds an identity (`VID/PID/serial number`) when the device reports one. If detailed info fails it falls back to plain port names.

**`serialReader(ctx)`** runs two loops:
- Every **1 s** it rescans ports and calls `syncPorts`.
- Every **100 ms**, on its own goroutine, it calls `refreshLocked` so stale data expires on time even if a Windows driver call blocks the scan.

**`syncPorts()`** keeps exactly one reader per port. A new port gets a new `receiver` and a `listenPort` goroutine. A port that disappears is cancelled and removed. A reader that failed is retried after a backoff of 1, 2, 4, then 8 s. A different device appearing on the same COM name (different identity) replaces the old reader.

**`listenPort()`** opens the port at **115200 baud** with a **200 ms read timeout**. The timeout lets the goroutine notice cancellation without closing a Windows handle mid-read. It splits bytes on CR or LF into lines and hands each complete line to `receiveLine`. Lines over 64 KB are discarded. An unterminated tail at unplug is never decoded. If a port sends data for **12 s** without being identified, the reader closes it and retries later. An idle port that sends nothing is left open, because a ground receiver can sit quiet until the rocket's radio comes on.

**Why one goroutine per port:** a slow or hung device cannot delay the other receiver.

### 4.3 `telemetry.go`: decoding and altitude selection

#### Shared state

- `currentData` (`TelemetryData`) is the live state every browser and CSV row is built from. It and `receivers` are protected by **`dataMutex`**. Any function ending in `Locked` expects the caller to hold it.
- `TelemetryData` embeds `FlightView`, which embeds `missionHeader`. Its JSON tags define the WebSocket message (see [section 6](#6-websocket-protocol)).
- `inputRecord` describes the one serial line that caused an event. It is attached only to CSV rows.

#### Featherweight packets: `decodeGPS(line)`

Accepted formats:

```
@ GPS_STAT Time 12:00:00 Alt 840 lt 42.02 ln -93.64 Vel 10 0 5 Fix 3 # 9
@ GPS_STAT 203 2020 11 15 01:20:21.986 CRC_OK Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9
@ RX_NOMTK RSSI -80 trk_B_V 4000
```

- Fields are found by keyword (`Alt`, `lt`, `ln`, `Vel`, `Fix`, `#`), not position, so a missing field fails cleanly instead of shifting values.
- After `Vel`, the 1st number is horizontal speed and the 3rd is vertical speed (ft/s).
- Rejected: `GS` or `FND` packets (other trackers), `CRC_ERR`, non-numeric or infinite values, latitude beyond ±90, longitude beyond ±180, fix other than 0, 2 or 3, unparseable time.
- `RX_NOMTK` updates RSSI and battery only. It never changes position or altitude.

#### `receiveLine(r, line, now)`: the heart of the backend

For each line:

1. **Featherweight path.** If the port is not already LoRa and `decodeGPS` accepts the line, the port becomes the Featherweight.
   - A `GPS_STAT` only counts as a new measurement if its tracker time **advances** (`advancingFix`, which handles midnight). Repeated or older timestamps are logged as `GPS_NONADVANCING` and cannot keep GPS altitude "fresh". A repeat with a lost fix still invalidates GPS immediately.
   - The tracker clock is remembered across USB reconnects (`trackerClock`) so a replayed cached packet after reconnect is not treated as new.
   - Then the automatic GPS pad reference is updated (`autoTareLocked`).
2. **LoRa path.** Otherwise, the trimmed line must be one plain decimal number (`numericLine` regex, finite). Zero, negative and repeated values are valid.
   - The first readings are not trusted: a port only becomes LoRa after **3 readings spanning at least 150 ms**. This rejects one-off numbers, startup bursts and the StratoLogger's first `OnPad` ground-elevation line. Those early readings are still logged (`NUMERIC_PROBE`, or `LORA_REQUALIFY` after a silence).
   - **Climb rate** is a least-squares slope over the last 1 s of readings (needs 3+ readings over 150 ms). A slope over several samples is far less noisy than the difference between two readings.
3. Calls `refreshInputLocked` to rebuild the live state.

#### `refreshInputLocked()`: deciding what to show

Runs on every reading and every 100 ms.

- `uniqueRole()` finds the one Featherweight and the one LoRa receiver. If two devices claim a role and both are live, the role reports `"ambiguous"` and is not used. The dashboard never guesses by COM number.
- **Freshness:** GPS altitude is eligible for **3 s** after an advancing 3-D fix. LoRa is live for **2 s** after its last valid reading.
- **Altitude priority:** live LoRa → fresh, tared 3-D GPS (GPS altitude minus the pad reference) → unavailable.
- **Climb priority:** LoRa slope → GPS vertical velocity.
- **Calibration lock (`TareLocked`):** set permanently once GPS vertical speed exceeds 60 ft/s, or either source reads more than 100 ft from the pad. After that nobody can re-tare.
- **Manual tare allowed (`CanTare`)** only with a fresh 3-D fix, nearly still (≤10 ft/s vertical, ≤15 ft/s horizontal), not locked and no reference yet.
- Updates the flight phase via `mission.observe`.
- **Change detection:** status ticks only produce an event when something actually changed. A change to the recorder counts alone is broadcast to browsers but **not** logged, otherwise every CSV flush would log a row that triggers another flush.
- `queueLocked` sends the new state to browsers and the CSV queue.

#### Automatic GPS pad reference: `autoTareLocked()`

While on the pad (no launch yet, not locked), and both LoRa and a 3-D fix are live, each stationary GPS fix adds one offset: **GPS altitude − mean LoRa height over the last second**. The pad reference is the **mean of the last 10 offsets**, published after **3**. It stops updating at launch.

Why: LoRa altitude is trusted. Matching GPS to it means that if LoRa drops mid-flight, the GPS fallback continues on the same reference with no jump on the graph. Averaging both sides smooths out GPS altitude noise.

Manual tare (`tareGPS`, triggered by the browser's `tare-gps` command) still exists for GPS-only sessions. The server checks eligibility, so a browser opened mid-flight cannot re-zero the reference.

### 4.4 `flight.go`: phases and detection

`missionTracker.observe()` runs on every refresh but only advances on fresh measurements (`GPS_STAT` with a fresh fix, or `LORA_ALT` while live). Source changes or gaps over 2 s reset its per-source history so a handoff between sensors cannot look like motion.

| Transition | Rule |
|---|---|
| PAD → ASCENT | `ascentDetector.rising`: 20 ft above the second-lowest reading of the last 2.5 s (so one low outlier cannot start it), then 3 qualifying readings over 150 ms with 5 ft more gain. Or GPS vertical speed > 60 ft/s (→ BOOST). |
| ASCENT → BOOST / COAST | GPS only: vertical speed > 60 ft/s is BOOST; climb falling 5 ft/s below its peak is COAST. With LoRa live, both sensors must agree. |
| → APOGEE | Altitude 5 ft below the peak and climb below −3 ft/s for 400 ms |
| APOGEE → DESCENT | After 1 s |
| DESCENT → DROGUE / MAIN | GPS only: below −40 ft/s is DROGUE; between −35 and −5 ft/s is MAIN |
| → LANDED | `settled`: altitude stays within a **20 ft spread for 5 s**, within **30 ft of the pad**, with no gap or source change |

`LaunchMS` and `LandedMS` are set once and drive the browser clock, the graph window and the CSV window. LANDED is final for the session; restart the exe for another flight.

The two-stage ascent rule was replayed against a real 4,781-reading pad capture (noise −6 to +6 ft) with no false trigger.

### 4.5 `recorder.go`: CSVs

**Armed on the pad (`logTelemetry`):** keeps the last **5 s** of rows (up to 2,048) in a ring buffer in memory. No file is created. When a row arrives with `LaunchMS` set, the recorder opens the files with the buffered rows.

**Recording (`writeFlight`, `recordBatches`):**
- Creates `logs/lora/ARES_LoRa_<time>.csv` and `logs/featherweight_gps/ARES_FeatherweightGPS_<time>.csv` with the same timestamp. `O_EXCL` guarantees a new file.
- One goroutine owns both files, so there are no concurrent writes.
- Writes and **fsyncs** every **5 s** or every 2,000 rows. The recording counts in the browser only advance after a successful sync.
- Stops **5 s after `LandedMS`**. A timer covers receivers that go silent after landing. State becomes `complete`.
- If any write or sync fails, recording stops with state `error`, shown in the browser.

**Routing (`logKind`):** Featherweight rows go to the GPS file. LoRa rows and numeric probes go to the LoRa file. Status rows with no receiver input (a LoRa dropout, a tare, a USB close) go to **both**, so each file shows when the selected source changed.

**Columns:**

| File | Columns |
|---|---|
| LoRa | `ReceivedUTC, EventID, Event, Phase, Port, Record, Altitude(ft), Source, SelectedAlt(ft AGL), SelectedUpVel(ft/s), SelectionReason` |
| GPS | `ReceivedUTC, EventID, Event, Phase, Port, Record, TrackerTime, Latitude, Longitude, Altitude(ft), Velocity(ft/s), UpVel(ft/s), Satellites, Fix, RSSI, Battery(mV), GPSPad(ft), GPSPadSource, Source, SelectedAlt(ft AGL), SelectedUpVel(ft/s), SelectionReason` |

Each GPS row shows only what that packet measured: position rows fill the fix columns, radio rows fill RSSI and battery. `Record` always holds the exact serial line. `ReceivedUTC` comes from the same PC clock in both files, so you can line them up.

**Backpressure:** the CSV queue holds 2,000 rows. If the disk stalls, producers wait rather than drop rows. If the writer dies, `csvDone` closes and producers stop waiting.

**What survives a bad exit:** Ctrl+C and closing the console window flush everything. A crash, a forced kill or a power cut loses at most the last 5 s of unflushed rows.

### 4.6 `server.go`: web page and WebSocket

- Serves the embedded `public/` folder at `/` and the WebSocket at `/ws`.
- **Cross-origin connections are rejected** (gorilla's default origin check).
- On connect, the client gets a **snapshot** of the current state (`snapshot: true`, plus `snapshotClockMs` so the browser can align the mission clock). Snapshot and registration happen under one lock so no update falls between them.
- **Heartbeat:** a ping every 20 s; a client that does not answer within 60 s is dropped. This frees handlers for laptops that leave Wi-Fi without closing the socket.
- The only command a browser can send is `tare-gps` (read limit 1 KB).
- `handleBroadcasts` copies the client list, then writes with a 1 s deadline per client, so one slow browser cannot stall the others.
- `publishDisplayLocked` keeps the 100-message display queue from blocking: if full, it drops the oldest message so the **newest** state is always delivered. This matters for the "LoRa went stale" transition, which must never be lost.

---

## 5. Frontend (`public/`)

### 5.1 `app.js`

**Connection.** Opens `/ws`, reconnects every 5 s after a drop. RSSI in dBm converts to a percentage (≥ −50 dBm = 100 %, −120 dBm = 0 %). Every message goes to `handleReceiverEvent`. Messages with an event ID at or below the last one are ignored, which discards stale queued updates after a snapshot. A new backend session ID clears the page.

**`handleReceiverEvent(data)`** updates, in order:
- The mission header (phase badge colour, clock) from the backend's `phase`, `launchMs` and `landedMs`.
- Recording indicator and counts.
- Receiver indicators, port tooltips and the status message (`altitudeMessage`), which explains in plain words where altitude comes from and whether GPS backup is ready.
- GPS-only panels (position, satellites, fix, battery, horizontal speed, estimated G) through `updateUI`.
- **Half-second averages** (`displayMean`) for altitude, climb, horizontal speed, RSSI and battery. Raw readings at 20 Hz are unreadable, so each displayed value and graph point is the mean of about 10 readings. A source change or reconnect starts a fresh average.
- **Peaks** come from raw readings, not averages.

**Estimated vertical G** is computed from GPS only: change in GPS vertical velocity between advancing fixes, divided by 32.174, plus 1 (so 1 G at rest). It is not computed from LoRa altitude because differentiating altitude twice amplifies noise into fake G spikes.

**Graphs.**
- Chart.js line charts, capped at 10,000 points each. Default graphs: altitude and vertical velocity. Horizontal velocity, battery and signal strength can be added.
- **Flight window (`routeGraphPoint`):** on the pad, graphs run live and keep a 5 s bank. At launch they clear and redraw from the bank so the trace starts flat just before liftoff. 5 s after landing they stop. Same window as the CSVs.
- **Source markers:** a source change or dropout inserts a break in the line and a yellow labelled marker on the altitude and vertical velocity graphs. Overlapping labels drop to a second row.
- Hover anywhere in a plot to read the nearest point (`interaction: index, axis: x`).

**Map.** Leaflet with OpenStreetMap tiles. Only positions with at least a 2-D fix are drawn. Repeated positions are skipped. The path is capped at 5,000 points. The map pans when the marker leaves the view.

**Clock.** Laptop time on the pad, `T+mm:ss` after launch, frozen at landing.

**Tare button.** Enabled only when the backend says `canTare`. It sends `tare-gps`.

**Platform panel.** Static descriptions of Harmonia, Shelly, Scylla and Phobos with catalog estimates for altitude and flight time.

**Legacy path.** `updateUI`'s own phase estimator (`checkFlightPhase`) only runs when the backend provides no phase, which today means the `/?simulate=1` preview.

### 5.2 `simulate_flight.js`

Loaded only for `/?simulate=1`. Feeds a fake one-sample-per-second flight into `handleReceiverEvent` with a LoRa outage between seconds 12 and 17. It opens no WebSocket and writes no CSV.

### 5.3 `index.html` and `style.css`

Layout: top bar (logo, phase, clock, indicators), telemetry table and diagnostics, map, graphs with recorder and controls, platform panel. Live and Peak columns have fixed widths so signed six-character values never wrap. Phase colours are set by `data-phase` on the badge.

---

## 6. WebSocket protocol

Server → browser: one JSON object per event.

| Field | Meaning |
|---|---|
| `eventID` | Increments on every change; browsers drop lower IDs |
| `received` | PC receive time (RFC 3339, UTC) |
| `packet` | Event kind: `GPS_STAT`, `RX_NOMTK`, `LORA_ALT`, `GPS_NONADVANCING`, `GPS_TARE`, `STATUS`, `USB_CLOSED`, `RECORDER` |
| `snapshot`, `snapshotClockMs` | Set on the first message after connecting |
| `sessionID` | Changes each time the exe starts |
| `phase`, `launchMs`, `landedMs` | Flight phase and times (Unix ms) |
| `source` | `LoRa`, `Featherweight` or `none` |
| `height`, `climb` | Selected altitude (ft above pad) and climb rate (ft/s) |
| `reason` | Backend explanation of the selection |
| `gpsPort`, `loraPort` | Assigned COM ports, or `ambiguous` |
| `gpsLive`, `gpsFixFresh`, `loraLive` | Receiver health |
| `gpsPad`, `gpsPadAuto` | GPS pad reference and whether it came from LoRa |
| `canTare`, `tareLocked` | Manual tare eligibility and calibration lock |
| `loraRaw` | Last LoRa reading |
| `lat`, `lon`, `alt`, `vel`, `upvel`, `sats`, `fix`, `rssi`, `volt`, `time` | Last Featherweight values |
| `recording` | `{state, gps, lora}`; state is `armed`, `starting`, `recording`, `complete`, `stopped` or `error` |

Browser → server: the text message `tare-gps`.

---

## 7. Key numbers

| Setting | Value | Where |
|---|---|---|
| Serial baud | 115200 | `serial.go` |
| Port rescan | 1 s | `serial.go` |
| Freshness tick | 100 ms | `serial.go` |
| Unidentified port timeout | 12 s after first data | `serial.go` |
| Reconnect backoff | 1, 2, 4, 8 s | `serial.go` |
| LoRa confirmation | 3 readings over 150 ms | `telemetry.go` |
| LoRa freshness | 2 s | `telemetry.go` |
| GPS freshness | 3 s | `telemetry.go` |
| Climb slope window | 1 s | `telemetry.go` |
| Auto-tare | mean of last 10 offsets, after 3 | `telemetry.go` |
| Calibration lock | > 60 ft/s or > 100 ft from pad | `telemetry.go` |
| Ascent | 20 ft in 2.5 s, then 3 readings / 150 ms / +5 ft | `flight.go` |
| Landing | 20 ft spread for 5 s within ±30 ft | `flight.go` |
| Pre-roll | 5 s (max 2,048 rows) | `recorder.go` |
| Post-landing | 5 s | `recorder.go` |
| CSV flush | 5 s or 2,000 rows | `recorder.go` |
| WebSocket ping / timeout | 20 s / 60 s | `server.go` |
| Display average | 500 ms | `app.js` |
| Graph cap / map cap | 10,000 / 5,000 points | `app.js` |

---

## 8. Tests

Run `go test ./...` and `node --test Testing/browser.test.cjs` before every commit. See [COMMANDS.md](COMMANDS.md) for running single tests.

### 8.1 Backend (`main_test.go`)

Test doubles: `testUSB` is a fake serial port fed through a channel. `resetParser()` resets all shared state between tests. `parseLine()` feeds one line through a test receiver. `readLog()` reads a flight CSV.

| Test | What it proves |
|---|---|
| `TestTimestampFormats` | Both GPS timestamp formats decode |
| `TestRadioPacketIdentifiesRetainedGPS` | A radio packet identifies the Featherweight while waiting for a fix |
| `TestInvalidGPSDoesNotReplaceCurrentState` | Malformed, GS/FND and CRC_ERR packets change nothing |
| `TestLoggerDrainsQueueOnShutdown` | Queued rows reach disk at shutdown |
| `TestStoppedParserDoesNotSendToClosedQueue` | No panic after shutdown begins |
| `TestCSVErrorsAndEscaping` | Disk errors surface; commas and quotes are escaped in both files |
| `TestWebSocketSnapshotAndOrigin` | New clients get a snapshot; cross-origin is rejected |
| `TestGetLogDir` | Log folder resolves correctly |
| `TestDiscoverPorts` | Bluetooth ports are excluded |
| `TestAutomaticDualUSB` | Both receivers identified in either plug order, fallback and recovery on a new COM port, healthy GPS never reopened |
| `TestRadioSilenceAndRecovery` | LoRa silence falls back to GPS; recovery needs requalification; climb slope correct |
| `TestRejectNoiseAndAmbiguousNumericDevices` | Junk never identifies a receiver; two numeric streams are ambiguous |
| `TestWebSocketAndCSVShareSelection` | What the browser saw matches what the CSV recorded |
| `TestConcurrentStreamsRecordedExactlyOnce` | 6,000 rows per receiver through a tiny queue, each in the right file, in order |
| `TestAmbiguousInputsAndRecoveryKeepOrigin` | Logged rows keep their real port during ambiguity |
| `TestExpirySurvivesFullDisplayQueue` | The "went stale" update survives a full display queue |
| `TestWriterFailureReleasesProducer` | A dead writer cannot block producers |
| `TestClientsRetireOnSilenceAndShutdown` | Silent and closing browsers are released; no handler leaks |
| `TestOldEpochCannotRenewFallback` | Old or repeated GPS fixes cannot keep GPS fresh; midnight; reconnect |
| `TestServerOwnsPadReference` | Tare is refused in flight or once locked |
| `TestRecordingAcknowledgesDurability` | Counts only advance after a successful sync; open failures show as errors |
| `TestReopenedBrowserCannotResetPad` | A browser opened at 1,000 ft cannot re-zero GPS |
| `TestArmedPadNeverCreatesFile` | Pad noise, spikes and steps never create a CSV |
| `TestCSVWindowFiveSecondPreRollFiveSecondsAfterLanding` | Exact 5 s pre-roll and 5 s post-landing window |
| `TestCSVClosesOnTimerWhenSilentAfterLanding` | Files close even if receivers go silent |
| `TestSimulatedFlightThroughReceiverAndRecorder` | Full simulated flight through the real pipeline produces the right window |
| `TestReplayPadCapture` | Optional: replays a real log through the ascent detector (`ARES_PAD_REPLAY`) |
| `TestLoRaOnlyHeaderThroughLanding` | LoRa alone drives PAD → ASCENT → APOGEE → DESCENT → LANDED |
| `TestMissionLandingRule` | Landing at +25 ft, never at 35 ft, not while descending, gap restarts the hold |
| `TestAutoTareMatchesGPSToLoRaAndLocksAtLaunch` | Auto-tare within 2 ft, locked at launch, seamless fallback |
| `TestAutoTareSkipsMovingGPSAndAveragesRecentFixes` | Moving fixes ignored; mean of the last 10 |
| `TestAutoTareNeedsLoRa` | GPS-only keeps the manual tare |

### 8.2 Browser (`Testing/browser.test.cjs`)

Loads `app.js` into a Node VM with fake DOM, Chart and Leaflet objects, then calls its functions directly. 37 tests cover: G and midnight handling, snapshots and radio packets not advancing phases, invalid and 2-D fixes, map filtering, graph cleanup, platform cards, LoRa-only display, fallback markers, late GPS acquisition, recorder states, half-second averaging, the status message, the mission clock, hover behaviour, the 5 s graph bank and stop after landing, and default graphs.

### 8.3 Writing a new test

- Backend: add a `TestXxx` function to `main_test.go`. Start with `resetParser()`. Use fake receivers (`&receiver{portInfo: portInfo{name: "COM6"}}`) and call `receiveLine` with explicit timestamps, so timing is deterministic. If the recorder is not under test, `close(csvDone)` right after `resetParser()` so rows are not queued.
- Browser: add `test('...', () => { const d = dashboard(); d.run('...'); assert... })` to `Testing/browser.test.cjs`.
- Hardware still needs a real check: physical radio loss, real flights, GPS noise in the field.

---

## 9. How to change things safely

- **Hold `dataMutex`** whenever you touch `currentData`, `receivers`, `mission`, `autoTare` or `padIdentity`. Functions named `...Locked` assume you already hold it.
- **Adding a field to the live state:** add it to `FlightView` with a JSON tag, set it in `refreshInputLocked`, read it in `handleReceiverEvent`. If it belongs in the logs, add a column to `logHeaders` and `writeCSVRows` and update the README.
- **Changing a threshold:** they are constants near the top of each file (see [section 7](#7-key-numbers)). Change the matching test and the docs.
- **Supporting a new receiver type:** add a decoder like `decodeGPS`, a role constant, identification in `receiveLine`, selection in `refreshInputLocked`, a log kind and folder in `recorder.go`.
- **Frontend changes:** keep the backend as the source of truth for phase, source and recording. Rebuild the exe to see changes.
- **Never block under the lock.** The only blocking call under `dataMutex` is the CSV queue send, which is bounded and released if the writer dies.

---

## 10. Hardware notes

- **Featherweight ground station:** FTDI USB serial, text packets as above.
- **LoRa ground receiver:** Adafruit Feather with RFM95, forwarding the StratoLoggerCF's telemetry. Must print one number per line for each new radio sample and go silent when the radio drops (not repeat cached values). The StratoLogger's first `OnPad` line is ground elevation, which the 3-reading confirmation excludes.
- **Bench simulator:** `Testing/usb_altitude_sim.ino` on an Adafruit Feather 32u4. 50 ms samples; 10 s pad, 20 s ascent to 1,500 ft, 60 s descent, then 0 ft. A run starts when a program opens the port; replug the board to restart.
