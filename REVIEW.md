# Dashboard reliability review — 2026-11-03

Reviewed the local ARES Go dashboard at baseline commit `3773b4a`, its JavaScript,
HTML, CSS, simulator, dependency declarations, and the local February 2025
Featherweight tracker manual (Appendix A). This is a software review, not launch
certification. Changes are uncommitted.

## Verified defects repaired

| Area | Finding and repair |
| --- | --- |
| Timestamp parsing | Missing `Time` key indexed token zero (`@`), preventing usable tracker time. The positional fallback now works. |
| Packet validation | Partial, nonnumeric, out-of-range, GS/FND, and CRC_ERR GPS packets could overwrite rocket state. Validated GPS updates now apply atomically. |
| GPS versus radio | Merged RX_NOMTK records reused old GPS values in flight calculations. Packet type now separates radio updates from fresh GPS processing and graph sampling. |
| Midnight and reconnect | The acceleration baseline stopped advancing at UTC midnight; disconnects/gaps retained stale acceleration. Rollover and baseline invalidation are tested. |
| Flight/reset state | Snapshots could trigger flight transitions, clear retained old acceleration/time state, and initial velocity could not trigger launch without G. These paths are corrected; launch retains its initial sample baseline and altitude/vertical-G peaks. |
| Graph lifecycle | Removed charts were not destroyed, retaining Chart.js resources. Cleanup now destroys the instance. |
| Map | Invalid latitude/longitude and no-fix positions entered the path. Bounds and fix checks now guard updates; clear removes the marker. |
| Logging | Buffered points were not flushed on normal shutdown, write errors were ignored, fields were not CSV-escaped, and dropped queue entries were silent. Shutdown drains the queue; writes/syncs are checked; CSV encoding and drop diagnostics are added. |
| WebSocket/server | All browser origins were accepted and the listener exposed all interfaces. Default is now loopback and same-origin; explicit `--listen` supports intentional trusted-LAN use. Initial snapshot writes have a deadline and error handling; inbound messages have a size limit. |
| Serial selection | Auto-scan could merge telemetry from several ports. Multiple available ports now require explicit `--port` selection. |
| Responsive UI | The short-screen rule overrode mobile grid rows. The rule now applies only to desktop width, and small-screen headers wrap. Graph selector has an accessible name. |
| Display semantics | “Net Accel” included gravity and was derived from GPS. It is now labeled “Est. vertical G” with explanatory text. |

## Validation

- Original baseline builds; there were no repository tests. An apparent missing-code issue in an initial source read was disproved by a full reread and successful build.
- Nine focused browser regression cases failed before changes and passed afterward.
- Expanded browser suite: 14 tests, including a synthetic PAD → BOOST → COAST → APOGEE → DROGUE → MAIN → LANDED sequence.
- Go suite: seven tests covering timestamp formats, retained GPS/radio identification, invalid/non-tracker GPS, closed-queue safety, CSV error/escaping behavior, actual logger queue draining, and WebSocket snapshot/origin behavior.
- `go test -race ./...` and `go vet ./...` passed on the staged implementation.
- Real-browser checks at 1280×720 and 390×844; representative synthetic telemetry; graph addition, exhaustion/duplicate prevention, removal, and platform switching.

## Remaining operational boundaries

- No USB tracker was connected or exercised. Validate representative captures from the actual ground-station firmware, including reconnects, no-fix periods, packet rate, and a full flight before field reliance.
- GPS-derived flight phase and vertical G remain heuristics. No sensor confirms motor burnout, parachute deployment, or landing. Missing/noisy samples can skip or delay inferred phases; a browser reload cannot reconstruct flight history.
- Packet CRC_ERR flags are honored for GPS, but the serial CRC is not independently recomputed. Hardware captures are needed before tightening all wire-format assumptions.
- Map tiles and optional fonts use external services. Offline map tiles are not embedded.
- Abrupt process termination/power loss, disk failure, or queue overload can lose telemetry. Normal shutdown flushing does not guarantee crash durability. Logging failures/drop counts are console diagnostics, not yet a dashboard health indicator.
- A slow WebSocket client can delay the single broadcaster until its write deadline. High-rate/many-client load was not benchmarked.
- One selected serial port does not provide multi-rocket identity management; the ground station must be configured for the intended tracker. Platform buttons remain reference specifications only.
- The preexisting simulator injects browser data directly and does not test USB parsing or CSV recording. Automated tests use synthetic data; no real flight traces were retained.

See [README](README.md) for commands and changed startup behavior.
