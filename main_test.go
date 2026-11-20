package main

// All backend tests. Go requires tests for package main to sit beside its
// source files, so this file stays in the project root.

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---- main ----

func resetParser() {
	dataMutex.Lock()
	defer dataMutex.Unlock()
	csvDone = make(chan struct{})
	receivers = map[string]*receiver{}
	padIdentity = ""
	autoTare.identity, autoTare.offsets = "", nil
	mission = missionTracker{}
	recorder.Store(nil)
	trackerClock.identity = ""
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
	writeFlight(dir, nil)
	// Rows without receiver input are status rows and belong to both files.
	for _, kind := range []string{gpsLog, loraLog} {
		if _, rows := readLog(t, dir, kind); len(rows) != 2 {
			t.Fatalf("shutdown lost %s samples: %v", kind, rows)
		}
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
	for _, kind := range []string{gpsLog, loraLog} {
		if err := writeCSVRows(brokenWriter{}, []TelemetryData{{Time: "12:00:00"}}, kind); err == nil {
			t.Fatal("write error swallowed")
		}
	}
	row := TelemetryData{Time: "a,b", Alt: "100", Packet: "GPS_STAT", input: inputRecord{gpsRole, "COM5", "x,\"y\"", "100"}}
	for kind, width := range map[string]int{gpsLog: 22, loraLog: 11} {
		var b bytes.Buffer
		if err := writeCSVRows(&b, []TelemetryData{row}, kind); err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(&b).ReadAll()
		header := strings.Split(strings.TrimSpace(logHeaders[kind]), ",")
		if err != nil || len(rows) != 1 || len(rows[0]) != width || len(header) != width || rows[0][5] != "x,\"y\"" {
			t.Fatalf("%s CSV corrupt: %v %v", kind, rows, err)
		}
		if kind == gpsLog && rows[0][6] != "a,b" {
			t.Fatal("GPS tracker time not escaped", rows[0])
		}
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

func TestGetLogDir(t *testing.T) {
	dir := getLogDir()
	if dir == "" {
		t.Fatal("expected non-empty log directory")
	}
	// Under 'go test', the binary runs in a temporary directory, so getLogDir falls back to "logs"
	if dir != "logs" && !filepath.IsAbs(dir) {
		t.Fatalf("unexpected log dir format: %s", dir)
	}
}

func TestDiscoverPorts(t *testing.T) {
	ports, err := discoverPorts()
	if err != nil {
		t.Fatalf("discoverPorts error: %v", err)
	}
	t.Logf("Discovered candidate ports: %v", ports)
	for _, p := range ports {
		if strings.Contains(strings.ToLower(p.name), "bluetooth") {
			t.Fatalf("expected bluetooth port to be excluded, got: %s", p)
		}
	}
}

func parseLine(line string) {
	if receivers["TEST"] == nil {
		receivers["TEST"] = &receiver{portInfo: portInfo{name: "TEST"}}
	}
	receiveLine(receivers["TEST"], line, time.Now())
}

// ---- receiver ----

type testUSB struct {
	chunks chan string
	closed chan struct{}
}

func newUSB() *testUSB { return &testUSB{make(chan string, 32), make(chan struct{})} }
func (p *testUSB) Read(b []byte) (int, error) {
	select {
	case s, ok := <-p.chunks:
		if !ok {
			return 0, io.EOF
		}
		return copy(b, s), nil
	case <-time.After(10 * time.Millisecond):
		return 0, nil
	}
}
func (p *testUSB) Close() error                       { close(p.closed); return nil }
func (p *testUSB) SetReadTimeout(time.Duration) error { return nil }
func view() TelemetryData                             { dataMutex.RLock(); defer dataMutex.RUnlock(); return currentData }
func waitView(t *testing.T, check func(TelemetryData) bool) {
	t.Helper()
	limit := time.Now().Add(3 * time.Second)
	for time.Now().Before(limit) {
		if check(view()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unexpected final state: %+v", view())
}

const gpsRecord = "@ GPS_STAT Time 12:00:00 Alt 840 lt 42 ln -93 Vel 10 0 5 Fix 3 # 9"

func TestAutomaticDualUSB(t *testing.T) {
	for _, gpsFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "gps-first", false: "lora-first"}[gpsFirst], func(t *testing.T) {
			resetParser()
			ctx, cancel := context.WithCancel(context.Background())
			a, b, c := newUSB(), newUSB(), newUSB()
			devices := map[string]*testUSB{"COM8": a, "COM2": b, "COM11": c}
			var mu sync.Mutex
			opens := map[string]int{}
			open := func(name string) (serialInput, error) {
				mu.Lock()
				defer mu.Unlock()
				opens[name]++
				if opens[name] > 1 {
					return nil, errors.New("double open")
				}
				return devices[name], nil
			}
			t.Cleanup(func() {
				cancel()
				syncPorts(ctx, nil, open, time.Now())
				readerWorkers.Wait()
				for name, p := range devices {
					mu.Lock()
					n := opens[name]
					mu.Unlock()
					if n > 0 {
						select {
						case <-p.closed:
						case <-time.After(time.Second):
							t.Error("reader leaked", name)
						}
					}
				}
			})
			gps := portInfo{"COM8", "gps-id"}
			lr := portInfo{"COM2", "lora-id"}
			digits := func(p *testUSB) { p.chunks <- "1100\r\n0\r\n"; time.Sleep(175 * time.Millisecond); p.chunks <- "0\n" }
			if gpsFirst {
				syncPorts(ctx, []portInfo{gps}, open, time.Now())
				a.chunks <- gpsRecord + "\r\n"
				waitView(t, func(d TelemetryData) bool { return d.GPSLive })
			} else {
				syncPorts(ctx, []portInfo{lr}, open, time.Now())
				digits(b)
				waitView(t, func(d TelemetryData) bool { return d.LoRaLive })
			}
			syncPorts(ctx, []portInfo{lr, gps}, open, time.Now())
			if gpsFirst {
				digits(b)
			} else {
				a.chunks <- gpsRecord + "\r\n"
			}
			waitView(t, func(d TelemetryData) bool { return d.GPSLive && d.Source == loraRole && d.Height == "0" })
			tareGPS(time.Now())
			a.chunks <- strings.Replace(strings.Replace(gpsRecord, "Alt 840", "Alt 870", 1), "12:00:00", "12:00:01", 1) + "\n"
			waitView(t, func(d TelemetryData) bool { return d.Alt == "870" && d.GPSPad == "840" })
			close(b.chunks)
			waitView(t, func(d TelemetryData) bool { return d.Source == gpsRole && d.Height == "30.00" })
			syncPorts(ctx, []portInfo{gps, {"COM11", "lora-id"}}, open, time.Now())
			digits(c)
			waitView(t, func(d TelemetryData) bool { return d.LoRaPort == "COM11" && d.Source == loraRole && d.GPSPad == "840" })
			mu.Lock()
			defer mu.Unlock()
			if opens[gps.name] != 1 {
				t.Fatal("interrupted healthy GPS")
			}
		})
	}
}

func TestRadioSilenceAndRecovery(t *testing.T) {
	resetParser()
	start := time.Now()
	gps := &receiver{portInfo: portInfo{name: "A"}}
	lr := &receiver{portInfo: portInfo{name: "B"}}
	receivers["A"] = gps
	receivers["B"] = lr
	feed := func(r *receiver, s string, ms int) { receiveLine(r, s, start.Add(time.Duration(ms)*time.Millisecond)) }
	tick := func(ms int) { refreshLocked(start.Add(time.Duration(ms)*time.Millisecond), "STATUS", false) }
	feed(gps, gpsRecord, 0)
	tareGPS(start)
	feed(lr, "900", 0)
	feed(lr, "0", 100)
	feed(lr, "0", 200)
	if currentData.Source != loraRole || currentData.Height != "0" || currentData.Climb != "" {
		t.Fatal(currentData)
	}
	feed(lr, "10", 300)
	feed(lr, "20", 400)
	if currentData.Climb != "100.00" {
		t.Fatalf("same-source slope %v", currentData)
	}
	feed(gps, strings.Replace(strings.Replace(gpsRecord, "Alt 840", "Alt 860", 1), "12:00:00", "12:00:01", 1), 2000)
	tick(2400)
	if currentData.Source != gpsRole || currentData.Height != "20.00" || currentData.Climb != "5" {
		t.Fatal(currentData)
	}
	feed(lr, "5000", 2500)
	feed(lr, "30", 2600)
	tick(2600)
	if currentData.Source != gpsRole {
		t.Fatal("premature recovery")
	}
	feed(lr, "30", 2700)
	if currentData.Source != loraRole || currentData.Height != "30" || currentData.Climb != "" {
		t.Fatal(currentData)
	}
	feed(gps, strings.Replace(strings.Replace(gpsRecord, "Fix 3", "Fix 2", 1), "12:00:00", "12:00:02", 1), 2800)
	feed(gps, "@ RX_NOMTK RSSI -70 trk_B_V 4000", 4800)
	tick(4800)
	if currentData.Source != "none" || !currentData.GPSLive || currentData.GPSFixFresh || currentData.Height != "" {
		t.Fatal(currentData)
	}
}

func TestRejectNoiseAndAmbiguousNumericDevices(t *testing.T) {
	resetParser()
	now := time.Now()
	r := &receiver{portInfo: portInfo{name: "X"}}
	receivers["X"] = r
	for _, s := range []string{"@ test", "@ GPS_STAT bad", "PING 12", "22 -77 8 GOOD", "1", "NaN", "Inf", "1e999"} {
		receiveLine(r, s, now)
		now = now.Add(200 * time.Millisecond)
	}
	if r.role != "" || len(broadcast) != 0 || len(csvChan) != 1 || (<-csvChan).Packet != "NUMERIC_PROBE" {
		t.Fatal("probe published invalid samples")
	}
	for _, name := range []string{"X", "Y"} {
		p := &receiver{portInfo: portInfo{name: name}}
		receivers[name] = p
		for i := 0; i < 3; i++ {
			receiveLine(p, "-2.5", now)
			now = now.Add(100 * time.Millisecond)
		}
	}
	if currentData.LoRaPort != "ambiguous" || currentData.Source != "none" {
		t.Fatal(currentData)
	}
	delete(receivers, "Y")
	refreshLocked(now, "STATUS", false)
	if currentData.Source != loraRole || currentData.Height != "-2.5" {
		t.Fatal(currentData)
	}
	receiveLine(r, "1234", now)
	if currentData.Height != "-2.5" {
		t.Fatal("obsolete reader altered state")
	}
}

// ---- integration ----

// Exercise the browser's tare command and compare what a real WebSocket client
// receives with the flight log written by the normal CSV writer.
func TestWebSocketAndCSVShareSelection(t *testing.T) {
	resetParser()
	done := make(chan struct{})
	go func() { defer close(done); handleBroadcasts() }()
	defer func() { close(broadcast); <-done }()
	srv := httptest.NewServer(http.HandlerFunc(handleWebSocket))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var state TelemetryData
	if err := ws.ReadJSON(&state); err != nil || !state.Snapshot {
		t.Fatalf("snapshot: %v", err)
	}
	var seen []TelemetryData
	readThrough := func(packet string) TelemetryData {
		t.Helper()
		for {
			var event TelemetryData
			if err := ws.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, event)
			if event.Packet == packet {
				return event
			}
		}
	}
	gps := &receiver{portInfo: portInfo{name: "GPS"}}
	lr := &receiver{portInfo: portInfo{name: "RADIO"}}
	dataMutex.Lock()
	receivers["GPS"] = gps
	receivers["RADIO"] = lr
	dataMutex.Unlock()
	now := time.Now()
	receiveLine(gps, gpsRecord, now)
	if readThrough("GPS_STAT").Source != "none" {
		t.Fatal("GPS used before tare")
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("tare-gps")); err != nil {
		t.Fatal(err)
	}
	if d := readThrough("GPS_TARE"); d.GPSPad != "840" || d.Height != "0.00" {
		t.Fatal(d)
	}
	for i, s := range []string{"900", "0", "12"} {
		receiveLine(lr, s, now.Add(time.Duration(i)*100*time.Millisecond))
	}
	if d := readThrough("LORA_ALT"); d.Height != "12" || d.Source != loraRole {
		t.Fatal(d)
	}
	dataMutex.Lock()
	refreshLocked(now.Add(2250*time.Millisecond), "STATUS", false)
	dataMutex.Unlock()
	if d := readThrough("STATUS"); d.Source != gpsRole {
		t.Fatal(d)
	}
	dir := t.TempDir()
	dataMutex.Lock()
	stopping = true
	close(csvChan)
	dataMutex.Unlock()
	writeFlight(dir, nil)
	records := map[string]map[string]string{}
	for _, kind := range []string{gpsLog, loraLog} {
		header, rows := readLog(t, dir, kind)
		for _, row := range rows {
			fields := map[string]string{}
			for i, name := range header {
				fields[name] = row[i]
			}
			records[fields["EventID"]] = fields
		}
	}
	for _, e := range seen {
		row := records[strconv.FormatUint(e.EventID, 10)]
		if row == nil || row["Event"] != e.Packet || row["Source"] != e.Source || row["SelectedAlt(ft AGL)"] != e.Height || row["SelectionReason"] != e.Reason {
			t.Fatalf("CSV and WebSocket differ for event %d", e.EventID)
		}
	}
}

// ---- stress ----

// Count actual CSV records, including probe readings, with a queue much smaller
// than a USB burst. Input from multiple ports may arrive in either order.
func TestConcurrentStreamsRecordedExactlyOnce(t *testing.T) {
	resetParser()
	csvChan = make(chan TelemetryData, 2)
	dir := t.TempDir()
	finished := make(chan struct{})
	go func() { defer close(finished); writeFlight(dir, nil) }()
	gps := &receiver{portInfo: portInfo{name: "GPS"}}
	lr := &receiver{portInfo: portInfo{name: "LR"}}
	receivers[gps.name] = gps
	receivers[lr.name] = lr
	base := time.Now()
	const n = 6000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			at := base.Add(time.Duration(i) * 100 * time.Millisecond)
			if i%2 == 0 {
				receiveLine(gps, fmt.Sprintf("@ GPS_STAT Time %s Alt %d lt 42 ln -93 Vel 1 0 2 Fix 3 # 9", at.Format("15:04:05.000"), i), at)
			} else {
				receiveLine(gps, fmt.Sprintf("@ RX_NOMTK RSSI -80 trk_B_V %d", i), at)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			receiveLine(lr, strconv.Itoa(i), base.Add(time.Duration(i)*100*time.Millisecond))
		}
	}()
	wg.Wait()
	dataMutex.Lock()
	stopping = true
	close(csvChan)
	dataMutex.Unlock()
	<-finished
	gpsHeader, gpsRows := readLog(t, dir, gpsLog)
	loraHeader, loraRows := readLog(t, dir, loraLog)
	if len(gpsRows) != n || len(loraRows) != n {
		t.Fatalf("received %d GPS and %d LoRa of %d each", len(gpsRows), len(loraRows), n)
	}
	counts := map[string]int{}
	check := func(header []string, rows [][]string) {
		col := columns(header)
		for _, row := range rows {
			port := row[col["Port"]]
			i := counts[port]
			counts[port]++
			value := strconv.Itoa(i)
			raw := row[col["Record"]]
			alt := row[col["Altitude(ft)"]]
			received := base.Add(time.Duration(i) * 100 * time.Millisecond)
			if row[col["ReceivedUTC"]] != received.UTC().Format(time.RFC3339Nano) {
				t.Fatal("receipt time mismatch", port, i)
			}
			switch port {
			case "GPS":
				if i%2 == 0 {
					if !strings.Contains(raw, " Alt "+value+" ") || alt != value || row[col["RSSI"]] != "" {
						t.Fatal("GPS order/content", i, row)
					}
				} else if !strings.HasSuffix(raw, "trk_B_V "+value) || alt != "" || row[col["Battery(mV)"]] != value {
					t.Fatal("radio order/content", i, row)
				}
			case "LR":
				if raw != value || alt != value {
					t.Fatal("LoRa order/content", i)
				}
				if (i < 2) != (row[col["Event"]] == "NUMERIC_PROBE") {
					t.Fatal("probe label", i)
				}
			default:
				t.Fatal("unexpected port", port)
			}
		}
	}
	check(gpsHeader, gpsRows)
	check(loraHeader, loraRows)
	if counts["GPS"] != n || counts["LR"] != n {
		t.Fatal(counts)
	}
}

