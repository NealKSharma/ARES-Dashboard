// recorder.go keeps the pad pre-roll, writes one CSV per receiver from
// launch until shortly after landing, and reports recording status.

package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var csvChan = make(chan TelemetryData, 2000)

var csvDone = make(chan struct{})

// The recorder keeps a short raw pre-roll on the pad, then records from the
// mission's detected launch until shortly after its detected landing. The
// browser applies the same window to its graphs, so CSV and display agree.
// Holds 5 s even at receiver rates well above the measured ~22 rows/s.
const prelaunchLimit = 2048

const prelaunchDuration = 5 * time.Second

const postLandingDuration = 5 * time.Second

type pendingRow struct {
	at  time.Time
	row TelemetryData
}

func rowTime(row TelemetryData) time.Time {
	at, err := time.Parse(time.RFC3339Nano, row.Received)
	if err != nil {
		return time.Now()
	}
	return at
}

func logTelemetry(dir string) error {
	(RecordingStatus{State: "armed"}).publish()
	var ring [prelaunchLimit]pendingRow
	var start, count int
	for row := range csvChan {
		at := rowTime(row)
		for count > 0 && (count == len(ring) || at.Sub(ring[start].at) > prelaunchDuration) {
			ring[start] = pendingRow{}
			start = (start + 1) % len(ring)
			count--
		}
		ring[(start+count)%len(ring)] = pendingRow{at, row}
		count++
		// The mission tracker sets LaunchMS once on detected ascent. Using it
		// here keeps the CSV start identical to the dashboard's phase change.
		if row.LaunchMS == 0 {
			continue
		}
		initial := make([]TelemetryData, 0, count)
		for i := 0; i < count; i++ {
			initial = append(initial, ring[(start+i)%len(ring)].row)
		}
		clear(ring[:])
		return writeFlight(dir, initial)
	}
	(RecordingStatus{State: "stopped"}).publish()
	return nil
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
	if err := logTelemetry(getLogDir()); err != nil {
		log.Printf("CSV recording stopped: %v", err)
	}
}

type RecordingStatus struct {
	State string `json:"state"`
	GPS   uint64 `json:"gps"`
	LoRa  uint64 `json:"lora"`
}

var recorder atomic.Pointer[RecordingStatus]

func (s RecordingStatus) publish() { recorder.Store(&s) }

// Each flight writes one CSV per receiver, in its own folder under logs.
const (
	gpsLog  = "gps"
	loraLog = "lora"
)

var logFolders = map[string]string{gpsLog: "featherweight_gps", loraLog: "lora"}

var logPrefixes = map[string]string{gpsLog: "ARES_FeatherweightGPS", loraLog: "ARES_LoRa"}

var logHeaders = map[string]string{
	gpsLog:  "ReceivedUTC,EventID,Event,Phase,Port,Record,TrackerTime,Latitude,Longitude,Altitude(ft),Velocity(ft/s),UpVel(ft/s),Satellites,Fix,RSSI,Battery(mV),GPSPad(ft),GPSPadSource,Source,SelectedAlt(ft AGL),SelectedUpVel(ft/s),SelectionReason\n",
	loraLog: "ReceivedUTC,EventID,Event,Phase,Port,Record,Altitude(ft),Source,SelectedAlt(ft AGL),SelectedUpVel(ft/s),SelectionReason\n",
}

// logKind says which receiver a row came from. Status rows with no input
// return "" and go to both files.
func logKind(row TelemetryData) string {
	switch row.input.source {
	case gpsRole:
		return gpsLog
	case loraRole, "Unclassified numeric":
		return loraLog
	}
	return ""
}

func writeFlight(logDir string, initial []TelemetryData) (result error) {
	status := RecordingStatus{State: "starting"}
	status.publish()
	defer func() {
		switch {
		case result != nil:
			status.State = "error"
		case status.State != "complete":
			status.State = "stopped"
		}
		status.publish()
	}()

	stamp := time.Now().Format("2006-01-02_15-04-05.000000000")
	open := func(kind string) (*os.File, error) {
		dir := filepath.Join(logDir, logFolders[kind])
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, fmt.Sprintf("%s_%s.csv", logPrefixes[kind], stamp))
		if abs, err := filepath.Abs(name); err == nil {
			name = abs
		}
		file, err := os.OpenFile(name, os.O_EXCL|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		if _, err := file.WriteString(logHeaders[kind]); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return nil, err
		}
		return file, nil
	}
	gpsFile, err := open(gpsLog)
	if err != nil {
		return err
	}
	defer func() {
		if err := gpsFile.Close(); result == nil {
			result = err
		}
	}()
	loraFile, err := open(loraLog)
	if err != nil {
		return err
	}
	defer func() {
		if err := loraFile.Close(); result == nil {
			result = err
		}
	}()

	return recordBatches(gpsFile, loraFile, stamp, &status, csvChan, initial...)
}

type syncWriter interface {
	io.Writer
	Sync() error
}

