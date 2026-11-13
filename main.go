package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

//go:embed public
var publicFS embed.FS

type TelemetryData struct {
	Lat      string `json:"lat"`
	Lon      string `json:"lon"`
	Alt      string `json:"alt"`
	Vel      string `json:"vel"`
	UpVel    string `json:"upvel"`
	Sats     string `json:"sats"`
	Fix      string `json:"fix"`
	RSSI     string `json:"rssi"`
	Volt     string `json:"volt"`
	Time     string `json:"time"`
	Packet   string `json:"packet,omitempty"`
	Snapshot bool   `json:"snapshot,omitempty"`
}

var currentData TelemetryData
var dataMutex sync.RWMutex
var clients = make(map[*websocket.Conn]bool)
var clientsMu sync.Mutex
var broadcast = make(chan TelemetryData, 100)
var csvChan = make(chan TelemetryData, 2000)
var debugMode string
var portFlag string
var listenFlag string
var stopping bool
var csvDone = make(chan struct{})
var broadcastDrops, csvDrops uint64

var upgrader = websocket.Upgrader{} // Reject cross-origin browser connections by default.

func main() {
	flag.StringVar(&debugMode, "debug", "", "Debug mode: 'all' (raw stream) or 'gps' (telemetry packets only)")
	flag.StringVar(&portFlag, "port", "", "Specific COM port to listen on (e.g., COM3). If empty, requires exactly one available port.")
	flag.StringVar(&listenFlag, "listen", "127.0.0.1:8080", "HTTP listen address; use an explicit trusted LAN address only when sharing is intended")
	flag.Parse()

	go serialReader()
	go handleBroadcasts()
	go csvLogger()

	sub, _ := fs.Sub(publicFS, "public")
	http.Handle("/", http.FileServer(http.FS(sub)))

	http.HandleFunc("/ws", handleWebSocket)

	port := listenFlag
	fmt.Printf("Server starting on http://%s\n", port)
	server := &http.Server{Addr: port, Handler: nil, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		log.Printf("Server stopped: %v", err)
	}
	// Stop producers before closing the queue, then drain and sync the log.
	dataMutex.Lock()
	stopping = true
	close(csvChan)
	dataMutex.Unlock()
	<-csvDone
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}

}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket Upgrade error:", err)
		return
	}

	dataMutex.RLock()
	data := currentData
	dataMutex.RUnlock()

	data.Snapshot = true
	// Write the snapshot FIRST before registering to the broadcaster to prevent concurrent writes
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	if err := conn.WriteJSON(data); err != nil {
		conn.Close()
		return
	}

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

	conn.SetReadLimit(1024)
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			clientsMu.Lock()
			delete(clients, conn)
			clientsMu.Unlock()
			conn.Close()
			break
		}
	}
}

func handleBroadcasts() {
	for data := range broadcast {
		clientsMu.Lock()
		activeClients := make([]*websocket.Conn, 0, len(clients))
		for conn := range clients {
			activeClients = append(activeClients, conn)
		}
		clientsMu.Unlock()

		for _, conn := range activeClients {
			conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
			err := conn.WriteJSON(data)
			if err != nil {
				clientsMu.Lock()
				if clients[conn] {
					conn.Close()
					delete(clients, conn)
				}
				clientsMu.Unlock()
			}
		}
	}
}

func getLogDir() string {
	// If running as a standalone binary outside temp, use its directory.
	// When running via 'go run', exePath is in a temporary build directory (e.g. go-build),
	// so fall back to the working directory's "logs" folder.
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		cleanDir := strings.ToLower(filepath.Clean(dir))
		tempDir := strings.ToLower(filepath.Clean(os.TempDir()))
		if !strings.Contains(cleanDir, "go-build") && !strings.HasPrefix(cleanDir, tempDir) {
			return filepath.Join(dir, "logs")
		}
	}
	return "logs"
}

func csvLogger() {
	defer close(csvDone)
	logTelemetry(getLogDir())
}