func TestAmbiguousInputsAndRecoveryKeepOrigin(t *testing.T) {
	resetParser()
	now := time.Now()
	for _, port := range []string{"X", "Y"} {
		r := &receiver{portInfo: portInfo{name: port}}
		receivers[port] = r
		for i := 0; i < 3; i++ {
			receiveLine(r, strconv.Itoa(i), now)
			now = now.Add(100 * time.Millisecond)
		}
	}
	var last TelemetryData
	for len(csvChan) > 0 {
		last = <-csvChan
	}
	if last.LoRaPort != "ambiguous" || last.Source != "none" || last.input.port != "Y" || last.input.altitude != "2" {
		t.Fatal(last)
	}
	receiveLine(receivers["Y"], " -1.5 ", now.Add(3*time.Second))
	row := <-csvChan
	if row.Packet != "LORA_REQUALIFY" || row.EventID != 0 || row.input.raw != " -1.5 " || row.input.altitude != "-1.5" {
		t.Fatal(row)
	}
}

func TestExpirySurvivesFullDisplayQueue(t *testing.T) {
	resetParser()
	broadcast = make(chan TelemetryData, 1)
	r := &receiver{portInfo: portInfo{name: "L"}}
	receivers["L"] = r
	now := time.Now()
	for i := 0; i < 3; i++ {
		receiveLine(r, "55", now.Add(time.Duration(i)*100*time.Millisecond))
	}
	refreshLocked(now.Add(3*time.Second), "STATUS", false)
	if d := <-broadcast; d.Source != "none" || d.Height != "" || d.LoRaLive {
		t.Fatal("stale final update", d)
	}
}

