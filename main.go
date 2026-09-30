package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"flag"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"
)

// TelemetryData holds the parsed payload from the GPS tracker
type TelemetryData struct {
	Lat string `json:"lat"`
	Lon string `json:"lon"`
	Alt string `json:"alt"`
	Vel string `json:"vel"`
	UpVel string `json:"upvel"`
	Sats string `json:"sats"`
	Fix string `json:"fix"`
	RSSI string `json:"rssi"`
	Volt string `json:"volt"`
	Time string `json:"time"`
}

var (
	currentData TelemetryData
	dataMutex   sync.RWMutex
	clients     = make(map[*websocket.Conn]bool)
	clientsMu   sync.Mutex
	broadcast   = make(chan TelemetryData, 100)
	csvChan     = make(chan TelemetryData, 2000)
	debugMode   string
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func main() {
	flag.StringVar(&debugMode, "debug", "", "Debug mode: 'all' (raw stream) or 'gps' (telemetry packets only)")
	flag.Parse()

	// run backend tasks
	go serialReader()
	go handleBroadcasts()
	go csvLogger()

	// Handle static files
	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	// Handle WebSocket connections
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

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

	// Send initial state
	dataMutex.RLock()
	data := currentData
	dataMutex.RUnlock()
	conn.WriteJSON(data)

	// Listen for close
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
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
		for conn := range clients {
			conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			err := conn.WriteJSON(data)
			if err != nil {
				conn.Close()
				delete(clients, conn)
			}
		}
		clientsMu.Unlock()
	}
}

func csvLogger() {
	var buffer []TelemetryData
	ticker := time.NewTicker(5 * time.Second)
	
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
	}
	
	filename := fmt.Sprintf("%s/ARES_FlightLog_%s.csv", logDir, time.Now().Format("2006-01-02_15-04-05"))
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to create CSV log: %v", err)
		return
	}
	defer file.Close()

	file.WriteString("Time,Latitude,Longitude,Altitude(ft),Velocity(ft/s),UpVel(ft/s),Satellites,Fix,RSSI,Battery(mV)\n")

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
		ports, errList := serial.GetPortsList()
		if errList == nil && len(ports) > 0 {
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
		// Example: @ GPS_STAT 203 2020 11 15 01:20:21.986 CRC_OK TRK ... Alt 5655 lt 39.55612 ln -105.1032 Vel 0 -155 0 Fix 3 # 9
		currentData.Time = extractVal(parts, 6) // usually the time
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
			// Channel full, skip to avoid blocking serial reader
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