func logTelemetry(logDir string) {
	var buffer []TelemetryData
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
		return
	}

	filename := filepath.Join(logDir, fmt.Sprintf("ARES_FlightLog_%s.csv", time.Now().Format("2006-01-02_15-04-05.000000000")))
	if abs, err := filepath.Abs(filename); err == nil {
		filename = abs
	}
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to create CSV log: %v", err)
		return
	}
	defer file.Close()

	if _, err := file.WriteString("Time,Latitude,Longitude,Altitude(ft),Velocity(ft/s),UpVel(ft/s),Satellites,Fix,RSSI,Battery(mV)\n"); err != nil {
		log.Printf("CSV header failed: %v", err)
		return
	}
	if err := file.Sync(); err != nil {
		log.Printf("CSV sync failed: %v", err)
		return
	}

	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		if err := writeCSVRows(file, buffer); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if debugMode == "all" || debugMode == "gps" {
			log.Printf("Saved %d telemetry points to %s", len(buffer), filename)
		}
		buffer = buffer[:0]
		return nil
	}
	for {
		select {
		case data, ok := <-csvChan:
			if !ok {
				if err := flush(); err != nil {
					log.Printf("CSV shutdown flush failed: %v", err)
				}
				return
			}
			buffer = append(buffer, data)
			if len(buffer) >= 2000 {
				if err := flush(); err != nil {
					log.Printf("CSV write failed: %v", err)
					return
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				log.Printf("CSV write failed: %v", err)
				return
			}
		}
	}
}

var (
	activePorts   = make(map[string]bool)
	activePortsMu sync.Mutex
	activeTracker string
	trackerMu     sync.Mutex
)

func discoverPorts() ([]string, error) {
	detailed, err := enumerator.GetDetailedPortsList()
	if err == nil && len(detailed) > 0 {
		var usbPorts []string
		var otherPorts []string
		for _, p := range detailed {
			prod := strings.ToLower(p.Product)
			name := strings.ToLower(p.Name)
			// Ignore virtual Bluetooth serial links that cause OS connection delays
			if strings.Contains(prod, "bluetooth") || strings.Contains(name, "bluetooth") {
				continue
			}
			if p.IsUSB || p.VID != "" {
				usbPorts = append(usbPorts, p.Name)
			} else {
				otherPorts = append(otherPorts, p.Name)
			}
		}
		// Prioritize USB serial ports first, then any other physical serial ports
		all := append(usbPorts, otherPorts...)
		if len(all) > 0 {
			return all, nil
		}
	}

	return serial.GetPortsList()
}

func serialReader() {
	lastLogged := ""
	for {
		trackerMu.Lock()
		hasTracker := (activeTracker != "")
		trackerMu.Unlock()

		if hasTracker {
			time.Sleep(2 * time.Second)
			continue
		}

		var ports []string
		if portFlag != "" {
			ports = []string{portFlag}
		} else {
			var errList error
			ports, errList = discoverPorts()
			if errList != nil {
				time.Sleep(2 * time.Second)
				continue
			}
		}

		if len(ports) > 0 {
			summary := strings.Join(ports, ", ")
			if (debugMode == "all" || debugMode == "gps") && summary != lastLogged && portFlag == "" {
				log.Printf("Scanning serial ports for telemetry: %s", summary)
				lastLogged = summary
			}

			activePortsMu.Lock()
			for _, portName := range ports {
				if !activePorts[portName] {
					activePorts[portName] = true
					go handlePort(portName, portFlag != "")
				}
			}
			activePortsMu.Unlock()
		}

		time.Sleep(2 * time.Second)
	}
}

func handlePort(portName string, isExplicit bool) {
	defer func() {
		activePortsMu.Lock()
		delete(activePorts, portName)
		activePortsMu.Unlock()

		trackerMu.Lock()
		wasActive := (activeTracker == portName)
		if wasActive {
			activeTracker = ""
			log.Printf("Lost connection to ARES Tracker on %s\n", portName)
		}
		trackerMu.Unlock()

		if debugMode == "all" || debugMode == "gps" {
			fmt.Printf("[DEBUG] Stopped listening to %s\n", portName)
		}
	}()

	mode := &serial.Mode{BaudRate: 115200}
	port, err := serial.Open(portName, mode)
	if err != nil {
		return
	}
	defer port.Close()

	if isExplicit {
		trackerMu.Lock()
		activeTracker = portName
		trackerMu.Unlock()
		log.Printf("Listening for ARES Tracker on %s...\n", portName)
	} else {
		// Auto-searching: probe for valid telemetry with a 5-second window.
		// If no telemetry packet arrives or another port claims the tracker, close this port.
		probeTimer := time.AfterFunc(5*time.Second, func() {
			trackerMu.Lock()
			isTracker := (activeTracker == portName)
			trackerMu.Unlock()
			if !isTracker {
				port.Close()
			}
		})
		defer probeTimer.Stop()
	}

	scanner := bufio.NewScanner(port)
	buf := make([]byte, 1024*1024) // 1MB buffer
	scanner.Buffer(buf, 1024*1024)

	firstPacket := true
	for scanner.Scan() {
		line := scanner.Text()

		isTelemetry := strings.HasPrefix(line, "@ ") || strings.HasPrefix(line, "@")

		if isTelemetry {
			trackerMu.Lock()
			if activeTracker == "" {
				activeTracker = portName
				log.Printf("Found ARES Tracker on %s (live telemetry detected)!\n", portName)
			}
			isCurrentTracker := (activeTracker == portName)
			trackerMu.Unlock()

			if !isCurrentTracker {
				// Another port was already selected as the active tracker; stop this one.
				return
			}

			if firstPacket {
				firstPacket = false
			}

			if debugMode == "all" || debugMode == "gps" {
				fmt.Printf("[LIVE-RAW %s] %s\n", portName, line)
			}
			parseLine(line)
		} else if debugMode == "all" {
			fmt.Printf("[LIVE-RAW %s] %s\n", portName, line)
		}
	}

	if err := scanner.Err(); err != nil && debugMode == "all" {
		log.Printf("Scanner error on %s: %v", portName, err)
	}
}