func TestWriterFailureReleasesProducer(t *testing.T) {
	resetParser()
	csvChan = make(chan TelemetryData)
	finished := make(chan struct{})
	go func() { defer close(finished); logRowLocked(TelemetryData{}) }()
	close(csvDone)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("producer stuck after logger failure")
	}
}

func TestClientsRetireOnSilenceAndShutdown(t *testing.T) {
	resetParser()
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveSocket(w, r, 20*time.Millisecond) }))
	defer short.Close()
	dial := func(url string) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		var d TelemetryData
		c.SetReadDeadline(time.Now().Add(time.Second))
		if err = c.ReadJSON(&d); err != nil || !d.Snapshot {
			t.Fatal("snapshot", err)
		}
		return c
	}
	dead := dial(short.URL)
	defer dead.Close() // No reads or pongs after the snapshot.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		clientsMu.Lock()
		n := len(clients)
		clientsMu.Unlock()
		if n == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("half-open socket retained")
		case <-time.After(5 * time.Millisecond):
		}
	}
	normal := httptest.NewServer(http.HandlerFunc(handleWebSocket))
	defer normal.Close()
	for i := 0; i < 20; i++ {
		c := dial(normal.URL)
		if i%2 == 0 {
			c.Close()
		} else {
			defer c.Close()
		}
	}
	dataMutex.Lock()
	stopping = true
	dataMutex.Unlock()
	ended := make(chan struct{})
	go func() { closeClients(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("socket handlers did not exit")
	}
	clientsMu.Lock()
	defer clientsMu.Unlock()
	if len(clients) != 0 {
		t.Fatal("remaining clients", len(clients))
	}
}

