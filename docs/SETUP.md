# ARES Dashboard Setup Guide

This guide takes you from an empty Windows laptop to a dashboard that is armed and ready on the pad. Follow it in order the first time. On launch day you only need [Part 6](#part-6-launch-day-checklist).

- Repository: https://github.com/NealKSharma/ARES-Dashboard
- Every command in this guide is also in [COMMANDS.md](COMMANDS.md).

---

## Part 1: Install the tools

You need Git to download the code and Go to build it. Node.js and Arduino IDE are optional: Node.js runs the browser tests and Arduino IDE flashes the bench simulator board.

Open **PowerShell** (Start menu, type `PowerShell`) and run:

```powershell
winget install --id Git.Git -e
winget install --id GoLang.Go -e
```

Optional:

```powershell
winget install --id OpenJS.NodeJS.LTS -e
winget install --id ArduinoSA.IDE.stable -e
```

If `winget` is not available, download the installers instead:

- Git: https://git-scm.com/downloads
- Go (1.21 or newer): https://go.dev/dl/
- Node.js LTS: https://nodejs.org/
- Arduino IDE: https://www.arduino.cc/en/software

**Close PowerShell and open a new window** so it picks up the new tools, then check them:

```powershell
git --version
go version
node --version
```

Each should print a version number. If one says "not recognized", reopen PowerShell or reinstall that tool.

---

## Part 2: Create a folder and download the code

Pick a home for your code. This guide uses `C:\Users\<you>\Desktop\Code\ARES`.

```powershell
mkdir "$HOME\Desktop\Code\ARES"
cd "$HOME\Desktop\Code\ARES"
git clone https://github.com/NealKSharma/ARES-Dashboard.git
cd ARES-Dashboard
```

You now have the source code in `...\ARES\ARES-Dashboard`. Every later command runs from this folder.

Download the Go libraries the dashboard uses (WebSockets and serial ports):

```powershell
go mod download
```

---

## Part 3: Build the executable

```powershell
go build -o "Ares Dashboard.exe" .
```

This creates `Ares Dashboard.exe` in the project folder. The web page (HTML, CSS, JavaScript) is built into the exe, so the exe is the only file you need on the field laptop.

**Rebuild every time you change any code**, including anything in `public/`. An old exe keeps running the old page.

Optional, but worth doing before a launch: run the tests to confirm the build is healthy.

```powershell
go test ./...
node --test Testing/browser.test.cjs
```

---

## Part 4: Start the dashboard

1. Double-click `Ares Dashboard.exe`, or run it from PowerShell:

   ```powershell
   & ".\Ares Dashboard.exe"
   ```

   A console window opens and prints `Server starting on http://127.0.0.1:8080`. **Leave this window open.** Closing it stops the dashboard.

2. Open a browser and go to **http://127.0.0.1:8080**.

3. The top bar shows three indicators: **Server**, **Featherweight** and **LoRa**. Server should turn green right away. The other two turn green once their receivers stream data.

The map needs Internet for its tiles. Everything else works offline.

---

## Part 5: Connect the receivers

The dashboard finds both receivers automatically. You never pick a COM port.

1. Plug in the **Featherweight GPS ground station** by USB.
2. Plug in the **LoRa ground receiver** (the board that forwards StratoLogger altitude) by USB.
3. Plug them in either order, before or after starting the dashboard.

Within a few seconds:

- **Featherweight connected** turns green once a valid GPS or radio packet arrives. GPS altitude needs a 3-D fix, which can take a minute outdoors.
- **LoRa connected** turns green once the receiver sends a steady stream of altitude numbers (at least 3 readings over 150 ms).
- Hover over either indicator to see which COM port it is on.

If a receiver does not show up:

- Close Arduino Serial Monitor or any other program that has the port open. Only one program can use a COM port at a time.
- Try a different USB cable (some are charge-only) or plug directly into the laptop instead of a hub.
- Open Device Manager and look under **Ports (COM & LPT)**. If the device is missing or shows an error, Windows cannot see it, so the dashboard cannot either.
- Run with `--debug=all` to see every line arriving on every port (see [COMMANDS.md](COMMANDS.md)).

---

## Part 6: Launch day checklist

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

## Part 7: Practising without a rocket

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

## Quick troubleshooting

| Problem | Fix |
|---|---|
| Browser shows "Connection lost" | The exe stopped. Restart it. The page reconnects every 5 seconds. |
| Page did not change after editing code | Rebuild the exe (Part 3) and restart it. |
| `go build` fails with "cannot find module" | Run `go mod download` from the project folder. |
| Altitude stuck at 0 with the simulator | The simulator finished its run. Replug the board. |
| "Multiple USB ports are streaming numbers" | Two devices look like LoRa receivers. Unplug the one you do not need. |
| No CSV after a test | Recording only starts on a detected ascent. A pad-only session saves nothing. |
| Port 8080 already in use | Another dashboard is running. Close it, or start with `--listen 127.0.0.1:8081`. |
