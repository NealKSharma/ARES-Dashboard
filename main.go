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
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func main() {
	// run backend tasks
	go serialReader()
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
	for {
		ports, errList := serial.GetPortsList()
		if errList != nil || len(ports) == 0 {
			log.Println("No serial ports found, retrying in 5s...")
			time.Sleep(5 * time.Second)
			continue
		}

		var activePort serial.Port
		
		for _, portName := range ports {
			mode := &serial.Mode{BaudRate: 115200}
			port, err := serial.Open(portName, mode)
			if err != nil {
				continue
			}

			port.SetReadTimeout(2 * time.Second)
			scanner := bufio.NewScanner(port)
			found := false

			// test stream for valid packets
			for i := 0; i < 5; i++ {
				if scanner.Scan() {
					if strings.HasPrefix(scanner.Text(), "@ ") {
						found = true
						break
					}
				}
			}

			if found {
				fmt.Printf("Connected to %s\n", portName)
				activePort = port
				break
			} else {
				port.Close()
			}
		}

		if activePort == nil {
			log.Println("No active telemetry stream found, retrying in 5s...")
			time.Sleep(5 * time.Second)
			continue
		}

		// listen until device drops
		scanner := bufio.NewScanner(activePort)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "@ ") {
				parseLine(line)
			}
		}

		log.Printf("Lost connection to ARES GPS, searching for new port...")
		activePort.Close()
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