func parseLine(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 || parts[0] != "@" {
		return
	}

	packetType := parts[1]

	dataMutex.Lock()
	defer dataMutex.Unlock()
	if stopping {
		return
	}
	updated := false

	next := currentData
	if packetType == "GPS_STAT" {
		// GS and FND are not the selected rocket's live tracker GPS.
		if indexOf(parts, "GS") >= 0 || indexOf(parts, "FND") >= 0 || indexOf(parts, "CRC_ERR") >= 0 {
			return
		}
		next.Time = extractAfter(parts, "Time")
		if next.Time == "" {
			next.Time = extractVal(parts, 6)
		}
		if _, err := time.Parse("15:04:05", next.Time); err != nil {
			return
		}
		next.Alt = extractAfter(parts, "Alt")
		next.Lat = extractAfter(parts, "lt")
		next.Lon = extractAfter(parts, "ln")
		velocity := indexOf(parts, "Vel")
		if velocity < 0 || velocity+3 >= len(parts) {
			return
		}
		next.Vel, next.UpVel = parts[velocity+1], parts[velocity+3]
		next.Fix, next.Sats = extractAfter(parts, "Fix"), extractAfter(parts, "#")
		for _, value := range []string{next.Alt, next.Lat, next.Lon, next.Vel, next.UpVel, next.Fix, next.Sats} {
			if !finiteNumber(value) {
				return
			}
		}
		lat, _ := strconv.ParseFloat(next.Lat, 64)
		lon, _ := strconv.ParseFloat(next.Lon, 64)
		if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			return
		}
		if next.Fix != "0" && next.Fix != "2" && next.Fix != "3" {
			return
		}
		updated = true
	} else if packetType == "RX_NOMTK" {
		next.RSSI, next.Volt = extractAfter(parts, "RSSI"), extractAfter(parts, "trk_B_V")
		if !finiteNumber(next.RSSI) || !finiteNumber(next.Volt) {
			return
		}
		updated = true
	}
	if updated {
		currentData = next
	}

	if updated {
		currentData.Packet = packetType
		select {
		case broadcast <- currentData:
		default:
			broadcastDrops++
			if broadcastDrops == 1 || broadcastDrops%100 == 0 {
				log.Printf("Broadcast queue dropped %d samples", broadcastDrops)
			}
		}

		select {
		case csvChan <- currentData:
		default:
			csvDrops++
			if csvDrops == 1 || csvDrops%100 == 0 {
				log.Printf("CSV queue dropped %d samples", csvDrops)
			}
		}
	}
}

func extractAfter(parts []string, key string) string {
	idx := indexOf(parts, key)
	if idx != -1 && idx+1 < len(parts) {
		return parts[idx+1]
	}
	return ""
}

func extractVal(parts []string, idx int) string {
	if idx >= 0 && idx < len(parts) {
		return parts[idx]
	}
	return ""
}

func indexOf(parts []string, key string) int {
	for i, v := range parts {
		if v == key {
			return i
		}
	}
	return -1
}

// writeCSVRows escapes fields and surfaces buffered write errors.
func writeCSVRows(dst io.Writer, rows []TelemetryData) error {
	writer := csv.NewWriter(dst)
	for _, d := range rows {
		if err := writer.Write([]string{d.Time, d.Lat, d.Lon, d.Alt, d.Vel, d.UpVel, d.Sats, d.Fix, d.RSSI, d.Volt}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func finiteNumber(s string) bool {
	value, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}