// ---- flight fixes ----

func TestOldEpochCannotRenewFallback(t *testing.T) {
	resetParser()
	now := time.Now()
	gps := &receiver{portInfo: portInfo{"G", "serial"}}
	receivers["G"] = gps
	feed := func(stamp string, ms int) {
		receiveLine(gps, strings.Replace(gpsRecord, "12:00:00", stamp, 1), now.Add(time.Duration(ms)*time.Millisecond))
	}
	feed("23:59:59", 0)
	tareGPS(now)
	feed("23:59:59.000", 3200)
	if currentData.Source != "none" || !currentData.GPSLive || currentData.GPSFixFresh || currentData.Packet != "GPS_NONADVANCING" {
		t.Fatal(currentData)
	}
	feed("00:00:00", 3300)
	if currentData.Source != gpsRole {
		t.Fatal("midnight", currentData)
	}
	feed("23:59:59", 3400)
	if currentData.Time != "00:00:00" {
		t.Fatal("old epoch accepted", currentData)
	}
	delete(receivers, "G")
	gps = &receiver{portInfo: portInfo{"G2", "serial"}}
	receivers["G2"] = gps
	feed("00:00:00", 7000)
	if currentData.GPSFixFresh {
		t.Fatal("cached reconnect")
	}
	feed("00:00:01", 7100)
	if currentData.Source != gpsRole || currentData.GPSPad != "840" {
		t.Fatal("reconnect", currentData)
	}
	receiveLine(gps, strings.NewReplacer("12:00:00", "00:00:01.000", "Fix 3", "Fix 2").Replace(gpsRecord), now.Add(7200*time.Millisecond))
	if currentData.GPSFixFresh {
		t.Fatal("same-epoch fix loss ignored")
	}
}

