// server.go serves the dashboard page and streams live state to browsers
// over WebSockets.

package main

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var clients = make(map[*websocket.Conn]bool)

var clientsMu sync.Mutex

var clientHandlers sync.WaitGroup

var broadcast = make(chan TelemetryData, 100)

var broadcastDrops uint64

var upgrader = websocket.Upgrader{} // Reject cross-origin browser connections by default.

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	serveSocket(w, r, 20*time.Second)
}

func serveSocket(w http.ResponseWriter, r *http.Request, heartbeat time.Duration) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket Upgrade error:", err)
		return
	}

	// Serialize the snapshot and registration with the broadcaster's client list.
	// Event IDs let browsers discard earlier queued updates after a snapshot.
	clientsMu.Lock()
	dataMutex.Lock()
	refreshLocked(time.Now(), "STATUS", false)
	data := currentData
	shuttingDown := stopping
	dataMutex.Unlock()
	if shuttingDown {
		clientsMu.Unlock()
		conn.Close()
		return
	}
	data.Snapshot = true
	data.SnapshotClockMS = time.Now().UnixMilli()
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	err = conn.WriteJSON(data)
	if err == nil {
		clients[conn] = true
		clientHandlers.Add(1)
	}
	clientsMu.Unlock()
	if err != nil {
		conn.Close()
		return
	}
	defer clientHandlers.Done()
	defer func() {
		clientsMu.Lock()
		delete(clients, conn)
		clientsMu.Unlock()
		conn.Close()
	}()
	// A network that disappears without a TCP close can otherwise retain this
	// handler indefinitely on a quiet pad. Browsers answer WebSocket ping frames.

	conn.SetReadDeadline(time.Now().Add(3 * heartbeat))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(3 * heartbeat)) })
	done := make(chan struct{})
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-pingDone }()
	conn.SetReadLimit(1024)
	for {
		_, command, err := conn.ReadMessage()
		if err == nil && string(command) == "tare-gps" {
			tareGPS(time.Now())
		}
		if err != nil {
			return
		}
	}
}

// stopping is set before this call. The registration lock prevents a handler
// from being added after shutdown has collected the active sockets.
func closeClients() {
	clientsMu.Lock()
	for conn := range clients {
		conn.Close()
	}
	clientsMu.Unlock()
	clientHandlers.Wait()
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

func publishDisplayLocked() {
	select {
	case broadcast <- currentData:
	default:
		// Producers share dataMutex. If the consumer drained the queue since the
		// first select, there is already room. Always deliver the newest state.
		select {
		case <-broadcast:
			broadcastDrops++
			if broadcastDrops == 1 || broadcastDrops%1000 == 0 {
				log.Printf("Display queue skipped %d older updates", broadcastDrops)
			}
		default:
		}
		broadcast <- currentData
	}
}
