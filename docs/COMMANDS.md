# ARES Dashboard Command Reference

Every command for the dashboard in one place. Run them in **PowerShell** from the project folder (the one with `go.mod`) unless a section says otherwise.

```powershell
cd "$HOME\Desktop\Code\ARES\ARES-Dashboard"
```

---

## Install tools (once)

| Task | Command |
|---|---|
| Install Git | `winget install --id Git.Git -e` |
| Install Go | `winget install --id GoLang.Go -e` |
| Install Node.js (browser tests) | `winget install --id OpenJS.NodeJS.LTS -e` |
| Install Arduino IDE (simulator board) | `winget install --id ArduinoSA.IDE.stable -e` |
| Check versions | `git --version` · `go version` · `node --version` |

---

## Get the code

| Task | Command |
|---|---|
| Clone the repository | `git clone https://github.com/NealKSharma/ARES-Dashboard.git` |
| Enter the folder | `cd ARES-Dashboard` |
| Download Go dependencies | `go mod download` |
| Get the latest changes | `git pull` |
| See what changed locally | `git status` |

---

## Build and run

| Task | Command |
|---|---|
| Build the Windows executable | `go build -o "Ares Dashboard.exe" .` |
| Run the executable | `& ".\Ares Dashboard.exe"` |
| Run from source without building | `go run .` |
| Open the dashboard | Browser: `http://127.0.0.1:8080` |
| Browser-only simulated flight | Browser: `http://127.0.0.1:8080/?simulate=1` |
| Stop the dashboard (saves the CSVs) | Press `Ctrl+C` in the console window |

Rebuild after any code change, including files in `public/`. The page is built into the exe.

When you run with `go run .`, logs go to a `logs` folder in your current directory. The built exe always writes `logs` next to itself.

---

## Command-line flags

Flags work the same with the exe and with `go run .`.

| Flag | Default | What it does |
|---|---|---|
| `--listen <address:port>` | `127.0.0.1:8080` | Where the web server listens |
| `--port <COMx>` | empty | Pins the Featherweight to one COM port. LoRa is still found automatically. |
| `--debug=gps` | off | Prints every valid Featherweight packet and each CSV batch saved |
| `--debug=all` | off | Prints every line from every port, and each CSV batch saved |
| `-h` | | Lists all flags |

Examples:

```powershell
& ".\Ares Dashboard.exe" --debug=all
& ".\Ares Dashboard.exe" --port COM5
& ".\Ares Dashboard.exe" --listen 127.0.0.1:8081
& ".\Ares Dashboard.exe" --listen 192.168.1.20:8080
go run . --debug=gps
```

`--listen` with a LAN address lets other laptops on the same network open the dashboard at `http://<that address>:8080`. There is no password, so only do this on a trusted network.

---

## Tests and checks

| Task | Command |
|---|---|
| All backend tests | `go test ./...` |
| Backend tests with the race detector | `go test -race ./...` |
| Shuffled order, repeated 5 times | `go test -race -shuffle=on -count=5 ./...` |
| One test by name | `go test -run TestMissionLandingRule -v ./...` |
| Tests matching a pattern | `go test -run "AutoTare" -v ./...` |
| Static checks | `go vet ./...` |
| Format all Go code | `gofmt -w .` |
| List unformatted files | `gofmt -l .` |
| Browser tests | `node --test Testing/browser.test.cjs` |
| One browser test by name | `node --test --test-name-pattern="pad bank" Testing/browser.test.cjs` |

`-race` on Windows needs a C compiler (for example MSYS2 MinGW-w64 gcc) and `CGO_ENABLED=1`. Plain `go test ./...` needs neither.

Replay a saved LoRa log through the launch detector to check for false launches:

```powershell
$env:ARES_PAD_REPLAY = "C:\path\to\logs\lora\ARES_LoRa_<time>.csv"
go test -run TestReplayPadCapture -v ./...
Remove-Item Env:ARES_PAD_REPLAY
```

Run the browser tests against a different copy of `app.js`:

```powershell
$env:ARES_APP = "C:\path\to\app.js"
node --test Testing/browser.test.cjs
Remove-Item Env:ARES_APP
```

---

## Serial ports and hardware

| Task | Command |
|---|---|
| List COM ports with names | `Get-PnpDevice -Class Ports -PresentOnly \| Select-Object FriendlyName,Status` |
| List COM port names only | `[System.IO.Ports.SerialPort]::GetPortNames()` |
| Check whether the dashboard is running | `Get-Process "Ares Dashboard" -ErrorAction SilentlyContinue` |
| Stop a stuck dashboard | `Stop-Process -Name "Ares Dashboard"` (no clean CSV save; prefer Ctrl+C) |
| See what is using port 8080 | `Get-NetTCPConnection -LocalPort 8080 -State Listen` |

Watch raw data on one port for 10 seconds, without the dashboard running:

```powershell
$p = [System.IO.Ports.SerialPort]::new("COM6", 115200)
$p.DtrEnable = $true
$p.ReadTimeout = 1000
$p.Open()
$end = (Get-Date).AddSeconds(10)
while ((Get-Date) -lt $end) { try { $p.ReadLine() } catch [TimeoutException] {} }
$p.Dispose()
```

---

## Simulator board (Arduino)

| Task | How |
|---|---|
| Adafruit boards URL (File > Preferences) | `https://adafruit.github.io/arduino-board-index/package_adafruit_index.json` |
| Install the board package | Tools > Board > Boards Manager > **Adafruit AVR** |
| Board to select | **Adafruit Feather 32u4** |
| Flash | Open `Testing/usb_altitude_sim.ino`, pick the COM port, click **Upload** |
| Restart the simulated flight | Unplug and replug the board |

Flash from the command line with Arduino CLI (installed with Arduino IDE):

```powershell
$cli = "C:\Program Files\Arduino IDE\resources\app\lib\backend\resources\arduino-cli.exe"
& $cli board list
& $cli core install adafruit:avr --additional-urls https://adafruit.github.io/arduino-board-index/package_adafruit_index.json
& $cli compile --fqbn adafruit:avr:feather32u4 Testing\usb_altitude_sim
& $cli upload -p COM6 --fqbn adafruit:avr:feather32u4 Testing\usb_altitude_sim
```

Arduino CLI expects the sketch inside a folder of the same name: `Testing\usb_altitude_sim\usb_altitude_sim.ino`.

---

## Git workflow

| Task | Command |
|---|---|
| Create a branch | `git checkout -b my-change` |
| Stage everything | `git add -A` |
| Commit | `git commit -m "Describe the change"` |
| Push a new branch | `git push -u origin my-change` |
| Push later commits | `git push` |
| See history | `git log --oneline -10` |
| Undo local edits to one file | `git restore <file>` |

`.gitignore` keeps the exe, `logs/`, CSV files and Go test binaries out of the repository.