func TestServerOwnsPadReference(t *testing.T) {
	resetParser()
	now := time.Now()
	gps := &receiver{portInfo: portInfo{name: "G"}}
	receivers["G"] = gps
	receiveLine(gps, gpsRecord, now)
	if !currentData.CanTare {
		t.Fatal("pad unavailable")
	}
	tareGPS(now)
	if currentData.CanTare {
		t.Fatal("baseline can be overwritten")
	}
	airborne := strings.NewReplacer("12:00:00", "12:00:01", "Alt 840", "Alt 1840", "0 5 Fix", "0 180 Fix").Replace(gpsRecord)
	receiveLine(gps, airborne, now.Add(time.Second))
	tareGPS(now.Add(time.Second))
	if !currentData.TareLocked || currentData.Height != "1000.00" || currentData.GPSPad != "840" {
		t.Fatal(currentData)
	}
	// Even a new client arriving later at apogee cannot set zero.
	receiveLine(gps, strings.NewReplacer("12:00:01", "12:00:02", "0 180 Fix", "0 0 Fix").Replace(airborne), now.Add(2*time.Second))
	tareGPS(now.Add(2 * time.Second))
	if currentData.GPSPad != "840" {
		t.Fatal(currentData)
	}
	resetParser()
	lr := &receiver{portInfo: portInfo{name: "L"}}
	receivers["L"] = lr
	for i := 0; i < 3; i++ {
		receiveLine(lr, "1200", now.Add(time.Duration(i)*100*time.Millisecond))
	}
	receivers["G"] = gps
	receiveLine(gps, strings.Replace(gpsRecord, "12:00:00", "12:00:03", 1), now.Add(time.Second))
	tareGPS(now.Add(time.Second))
	if !currentData.TareLocked || currentData.GPSPad != "" {
		t.Fatal("late GPS after LoRa ascent", currentData)
	}
}

type syncFailure struct{ io.Writer }

func (syncFailure) Sync() error { return errors.New("sync failed") }

func TestRecordingAcknowledgesDurability(t *testing.T) {
	resetParser()
	records := make(chan TelemetryData, 1)
	records <- TelemetryData{input: inputRecord{source: loraRole}}
	close(records)
	var status RecordingStatus
	if err := recordBatches(syncFailure{io.Discard}, syncFailure{io.Discard}, "test", &status, records); err == nil || status.LoRa != 0 {
		t.Fatal("failed sync acknowledged", status, err)
	}
	invalid := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(invalid, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeFlight(invalid, nil); err == nil || recorder.Load().State != "error" {
		t.Fatal("open failure invisible", err)
	}
	csvChan = make(chan TelemetryData, 4)
	for _, source := range []string{gpsRole, loraRole, loraRole, "Unclassified numeric"} {
		csvChan <- TelemetryData{input: inputRecord{source: source}}
	}
	close(csvChan)
	if err := writeFlight(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Load(); got.GPS != 1 || got.LoRa != 2 || got.State != "stopped" {
		t.Fatal(got)
	}
	csvChan = make(chan TelemetryData, 4)
	refreshLocked(time.Now(), "STATUS", false)
	initial := len(csvChan)
	(RecordingStatus{State: "recording", GPS: 12, LoRa: 24}).publish()
	refreshLocked(time.Now(), "STATUS", false)
	if currentData.Packet != "RECORDER" || currentData.Recording.LoRa != 24 || len(csvChan) != initial {
		t.Fatal("acknowledgement loop", currentData)
	}
}

// Exercise the normal client command after a second browser opens in flight.
func TestReopenedBrowserCannotResetPad(t *testing.T) {
	resetParser()
	now := time.Now()
	gps := &receiver{portInfo: portInfo{name: "COM8"}}
	receivers["COM8"] = gps
	receiveLine(gps, gpsRecord, now)
	tareGPS(now)
	receiveLine(gps, strings.NewReplacer("12:00:00", "12:00:01", "Alt 840", "Alt 1840", "0 5 Fix", "0 180 Fix").Replace(gpsRecord), time.Now())
	srv := httptest.NewServer(http.HandlerFunc(handleWebSocket))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(time.Second))
	var shot TelemetryData
	if err := ws.ReadJSON(&shot); err != nil || !shot.Snapshot || !shot.TareLocked || shot.CanTare || shot.GPSPad != "840" {
		t.Fatal(shot, err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("tare-gps")); err != nil {
		t.Fatal(err)
	}
	// The protocol-level pong follows the preceding text command on this socket.
	pong := make(chan struct{})
	ws.SetPongHandler(func(string) error { close(pong); return nil })
	reader := make(chan struct{})
	go func() { defer close(reader); ws.ReadMessage() }()
	if err := ws.WriteControl(websocket.PingMessage, []byte("check"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pong:
	case <-time.After(time.Second):
		t.Fatal("command not processed")
	}
	if got := view(); got.GPSPad != "840" || got.Height != "1000.00" {
		t.Fatal("shared baseline changed", got)
	}
	ws.Close()
	<-reader
}

// ---- launch log ----

func ascentRow(port string, h int, at time.Time) TelemetryData {
	s := strconv.Itoa(h)
	row := TelemetryData{Packet: "LORA_ALT", input: inputRecord{loraRole, port, s, s}}
	row.Received = at.UTC().Format(time.RFC3339Nano)
	row.LoRaLive = true
	row.LoRaPort = port
	return row
}
func TestArmedPadNeverCreatesFile(t *testing.T) {
	resetParser()
	dir := t.TempDir()
	base := time.Now()
	for i := 0; i < 400; i++ {
		row := ascentRow("A", i%11-5, base.Add(time.Duration(i)*50*time.Millisecond))
		if i == 0 {
			row.Packet = "NUMERIC_PROBE"
			row.input.altitude = "541"
		}
		if i == 100 {
			row.input.altitude = "200"
		} // isolated spike
		if i >= 200 {
			row.input.port = "B"
			row.LoRaPort = "B"
			row.input.altitude = "300"
		} // isolated device step
		csvChan <- row
	}
	close(csvChan)
	if err := logTelemetry(dir); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("pad log created")
	}
}

// readLog returns the header and rows of one receiver's CSV for the flight.
func readLog(t *testing.T, dir, kind string) ([]string, [][]string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, logFolders[kind], "*.csv"))
	if len(files) != 1 {
		t.Fatal("expected one", kind, "CSV", files)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows[0], rows[1:]
}

func columns(header []string) map[string]int {
	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}
	return col
}

