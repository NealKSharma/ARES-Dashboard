# ARES Dashboard Setup Guide

## Quick setup (Windows)

1. Create a folder named `ARES` in File Explorer.
2. Open that folder, type `cmd` in the File Explorer address bar and press **Enter**.
3. Install Git and Go in Command Prompt.

   ```cmd
   winget install --id Git.Git -e
   winget install --id GoLang.Go -e
   ```

4. Close Command Prompt and open `cmd` again from the same folder so it picks up Git and Go. Clone the dashboard and enter its folder.

   ```cmd
   git clone https://github.com/NealKSharma/ARES-Dashboard.git
   cd ARES-Dashboard
   ```

5. Download the dependencies and build the dashboard.

   ```cmd
   go mod download
   go build -o "Ares Dashboard.exe" .
   ```

6. In Explorer, open `ARES-Dashboard` and double-click `Ares Dashboard.exe`. Leave its terminal window open.
7. Hold **Ctrl** and click the link printed in the terminal. If the click does nothing, paste the link into your browser.
8. Plug the **Featherweight GPS ground station** and the **LoRa receiver** into the laptop by USB.
9. Check the terminal for two lines like these. Your COM numbers may differ.

   ```text
   Featherweight telemetry on COM3
   LoRa altitude stream on COM4
   ```

   When both appear on different COM ports, the dashboard has detected both telemetry streams. You should start seeing live readings in the browser. If either line is missing, see [Check the receivers](#check-the-receivers).

## Detailed setup

### Install and check the tools

If Windows cannot find `winget`, use the [Git for Windows](https://git-scm.com/downloads/win) and [Go](https://go.dev/dl/) installers instead.

Open a new Command Prompt and run `git --version` and `go version`. Both should print a version. The dashboard requires Go 1.21 or newer.

### Clone, build and start

Open Command Prompt in the folder where you want the source code. Run the clone command from Quick setup once. For later builds, open Command Prompt directly in `ARES-Dashboard`, the folder containing `go.mod`.

Run `go mod download`, then `go build -o "Ares Dashboard.exe" .` including the final dot. The exe contains the web page. Rebuild and restart it after changing Go code or files in `public/`.

Double-click the exe and open the address it prints (normally `http://127.0.0.1:8080`). A green **Server connected** indicator means the browser can reach the dashboard. Closing the terminal stops the server. Map tiles need internet access.

### Check the receivers

Plug the receivers in either order. Hover over each green receiver indicator in the browser to see its COM port.

| Receiver | What confirms live data |
| --- | --- |
| Featherweight GPS ground station | The terminal prints `Featherweight telemetry on COM…` when a valid packet arrives. GPS altitude needs a 3-D fix, which may take a minute outdoors. |
| LoRa ground receiver | The terminal prints `LoRa altitude stream on COM…` when it detects a steady stream of altitude readings. |

If a receiver is missing, open **Device Manager → Ports (COM & LPT)**. Unplug it and plug it back in to see which port appears. If no port appears, try a USB data cable directly in the laptop. If the port appears but the dashboard shows no readings, close Arduino Serial Monitor or any other program using that port. To inspect incoming data, close the dashboard and run `"Ares Dashboard.exe" --debug=all` from its folder. See [COMMANDS.md](COMMANDS.md) for other options.

### Launch day checklist

Do this at the pad, with the rocket powered and both receivers plugged in.

| Check | What you should see |
|---|---|
| Server | Green **Server connected** |
| Featherweight | Green **Featherweight connected**, GPS fix **3**, satellites showing |
| LoRa | Green **LoRa connected**, altitude near 0 ft and source **StratoLogger via LoRa** |
| GPS backup | The status line says **GPS backup is ready, matched to LoRa.** This sets itself about 3 seconds after both receivers are live. |
| Phase and clock | Phase badge **PAD**, clock showing the laptop's time |
| Recording | **CSV armed · waiting for ascent** |

**GPS backup without LoRa:** if only the Featherweight is connected, wait for a 3-D fix with the rocket still and click **Tare GPS at pad**. With LoRa connected you do not need to tare; it happens automatically and locks at launch.

**What happens at launch:**

- The phase changes from **PAD** to **ASCENT** (or **BOOST** with GPS) and the clock switches to **T+**.
- The graphs clear and redraw starting 5 seconds before liftoff.
- Recording starts: **CSV recording**, with saved row counts for GPS and LoRa.
- If LoRa drops out mid-flight, altitude switches to **Featherweight GPS** automatically and switches back when LoRa returns. Yellow markers on the graphs show each switch.

**After landing:**

- The phase shows **LANDED** once the altitude has held steady for 5 seconds within 30 ft of the pad. The clock freezes at the flight time.
- 5 seconds later the graphs stop and the recorder shows **CSV saved · flight complete**.
- Your files are in the `logs` folder next to the exe:
  - `logs\lora\ARES_LoRa_<date_time>.csv`
  - `logs\featherweight_gps\ARES_FeatherweightGPS_<date_time>.csv`
- Press **Ctrl+C** in the console window to close the dashboard. **Restart the exe before the next flight.**

---

### Practice without a rocket

**Browser-only preview:** with the dashboard running, open http://127.0.0.1:8080/?simulate=1. It plays a fake flight with a LoRa dropout. Nothing gets recorded.

**Bench simulator board:** `Testing/usb_altitude_sim.ino` turns an Adafruit Feather 32u4 into a fake LoRa receiver that sends a 90-second flight: 10 s on the pad, 20 s climb to 1,500 ft, 60 s descent.

1. In Arduino IDE, go to **File > Preferences** and add this to **Additional boards manager URLs**:
   `https://adafruit.github.io/arduino-board-index/package_adafruit_index.json`
2. **Tools > Board > Boards Manager**, search **Adafruit AVR**, install it.
3. Open `Testing/usb_altitude_sim.ino`. Arduino IDE asks to move it into a folder named `usb_altitude_sim`; let it.
4. Pick board **Adafruit Feather 32u4** and its COM port, then click **Upload** (the arrow button).
5. Close Arduino IDE's Serial Monitor and start the dashboard. The flight starts when the dashboard opens the port.
6. To run it again, unplug and replug the board.

Flashing the simulator replaces whatever was on the board, so keep a copy of the real receiver sketch if you are reusing a flight board.

---

### Troubleshooting

| Problem | Fix |
|---|---|
| Browser shows "Connection lost" | The exe stopped. Restart it. The page reconnects every 5 seconds. |
| Page did not change after editing code | Rebuild the exe and restart it. |
| `go build` fails with "cannot find module" | Run `go mod download` from the project folder. |
| Altitude stuck at 0 with the simulator | The simulator finished its run. Replug the board. |
| "Multiple USB ports are streaming numbers" | Two devices look like LoRa receivers. Unplug the one you do not need. |
| No CSV after a test | Recording only starts on a detected ascent. A pad-only session saves nothing. |
| Port 8080 already in use | Another dashboard is running. Close it, or start with `--listen 127.0.0.1:8081`. |
