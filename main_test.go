package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func resetParser() {
	currentData = TelemetryData{}
	broadcast = make(chan TelemetryData, 100)
	csvChan = make(chan TelemetryData, 2000)
	stopping = false
}

func TestTimestampFormats(t *testing.T) {
	for _, line := range []string{
		"@ GPS_STAT 203 2020 11 15 01:20:21.986 CRC_OK Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
		"@ GPS_STAT Time 01:20:21.986 Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
	} {
		resetParser()
		parseLine(line)
		if currentData.Time != "01:20:21.986" {
			t.Fatalf("bad timestamp: %q", currentData.Time)
		}
		if currentData.Packet != "GPS_STAT" {
			t.Fatal("missing packet type")
		}
		if len(broadcast) != 1 || len(csvChan) != 1 {
			t.Fatal("sample not queued")
		}
	}
}

func TestRadioPacketIdentifiesRetainedGPS(t *testing.T) {
	resetParser()
	parseLine("@ GPS_STAT Time 12:00:00 Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9")
	parseLine("@ RX_NOMTK RSSI -80 trk_B_V 4000")
	if currentData.Packet != "RX_NOMTK" || currentData.Alt != "1000" || currentData.Volt != "4000" {
		t.Fatalf("bad merged state: %+v", currentData)
	}
}

func TestInvalidGPSDoesNotReplaceCurrentState(t *testing.T) {
	for _, line := range []string{
		"@ GPS_STAT Time 12:00:00 Alt 1000 lt 95 ln -93 Vel 0 0 10 Fix 3 # 9",
		"@ GPS_STAT Time 12:00:00 Alt NaN lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
		"@ GPS_STAT Time 12:00:00 Alt 1000 lt 42 ln -93 Fix 3 # 9",
		"@ GPS_STAT Time 12:00:00 CRC_OK GS Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
		"@ GPS_STAT Time 12:00:00 CRC_OK FND Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
		"@ GPS_STAT Time 12:00:00 CRC_ERR TRK Alt 1000 lt 42 ln -93 Vel 0 0 10 Fix 3 # 9",
	} {
		resetParser()
		currentData.Alt = "old"
		parseLine(line)
		if currentData.Alt != "old" || len(broadcast) != 0 {
			t.Fatalf("accepted invalid/non-tracker GPS: %s", line)
		}
	}
}

func TestLoggerDrainsQueueOnShutdown(t *testing.T) {
	resetParser()
	dir := t.TempDir()
	csvChan <- TelemetryData{Time: "12:00:00", Alt: "100"}
	csvChan <- TelemetryData{Time: "12:00:01", Alt: "101"}
	close(csvChan)
	logTelemetry(dir)
	files, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files: %v %v", files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("shutdown lost samples: %v %v", rows, err)
	}
}

func TestStoppedParserDoesNotSendToClosedQueue(t *testing.T) {
	resetParser()
	stopping = true
	close(csvChan)
	parseLine("@ RX_NOMTK RSSI -80 trk_B_V 4000")
	if len(broadcast) != 0 {
		t.Fatal("sent during shutdown")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }
func TestCSVErrorsAndEscaping(t *testing.T) {
	if err := writeCSVRows(brokenWriter{}, []TelemetryData{{Time: "12:00:00"}}); err == nil {
		t.Fatal("write error swallowed")
	}
	var b bytes.Buffer
	if err := writeCSVRows(&b, []TelemetryData{{Time: "a,b", Alt: "100"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil || len(rows) != 1 || len(rows[0]) != 10 || rows[0][0] != "a,b" {
		t.Fatalf("CSV corrupt: %v %v", rows, err)
	}
}

func TestWebSocketSnapshotAndOrigin(t *testing.T) {
	resetParser()
	server := httptest.NewServer(http.HandlerFunc(handleWebSocket))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var data TelemetryData
	if err = conn.ReadJSON(&data); err != nil || !data.Snapshot {
		t.Fatalf("snapshot: %v %+v", err, data)
	}
	conn.Close()
	headers := http.Header{"Origin": []string{"https://unrelated.example"}}
	c, resp, err := websocket.DefaultDialer.Dial(url, headers)
	if c != nil {
		c.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("cross-origin access accepted")
	}
}
