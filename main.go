// Ares Dashboard: ground-station telemetry for the ARES rocket.
// main.go starts the serial readers, the CSV recorder and the web server.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed public
var publicFS embed.FS

var debugMode string

var portFlag string

var listenFlag string

var stopping bool

func main() {
	flag.StringVar(&debugMode, "debug", "", "Debug mode: 'all' (raw stream) or 'gps' (telemetry packets only)")
	flag.StringVar(&portFlag, "port", "", "Specific COM port to listen on (e.g., COM3). Optional Featherweight override. Both receivers are discovered automatically by default.")
	flag.StringVar(&listenFlag, "listen", "127.0.0.1:8080", "HTTP listen address; use an explicit trusted LAN address only when sharing is intended")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serialDone := make(chan struct{})
	go func() { defer close(serialDone); serialReader(ctx) }()
	broadcastDone := make(chan struct{})
	go func() { defer close(broadcastDone); handleBroadcasts() }()
	go csvLogger()

	sub, _ := fs.Sub(publicFS, "public")
	http.Handle("/", http.FileServer(http.FS(sub)))

	http.HandleFunc("/ws", handleWebSocket)

	port := listenFlag
	fmt.Printf("Server starting on http://%s\n", port)
	server := &http.Server{Addr: port, Handler: nil, ReadHeaderTimeout: 5 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		log.Printf("Server stopped: %v", err)
	}
	stop()
	<-serialDone
	// Stop producers before closing the queue, then drain and sync the log.
	dataMutex.Lock()
	stopping = true
	close(csvChan)
	close(broadcast)
	dataMutex.Unlock()
	closeClients()
	<-broadcastDone
	<-csvDone
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}

}
