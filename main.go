package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"
)

// parsed data from the Featherweight GPS tracker
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
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func main() {
	// Start serial reader in a goroutine
	go serialReader()
	
	// Start the single broadcaster goroutine
	go handleBroadcasts()

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

func serialReader() {
	var port serial.Port
	var err error

	for {
		if port == nil {
			// Auto-detect port
			ports, errList := serial.GetPortsList()
			if errList != nil || len(ports) == 0 {
				log.Println("No serial ports found, retrying in 5s...")
				time.Sleep(5 * time.Second)
				continue
			}

			// Just pick the first available port for simplicity as per requirement
			portName := ports[0]
			fmt.Printf("Attempting to open port %s...\n", portName)
			mode := &serial.Mode{
				BaudRate: 115200,
			}
			port, err = serial.Open(portName, mode)
			if err != nil {
				log.Printf("Failed to open %s: %v, retrying in 5s...", portName, err)
				time.Sleep(5 * time.Second)
				continue
			}
			fmt.Printf("Successfully opened %s\n", portName)
		}

		scanner := bufio.NewScanner(port)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "@ ") {
				parseLine(line)
			}
		}
		
		if err := scanner.Err(); err != nil {
			log.Printf("Serial read error: %v", err)
		}
		
		port.Close()
		port = nil
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