// Rows carry the mission header. Launch at row 170, landing at row 300, 20 Hz.
func TestCSVWindowFiveSecondPreRollFiveSecondsAfterLanding(t *testing.T) {
	resetParser()
	dir := t.TempDir()
	base := time.Now()
	at := func(i int) time.Time { return base.Add(time.Duration(i) * 50 * time.Millisecond) }
	done := make(chan error, 1)
	go func() { done <- logTelemetry(dir) }()
	for i := 0; i < 500; i++ {
		row := ascentRow("A", i, at(i))
		row.EventID = uint64(i + 1)
		if i >= 170 {
			row.LaunchMS = at(170).UnixMilli()
		}
		if i >= 300 {
			row.LandedMS = at(300).UnixMilli()
		}
		select {
		case csvChan <- row:
		case <-csvDone:
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := recorder.Load(); got.State != "complete" || got.LoRa == 0 {
		t.Fatal("recorder state", got)
	}
	_, rows := readLog(t, dir, loraLog)
	first, _ := strconv.Atoi(rows[0][1])
	last, _ := strconv.Atoi(rows[len(rows)-1][1])
	// EventID is index+1: pre-roll starts 5.0 s (100 rows) before launch and
	// the last row is 5.0 s (100 rows) after landing.
	if first != 71 || last != 401 || len(rows) != 331 {
		t.Fatalf("window first=%d last=%d rows=%d", first, last, len(rows))
	}
	for j, r := range rows {
		if r[1] != strconv.Itoa(71+j) || r[6] != strconv.Itoa(70+j) {
			t.Fatal("raw row changed or missing", j, r)
		}
	}
}

// Receivers that fall silent after landing must not keep the file open.
func TestCSVClosesOnTimerWhenSilentAfterLanding(t *testing.T) {
	resetParser()
	dir := t.TempDir()
	now := time.Now()
	done := make(chan error, 1)
	go func() { done <- logTelemetry(dir) }()
	row := ascentRow("A", 3, now)
	row.LaunchMS = now.Add(-30 * time.Second).UnixMilli()
	row.LandedMS = now.Add(-4800 * time.Millisecond).UnixMilli()
	csvChan <- row
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recorder stayed open after the post-landing window")
	}
	if recorder.Load().State != "complete" {
		t.Fatal(recorder.Load())
	}
	if _, rows := readLog(t, dir, loraLog); len(rows) != 1 {
		t.Fatal("expected the in-window row", rows)
	}
	// Producers must not block once recording has finished.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		dataMutex.Lock()
		defer dataMutex.Unlock()
		for i := 0; i < 5000; i++ {
			logRowLocked(row)
		}
	}()
	close(csvDone)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("producer blocked after recording completed")
	}
}