func recordBatches(gpsFile, loraFile syncWriter, flight string, status *RecordingStatus, rows <-chan TelemetryData, initial ...TelemetryData) error {
	buffer := initial
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	status.State = "recording"
	status.publish()
	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		var gpsRows, loraRows []TelemetryData
		for _, row := range buffer {
			switch logKind(row) {
			case gpsLog:
				gpsRows = append(gpsRows, row)
			case loraLog:
				loraRows = append(loraRows, row)
			default: // selection and status changes belong to both timelines
				gpsRows = append(gpsRows, row)
				loraRows = append(loraRows, row)
			}
		}
		for _, out := range []struct {
			file syncWriter
			rows []TelemetryData
			kind string
		}{{gpsFile, gpsRows, gpsLog}, {loraFile, loraRows, loraLog}} {
			if len(out.rows) == 0 {
				continue
			}
			if err := writeCSVRows(out.file, out.rows, out.kind); err != nil {
				return err
			}
			if err := out.file.Sync(); err != nil {
				return err
			}
		}
		if debugMode == "all" || debugMode == "gps" {
			log.Printf("Saved %d telemetry points for flight %s", len(buffer), flight)
		}
		for _, row := range buffer {
			switch row.input.source {
			case gpsRole:
				status.GPS++
			case loraRole:
				status.LoRa++
			}
		}
		status.publish()
		clear(buffer)
		buffer = buffer[:0]
		return nil
	}
	// After the mission reports landing, keep postLandingDuration of rows and
	// then close the flight. The timer covers receivers that fall silent.
	var stopAt time.Time
	var stopTimer <-chan time.Time
	var landingTimer *time.Timer
	defer func() {
		if landingTimer != nil {
			landingTimer.Stop()
		}
	}()
	watchLanding := func(row TelemetryData) {
		if row.LandedMS == 0 || !stopAt.IsZero() {
			return
		}
		stopAt = time.UnixMilli(row.LandedMS).Add(postLandingDuration)
		landingTimer = time.NewTimer(time.Until(stopAt))
		stopTimer = landingTimer.C
	}
	// Compare at millisecond precision, the precision of LandedMS.
	afterWindow := func(row TelemetryData) bool {
		return !stopAt.IsZero() && rowTime(row).UnixMilli() > stopAt.UnixMilli()
	}
	for _, row := range buffer {
		watchLanding(row)
	}
	finish := func() error {
		if err := flush(); err != nil {
			return err
		}
		status.State = "complete"
		status.publish()
		return nil
	}
	for {
		select {
		case data, ok := <-rows:
			if !ok {
				return flush()
			}
			watchLanding(data)
			if afterWindow(data) {
				return finish()
			}
			buffer = append(buffer, data)
			if len(buffer) >= 2000 {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case <-stopTimer:
			// Keep rows already queued from inside the window, then stop.
			for {
				select {
				case data, ok := <-rows:
					if ok && !afterWindow(data) {
						buffer = append(buffer, data)
						continue
					}
				default:
				}
				return finish()
			}
		}
	}
}

func logRowLocked(row TelemetryData) {
	// Bounded backpressure preserves samples during short disk stalls. On a disk
	// error the writer reports the failure, exits and releases blocked producers.
	select {
	case <-csvDone:
		return
	default:
	}
	select {
	case csvChan <- row:
	case <-csvDone:
	}
}

// writeCSVRows escapes fields and surfaces buffered write errors.
func writeCSVRows(dst io.Writer, rows []TelemetryData, kind string) error {
	writer := csv.NewWriter(dst)
	for _, d := range rows {
		id := strconv.FormatUint(d.EventID, 10)
		if d.EventID == 0 {
			id = ""
		}
		var record []string
		if kind == gpsLog {
			padSource := ""
			if d.GPSPad != "" {
				padSource = "manual"
				if d.GPSPadAuto {
					padSource = "auto (LoRa)"
				}
			}
			// Each row shows only what that packet measured. Radio rows carry
			// RSSI and battery, fix rows carry position. Status rows and repeated
			// fixes leave measurement columns empty; Record keeps the raw line.
			fix := make([]string, 8)
			radio := make([]string, 2)
			switch {
			case d.input.source == gpsRole && d.Packet == "GPS_STAT":
				fix = []string{d.Time, d.Lat, d.Lon, d.Alt, d.Vel, d.UpVel, d.Sats, d.Fix}
			case d.input.source == gpsRole && d.Packet == "RX_NOMTK":
				radio = []string{d.RSSI, d.Volt}
			}
			record = append([]string{d.Received, id, d.Packet, d.Phase, d.input.port, d.input.raw}, fix...)
			record = append(record, radio...)
			record = append(record, d.GPSPad, padSource, d.Source, d.Height, d.Climb, d.Reason)
		} else {
			record = []string{d.Received, id, d.Packet, d.Phase, d.input.port, d.input.raw, d.input.altitude, d.Source, d.Height, d.Climb, d.Reason}
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
