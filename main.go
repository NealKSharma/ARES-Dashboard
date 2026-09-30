package main

import (
	"bufio"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"path/filepath"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	flag.StringVar(&debugMode, "debug", "", "Debug mode: 'all' (raw stream) or 'gps' (telemetry packets only)")
	flag.StringVar(&portFlag, "port", "", "Specific COM port to listen on (e.g., COM3). If empty, auto-scans all ports.")
	flag.Parse()

	go serialReader()
	go handleBroadcasts()
	go csvLogger()

	sub, _ := fs.Sub(publicFS, "public")
	http.Handle("/", http.FileServer(http.FS(sub)))

	http.HandleFunc("/ws", handleWebSocket)

	port := ":8080"
	fmt.Printf("Server starting on http://localhost%s\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
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
	conn.WriteJSON(data)

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

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

func csvLogger() {
	var buffer []TelemetryData
	ticker := time.NewTicker(5 * time.Second)

	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Failed to get executable path: %v", err)
		return
	}
	logDir := filepath.Join(filepath.Dir(exePath), "logs")
	
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
	}

	filename := filepath.Join(logDir, fmt.Sprintf("ARES_FlightLog_%s.csv", time.Now().Format("2006-01-02_15-04-05")))
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to create CSV log: %v", err)
		return
	}
	defer file.Close()

	file.WriteString("Time,Latitude,Longitude,Altitude(ft),Velocity(ft/s),UpVel(ft/s),Satellites,Fix,RSSI,Battery(mV)\n")
	file.Sync()

	for {
		select {
		case data := <-csvChan:
			buffer = append(buffer, data)
		case <-ticker.C:
			if len(buffer) > 0 {
				var sb strings.Builder
				for _, d := range buffer {
					sb.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
						d.Time, d.Lat, d.Lon, d.Alt, d.Vel, d.UpVel, d.Sats, d.Fix, d.RSSI, d.Volt))
				}
				file.WriteString(sb.String())
				file.Sync()
				log.Printf("Saved %d telemetry points to %s\n", len(buffer), filename)
				buffer = buffer[:0]
			}
		}
	}
}

var (
	activePorts   = make(map[string]bool)
	activePortsMu sync.Mutex
)

func serialReader() {
	for {
		var ports []string
		if portFlag != "" {
			ports = []string{portFlag}
		} else {
			var errList error
			ports, errList = serial.GetPortsList()
			if errList != nil {
				time.Sleep(3 * time.Second)
				continue
			}
		}

		if len(ports) > 0 {
			activePortsMu.Lock()
			for _, portName := range ports {
				if !activePorts[portName] {
					activePorts[portName] = true
					if debugMode == "all" || debugMode == "gps" {
						fmt.Printf("[DEBUG] Launching listener for %s...\n", portName)
					}
					go handlePort(portName)
				}
			}
			activePortsMu.Unlock()
		}
		time.Sleep(3 * time.Second)
	}
}

func handlePort(portName string) {
	defer func() {
		activePortsMu.Lock()
		delete(activePorts, portName)
		activePortsMu.Unlock()
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

	scanner := bufio.NewScanner(port)
	buf := make([]byte, 1024*1024) // 1MB buffer
	scanner.Buffer(buf, 1024*1024)

	firstPacket := true
	for scanner.Scan() {
		line := scanner.Text()

		isTelemetry := strings.HasPrefix(line, "@ ") || strings.HasPrefix(line, "@")

		if debugMode == "all" {
			fmt.Printf("[LIVE-RAW %s] %s\n", portName, line)
		} else if debugMode == "gps" && isTelemetry {
			fmt.Printf("[LIVE-RAW %s] %s\n", portName, line)
		}

		if isTelemetry {
			if firstPacket {
				log.Printf("Connected to ARES Tracker on %s and receiving live telemetry!\n", portName)
				firstPacket = false
			}
			parseLine(line)
		}
	}
	
	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error on %s: %v", portName, err)
	}

	if !firstPacket {
		log.Printf("Lost connection to ARES Tracker on %s\n", portName)
	}
}

func parseLine(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return
	}

	packetType := parts[1]

	dataMutex.Lock()
	defer dataMutex.Unlock()
	updated := false

	if packetType == "GPS_STAT" {
		currentData.Time = extractVal(parts, indexOf(parts, "Time")+1)
		if currentData.Time == "" { // fallback if "Time" key isn't explicitly printed
			currentData.Time = extractVal(parts, 6)
		}
		currentData.Alt = extractAfter(parts, "Alt")
		currentData.Lat = extractAfter(parts, "lt")
		currentData.Lon = extractAfter(parts, "ln")

		velIndex := indexOf(parts, "Vel")
		if velIndex != -1 && velIndex+3 < len(parts) {
			currentData.Vel = parts[velIndex+1]
			currentData.UpVel = parts[velIndex+3]
		}

		currentData.Fix = extractAfter(parts, "Fix")
		currentData.Sats = extractAfter(parts, "#")
		updated = true

	} else if packetType == "RX_NOMTK" {
		currentData.RSSI = extractAfter(parts, "RSSI")
		currentData.Volt = extractAfter(parts, "trk_B_V")
		updated = true
	}

	if updated {
		select {
		case broadcast <- currentData:
		default:
		}

		select {
		case csvChan <- currentData:
		default:
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