// Feed the bench simulator profile through the real receiver, mission and
// recorder path and check the CSV starts near launch and ends after landing.
func TestSimulatedFlightThroughReceiverAndRecorder(t *testing.T) {
	resetParser()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- logTelemetry(dir) }()
	r := &receiver{portInfo: portInfo{name: "COM6"}}
	dataMutex.Lock()
	receivers[r.name] = r
	dataMutex.Unlock()
	start := time.Now()
	for i := 0; i <= 1800; i++ {
		seconds := float64(i) / 20
		u := max(0.0, min(1.0, (seconds-10)/30))
		if seconds >= 40 {
			u = max(0.0, 1-(seconds-40)/30)
		}
		h := math.Round(1500 * u * u * (3 - 2*u))
		receiveLine(r, strconv.Itoa(int(h)), start.Add(time.Duration(i)*50*time.Millisecond))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	dataMutex.RLock()
	launch, landed := currentData.LaunchMS, currentData.LandedMS
	dataMutex.RUnlock()
	if launch == 0 || landed == 0 {
		t.Fatal("mission did not reach landing")
	}
	header, rows := readLog(t, dir, loraLog)
	col := map[string]int{}
	for i, k := range header {
		col[k] = i
	}
	parse := func(r []string) time.Time {
		at, err := time.Parse(time.RFC3339Nano, r[col["ReceivedUTC"]])
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	lead := time.UnixMilli(launch).Sub(parse(rows[0]))
	tail := parse(rows[len(rows)-1]).Sub(time.UnixMilli(landed))
	if lead < 4900*time.Millisecond || lead > 5050*time.Millisecond {
		t.Fatal("pre-roll", lead)
	}
	if tail < 4950*time.Millisecond || tail > 5001*time.Millisecond {
		t.Fatal("post-landing", tail)
	}
	t.Logf("CSV: %d rows, %v before launch to %v after landing", len(rows), lead, tail)
}

func TestReplayPadCapture(t *testing.T) {
	path := os.Getenv("ARES_PAD_REPLAY")
	if path == "" {
		t.Skip("optional local pad capture")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	// Accept the current LoRa log and the older single-file flight log.
	col := columns(rows[0])
	timeCol, eventCol, portCol, altCol := col["ReceivedUTC"], col["Event"], col["Port"], col["Altitude(ft)"]
	if old, ok := col["InputAltitude(ft)"]; ok {
		portCol, altCol = col["InputPort"], old
	}
	var det ascentDetector
	n := 0
	for _, r := range rows[1:] {
		if r[eventCol] != "LORA_ALT" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, r[timeCol])
		if err != nil {
			t.Fatal(err)
		}
		h, err := strconv.ParseFloat(strings.TrimSpace(r[altCol]), 64)
		if err != nil {
			t.Fatal(err)
		}
		if det.rising(r[portCol], at, h) {
			t.Fatal("false launch on pad", r[timeCol])
		}
		n++
	}
	if n == 0 {
		t.Fatal("no samples")
	}
	t.Logf("replayed %d readings", n)
}

// ---- mission phase ----

func TestLoRaOnlyHeaderThroughLanding(t *testing.T) {
	resetParser()
	r := &receiver{portInfo: portInfo{name: "COM6"}}
	receivers[r.name] = r
	start := time.Unix(1700000000, 0)
	var phases []string
	for i := 0; i <= 1600; i++ {
		seconds := float64(i) / 20
		u := max(0.0, min(1.0, (seconds-10)/30))
		if seconds >= 40 {
			u = max(0.0, 1-(seconds-40)/30)
		}
		h := math.Round(1500 * u * u * (3 - 2*u))
		receiveLine(r, strconv.Itoa(int(h)), start.Add(time.Duration(i)*50*time.Millisecond))
		if len(phases) == 0 || currentData.Phase != phases[len(phases)-1] {
			phases = append(phases, currentData.Phase)
		}
	}
	if !slices.Equal(phases, []string{"", "PAD", "ASCENT", "APOGEE", "DESCENT", "LANDED"}) {
		t.Fatal(phases)
	}
	launch, land := currentData.LaunchMS-start.UnixMilli(), currentData.LandedMS-start.UnixMilli()
	if launch < 10000 || launch > 16000 || land < 72000 || land > 77000 {
		t.Fatalf("launch/landing offsets %d/%d", launch, land)
	}
}

func TestMissionLandingRule(t *testing.T) {
	start := time.Unix(1700000000, 0)
	in := inputRecord{source: loraRole, port: "COM6"}
	run := func(t *testing.T, heights func(ms int) (string, string)) (missionTracker, int) {
		t.Helper()
		m := missionTracker{missionHeader: missionHeader{Phase: "DESCENT", LaunchMS: start.UnixMilli()}}
		for ms := 0; ms <= 20000; ms += 100 {
			h, event := heights(ms)
			if h == "" {
				continue
			}
			d := TelemetryData{FlightView: FlightView{LoRaLive: true, LoRaPort: "COM6", Height: h, Climb: "0"}}
			m.observe(d, event, in, start.Add(time.Duration(ms)*time.Millisecond))
			if m.Phase == "LANDED" {
				return m, ms
			}
		}
		return m, -1
	}
	t.Run("field 25 ft above pad lands after 5 s steady", func(t *testing.T) {
		_, at := run(t, func(ms int) (string, string) { return strconv.Itoa(25 + ms/100%5 - 2), "LORA_ALT" })
		if at < 5000 || at > 5100 {
			t.Fatal("landed at", at)
		}
	})
	t.Run("steady 35 ft away never lands", func(t *testing.T) {
		if _, at := run(t, func(int) (string, string) { return "-35", "LORA_ALT" }); at != -1 {
			t.Fatal("landed outside the band at", at)
		}
	})
	t.Run("descending under canopy through the band does not land", func(t *testing.T) {
		// 15 ft/s from +30 ft, settling at -28 ft (field below the pad) after ~3.9 s.
		_, at := run(t, func(ms int) (string, string) {
			return strconv.Itoa(max(-28, 30-15*ms/1000)), "LORA_ALT"
		})
		if at < 7000 || at > 9100 {
			t.Fatal("landed at", at)
		}
	})
	t.Run("receiver gap restarts the hold", func(t *testing.T) {
		_, at := run(t, func(ms int) (string, string) {
			if ms > 3000 && ms < 6000 {
				return "", ""
			}
			return "0", "LORA_ALT"
		})
		if at < 11000 || at > 11100 {
			t.Fatal("landed at", at)
		}
	})
	t.Run("status events never count as samples", func(t *testing.T) {
		if _, at := run(t, func(ms int) (string, string) {
			if ms < 1000 {
				return "0", "LORA_ALT"
			}
			return "0", "STATUS"
		}); at != -1 {
			t.Fatal("landed without fresh samples at", at)
		}
	})
}

// ---- auto tare ----

func gpsLine(at time.Time, alt float64, horizontal, up int) string {
	return fmt.Sprintf("@ GPS_STAT Time %s Alt %.1f lt 42 ln -93 Vel %d 0 %d Fix 3 # 9",
		at.UTC().Format("15:04:05"), alt, horizontal, up)
}

// GPS altitude reads 840 ft above LoRa with a few feet of noise. The automatic
// reference averages the offsets, locks at launch and gives a seamless fallback.
func TestAutoTareMatchesGPSToLoRaAndLocksAtLaunch(t *testing.T) {
	resetParser()
	close(csvDone) // no recorder in this test
	gps := &receiver{portInfo: portInfo{name: "COM5"}}
	lora := &receiver{portInfo: portInfo{name: "COM6"}}
	receivers[gps.name], receivers[lora.name] = gps, lora
	start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	truth := func(ms int) float64 { // pad for 20 s, then climb at 50 ft/s
		if ms < 20000 {
			return 0
		}
		return float64(ms-20000) / 20
	}
	gpsNoise := []float64{3, -2, 4, -4, 1, -1, 2, -3, 0, 0}
	var padAfterThree string
	var lockedPad string
	for ms := 0; ms <= 40000; ms += 50 {
		at := start.Add(time.Duration(ms) * time.Millisecond)
		if ms <= 30000 {
			loraNoise := float64((ms/50)%5 - 2)
			receiveLine(lora, strconv.Itoa(int(math.Round(truth(ms)+loraNoise))), at)
		}
		if ms%1000 == 0 {
			up := 0
			if ms >= 20000 {
				up = 50
			}
			receiveLine(gps, gpsLine(at, 840+truth(ms)+gpsNoise[(ms/1000)%10], 0, up), at)
			if ms == 3000 {
				padAfterThree = currentData.GPSPad
			}
		}
		if currentData.LaunchMS != 0 && lockedPad == "" {
			lockedPad = currentData.GPSPad
		}
		if lockedPad != "" && currentData.GPSPad != lockedPad {
			t.Fatalf("pad reference changed after launch: %s -> %s", lockedPad, currentData.GPSPad)
		}
	}
	if padAfterThree == "" {
		t.Fatal("no automatic reference after three GPS fixes")
	}
	pad, _ := strconv.ParseFloat(lockedPad, 64)
	if math.Abs(pad-840) > 2 || !currentData.GPSPadAuto {
		t.Fatal("pad reference", lockedPad, currentData.GPSPadAuto)
	}
	if currentData.Source != gpsRole {
		t.Fatal("expected GPS fallback after LoRa silence", currentData.Source)
	}
	// At 40 s the true height is 1000 ft. Fallback should be within GPS noise.
	h, _ := strconv.ParseFloat(currentData.Height, 64)
	if math.Abs(h-1000) > 6 {
		t.Fatal("fallback height", currentData.Height)
	}
}

func TestAutoTareSkipsMovingGPSAndAveragesRecentFixes(t *testing.T) {
	resetParser()
	close(csvDone)
	gps := &receiver{portInfo: portInfo{name: "COM5"}}
	lora := &receiver{portInfo: portInfo{name: "COM6"}}
	receivers[gps.name], receivers[lora.name] = gps, lora
	start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	gpsAlts := []float64{900, 900, 900, 830, 832, 834, 836, 838, 840, 842, 844, 846, 848, 850}
	for ms := 0; ms <= 13000; ms += 50 {
		at := start.Add(time.Duration(ms) * time.Millisecond)
		receiveLine(lora, "0", at)
		if ms%1000 == 0 {
			i := ms / 1000
			up := 0
			if i < 3 {
				up = 25 // moving: must not contribute
			}
			receiveLine(gps, gpsLine(at, gpsAlts[i], 0, up), at)
		}
	}
	// Last 10 stationary offsets: 832..850 -> mean 841.
	if currentData.GPSPad != "841.00" {
		t.Fatal("expected mean of the last 10 stationary fixes", currentData.GPSPad)
	}
	if currentData.CanTare {
		t.Fatal("manual tare should be unnecessary while the automatic reference is set")
	}
}

func TestAutoTareNeedsLoRa(t *testing.T) {
	resetParser()
	close(csvDone)
	gps := &receiver{portInfo: portInfo{name: "COM5"}}
	receivers[gps.name] = gps
	start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		receiveLine(gps, gpsLine(at, 840, 0, 0), at)
	}
	if currentData.GPSPad != "" || !currentData.CanTare {
		t.Fatal("GPS-only should leave the manual tare available", currentData.GPSPad, currentData.CanTare)
	}
}
