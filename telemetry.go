// telemetry.go decodes Featherweight and LoRa lines, identifies each
// receiver, selects the displayed altitude and maintains the GPS pad reference.

package main

import (
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gpsRole     = "Featherweight"
	loraRole    = "LoRa"
	gpsTTL      = 3 * time.Second
	loraTTL     = 2 * time.Second
	confirmSpan = 150 * time.Millisecond
)

var dashboardSession = time.Now().UTC().Format(time.RFC3339Nano)

// inputRecord is attached only to CSV rows. Display state can retain fields from
// both receivers, while this record describes exactly one arriving serial line.
type inputRecord struct{ source, port, raw, altitude string }

type TelemetryData struct {
	input inputRecord
	FlightView
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

type FlightView struct {
	missionHeader
	SnapshotClockMS int64           `json:"snapshotClockMs,omitempty"`
	SessionID       string          `json:"sessionID"`
	Recording       RecordingStatus `json:"recording"`
	TareLocked      bool            `json:"tareLocked"`
	CanTare         bool            `json:"canTare"`
	EventID         uint64          `json:"eventID"`
	Received        string          `json:"received"`
	GPSPort         string          `json:"gpsPort"`
	LoRaPort        string          `json:"loraPort"`
	GPSLive         bool            `json:"gpsLive"`
	GPSFixFresh     bool            `json:"gpsFixFresh"`
	LoRaLive        bool            `json:"loraLive"`
	GPSPad          string          `json:"gpsPad"`
	GPSPadAuto      bool            `json:"gpsPadAuto"`
	LoRaRaw         string          `json:"loraRaw"`
	Source          string          `json:"source"`
	Height          string          `json:"height"`
	Climb           string          `json:"climb"`
	Reason          string          `json:"reason"`
}

type heightSample struct {
	at    time.Time
	value float64
}

var currentData TelemetryData

var dataMutex sync.RWMutex

var numericLine = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)

func decodeGPS(line string) (TelemetryData, bool) {
	parts := strings.Fields(line)
	if len(parts) < 3 || parts[0] != "@" {
		return TelemetryData{}, false
	}

	packetType := parts[1]

	var next TelemetryData
	if packetType == "GPS_STAT" {
		// GS and FND are not the selected rocket's live tracker GPS.
		if indexOf(parts, "GS") >= 0 || indexOf(parts, "FND") >= 0 || indexOf(parts, "CRC_ERR") >= 0 {
			return TelemetryData{}, false
		}
		next.Time = extractAfter(parts, "Time")
		if next.Time == "" {
			next.Time = extractVal(parts, 6)
		}
		if _, err := time.Parse("15:04:05", next.Time); err != nil {
			return TelemetryData{}, false
		}
		next.Alt = extractAfter(parts, "Alt")
		next.Lat = extractAfter(parts, "lt")
		next.Lon = extractAfter(parts, "ln")
		velocity := indexOf(parts, "Vel")
		if velocity < 0 || velocity+3 >= len(parts) {
			return TelemetryData{}, false
		}
		next.Vel, next.UpVel = parts[velocity+1], parts[velocity+3]
		next.Fix, next.Sats = extractAfter(parts, "Fix"), extractAfter(parts, "#")
		for _, value := range []string{next.Alt, next.Lat, next.Lon, next.Vel, next.UpVel, next.Fix, next.Sats} {
			if !finiteNumber(value) {
				return TelemetryData{}, false
			}
		}
		lat, _ := strconv.ParseFloat(next.Lat, 64)
		lon, _ := strconv.ParseFloat(next.Lon, 64)
		if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			return TelemetryData{}, false
		}
		if next.Fix != "0" && next.Fix != "2" && next.Fix != "3" {
			return TelemetryData{}, false
		}

	} else if packetType == "RX_NOMTK" {
		next.RSSI, next.Volt = extractAfter(parts, "RSSI"), extractAfter(parts, "trk_B_V")
		if !finiteNumber(next.RSSI) || !finiteNumber(next.Volt) {
			return TelemetryData{}, false
		}

	} else {
		return TelemetryData{}, false
	}
	next.Packet = packetType
	return next, true
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

func finiteNumber(s string) bool {
	value, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func fresh(at, now time.Time, ttl time.Duration) bool { return !at.IsZero() && now.Sub(at) < ttl }

func receiveLine(r *receiver, line string, now time.Time) {
	dataMutex.Lock()
	defer dataMutex.Unlock()
	if stopping || receivers[r.name] != r || r.done {
		return
	}
	if debugMode == "all" {
		log.Printf("[%s] %s", r.name, line)
	}
	if r.role != loraRole && (portFlag == "" || strings.EqualFold(portFlag, r.name)) {
		if sample, ok := decodeGPS(line); ok {
			if r.role == "" {
				log.Printf("Featherweight telemetry on %s", r.name)
			}
			input := inputRecord{gpsRole, r.name, line, sample.Alt}
			r.role = gpsRole
			r.last = now
			r.failures = 0
			if sample.Packet == "GPS_STAT" {
				previous, previousAt := r.gps.Time, r.gpsAt
				if previous == "" && trackerClock.identity == r.deviceKey() {
					previous = trackerClock.stamp
					previousAt = trackerClock.at
				}
				if !advancingFix(previous, sample.Time, now.Sub(previousAt)) {
					// Log the actual input, but never plot it or renew measurement freshness.
					// A same-epoch loss of fix immediately invalidates fallback.
					stamp, _ := time.Parse("15:04:05", sample.Time)
					prior, _ := time.Parse("15:04:05", r.gps.Time)
					if stamp.Equal(prior) && sample.Fix != "3" {
						r.gps.Fix = sample.Fix
					}
					refreshInputLocked(now, "GPS_NONADVANCING", true, input)
					return
				}
				sample.RSSI = r.gps.RSSI
				sample.Volt = r.gps.Volt
				r.gps = sample
				r.gpsAt = now
				if only, _ := uniqueRole(gpsRole, now); only == r {
					trackerClock.identity = r.deviceKey()
					trackerClock.stamp = sample.Time
					trackerClock.at = now
					autoTareLocked(r, now)
				}
			} else {
				r.gps.RSSI = sample.RSSI
				r.gps.Volt = sample.Volt
			}
			if debugMode == "gps" {
				log.Printf("[%s] %s", r.name, line)
			}
			refreshInputLocked(now, sample.Packet, true, input)
			return
		}
	}
	if r.role == gpsRole || strings.EqualFold(r.name, portFlag) {
		return
	}
	raw := line
	line = strings.TrimSpace(line)
	if !numericLine.MatchString(line) || !finiteNumber(line) {
		if r.role == "" {
			r.count = 0
		}
		return
	}
	if !fresh(r.last, now, loraTTL) || r.count == 0 {
		r.first = now
		r.count = 0
		r.history = nil
		r.climb = ""
	}
	r.count++
	r.last = now
	input := inputRecord{loraRole, r.name, raw, line}
	if r.count < 3 || now.Sub(r.first) < confirmSpan {
		row := currentData
		row.EventID = 0
		row.Packet = "LORA_REQUALIFY"
		if r.role == "" {
			row.Packet = "NUMERIC_PROBE"
			input.source = "Unclassified numeric"
		}
		row.Received = now.UTC().Format(time.RFC3339Nano)
		row.input = input
		logRowLocked(row)
		return
	}
	if r.role == "" {
		log.Printf("LoRa altitude stream on %s", r.name)
	}
	r.role = loraRole
	r.raw = line
	r.failures = 0
	v, _ := strconv.ParseFloat(line, 64)
	// Fit a short same-source slope. Clustered arrivals have too little timing
	// information; require at least three readings spanning 150 ms.
	for len(r.history) > 0 && now.Sub(r.history[0].at) > time.Second {
		r.history = r.history[1:]
	}
	if len(r.history) == 0 || now.Sub(r.history[len(r.history)-1].at) >= 10*time.Millisecond {
		r.history = append(r.history, heightSample{now, v})
	}
	r.climb = ""
	if len(r.history) >= 3 && now.Sub(r.history[0].at) >= confirmSpan {
		var st, sy, stt, sty float64
		for _, s := range r.history {
			t := s.at.Sub(now).Seconds()
			st += t
			sy += s.value
			stt += t * t
			sty += t * s.value
		}
		n := float64(len(r.history))
		r.climb = number((n*sty - st*sy) / (n*stt - st*st))
	}
	refreshInputLocked(now, "LORA_ALT", true, input)
}

// Tracker time is UTC time-of-day. Use the nearest ordering across midnight;
// a longer outage starts a new comparison epoch on a distinct timestamp.
func advancingFix(previous, current string, elapsed time.Duration) bool {
	if previous == "" {
		return true
	}
	old, _ := time.Parse("15:04:05", previous)
	next, _ := time.Parse("15:04:05", current)
	delta := next.Sub(old)
	if delta == 0 {
		return false
	}
	if elapsed >= 12*time.Hour {
		return true
	}
	if delta < -12*time.Hour {
		delta += 24 * time.Hour
	}
	return delta > 0 && delta <= 12*time.Hour
}

func number(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if !finiteNumber(s) {
		return ""
	}
	return s
}

func uniqueRole(role string, now time.Time) (*receiver, string) {
	var connected, live *receiver
	connectedCount, liveCount := 0, 0
	ttl := gpsTTL
	if role == loraRole {
		ttl = loraTTL
	}
	for _, r := range receivers {
		if r.done || r.role != role {
			continue
		}
		connected = r
		connectedCount++
		if fresh(r.last, now, ttl) {
			live = r
			liveCount++
		}
	}
	if liveCount == 1 {
		return live, live.name
	}
	if connectedCount == 1 {
		return connected, connected.name
	}
	if connectedCount > 1 {
		return nil, "ambiguous"
	}
	return nil, ""
}

func refreshLocked(now time.Time, event string, force bool) {
	refreshInputLocked(now, event, force, inputRecord{})
}

func refreshInputLocked(now time.Time, event string, force bool, input inputRecord) {
	if stopping {
		return
	}
	gps, gp := uniqueRole(gpsRole, now)
	lora, lp := uniqueRole(loraRole, now)
	old := currentData
	var next TelemetryData
	if gps != nil {
		next = gps.gps
	}
	next.GPSPort = gp
	next.LoRaPort = lp
	next.GPSPad = old.GPSPad
	next.GPSPadAuto = old.GPSPadAuto
	if gps != nil && padIdentity != "" && padIdentity != gps.deviceKey() {
		next.GPSPad = ""
		next.GPSPadAuto = false
		padIdentity = ""
	}
	next.SessionID = dashboardSession
	next.Source = "none"
	next.TareLocked = old.TareLocked
	if state := recorder.Load(); state != nil {
		next.Recording = *state
	}

	if gps != nil {
		next.GPSLive = fresh(gps.last, now, gpsTTL)
		next.GPSFixFresh = gps.gps.Fix == "3" && fresh(gps.gpsAt, now, gpsTTL)
		if next.GPSFixFresh {
			vertical, _ := strconv.ParseFloat(next.UpVel, 64)
			horizontal, _ := strconv.ParseFloat(next.Vel, 64)
			next.CanTare = math.Abs(vertical) <= 10 && math.Abs(horizontal) <= 15
			if math.Abs(vertical) > 60 {
				next.TareLocked = true
			}
		}
		if next.GPSFixFresh && next.GPSPad != "" {
			alt, _ := strconv.ParseFloat(next.Alt, 64)
			pad, _ := strconv.ParseFloat(next.GPSPad, 64)
			next.Source = gpsRole
			next.Height = number(alt - pad)
			if math.Abs(alt-pad) > 100 {
				next.TareLocked = true
			}
			next.Climb = next.UpVel
		}
	}
	if lora != nil {
		next.LoRaRaw = lora.raw
		next.LoRaLive = lora.count >= 3 && lora.last.Sub(lora.first) >= confirmSpan && fresh(lora.last, now, loraTTL)
		if next.LoRaLive {
			next.Source = loraRole
			next.Height = lora.raw
			altitude, _ := strconv.ParseFloat(lora.raw, 64)
			if math.Abs(altitude) > 100 {
				next.TareLocked = true
			}
			next.Climb = lora.climb
		}
	}
	next.CanTare = next.CanTare && !next.TareLocked && next.GPSPad == ""
	switch {
	case lp == "ambiguous":
		next.Reason = "Multiple LoRa candidates"
	case next.LoRaLive:
		next.Reason = "Live StratoLogger altitude"
	case lp != "":
		next.Reason = "Waiting for fresh LoRa altitude"
	default:
		next.Reason = "Searching for LoRa"
	}
	if next.Source == "none" {
		switch {
		case gp == "ambiguous":
			next.Reason += " · multiple GPS candidates"
		case next.GPSFixFresh:
			next.Reason += " · tare GPS at pad"
		default:
			next.Reason += " · waiting for 3-D GPS"
		}
	}
	next.missionHeader = mission.observe(next, event, input, now)
	next.EventID = old.EventID
	next.Received = old.Received
	next.Packet = old.Packet
	if !force && next == old {
		return
	}
	// Recorder acknowledgements are display-only. Logging them would create
	// another flush and a self-sustaining stream of CSV acknowledgements.
	old.Recording = next.Recording
	recordingOnly := !force && next == old
	next.EventID++
	next.Packet = event
	next.Received = now.UTC().Format(time.RFC3339Nano)
	currentData = next
	if recordingOnly {
		currentData.Packet = "RECORDER"
		publishDisplayLocked()
		return
	}
	queueLocked(input)
}

func queueLocked(input inputRecord) {
	publishDisplayLocked()
	row := currentData
	row.input = input
	logRowLocked(row)
}

var padIdentity string

// Retain one tracker clock across USB reconnections during this session.
var trackerClock struct {
	identity, stamp string
	at              time.Time
}

// Automatic GPS pad reference: on the pad, while LoRa and 3-D GPS are both
// live, each GPS fix contributes GPS altitude minus the mean LoRa height over
// the last second. The pad reference is the mean of the most recent offsets.
// It stops updating at launch, so a mid-flight LoRa dropout falls back to GPS
// on the same reference.
const autoTareSamples = 10

const autoTareMinimum = 3

var autoTare struct {
	identity string
	offsets  []float64
}

func autoTareLocked(gps *receiver, now time.Time) {
	if mission.LaunchMS != 0 || (mission.Phase != "" && mission.Phase != "PAD") || currentData.TareLocked {
		return
	}
	if autoTare.identity != gps.deviceKey() {
		autoTare.identity, autoTare.offsets = gps.deviceKey(), nil
	}
	lora, port := uniqueRole(loraRole, now)
	if lora == nil || port == "ambiguous" || lora.count < 3 || lora.last.Sub(lora.first) < confirmSpan ||
		!fresh(lora.last, now, loraTTL) || len(lora.history) < 3 || gps.gps.Fix != "3" {
		return
	}
	alt, err1 := strconv.ParseFloat(gps.gps.Alt, 64)
	vertical, err2 := strconv.ParseFloat(gps.gps.UpVel, 64)
	horizontal, err3 := strconv.ParseFloat(gps.gps.Vel, 64)
	if err1 != nil || err2 != nil || err3 != nil || math.Abs(vertical) > 10 || math.Abs(horizontal) > 15 {
		return
	}
	var sum float64
	for _, sample := range lora.history {
		sum += sample.value
	}
	autoTare.offsets = append(autoTare.offsets, alt-sum/float64(len(lora.history)))
	if len(autoTare.offsets) > autoTareSamples {
		autoTare.offsets = autoTare.offsets[len(autoTare.offsets)-autoTareSamples:]
	}
	if len(autoTare.offsets) < autoTareMinimum {
		return
	}
	var total float64
	for _, offset := range autoTare.offsets {
		total += offset
	}
	currentData.GPSPad = number(total / float64(len(autoTare.offsets)))
	currentData.GPSPadAuto = true
	padIdentity = gps.deviceKey()
}

func tareGPS(now time.Time) {
	dataMutex.Lock()
	defer dataMutex.Unlock()
	gps, _ := uniqueRole(gpsRole, now)
	if stopping || gps == nil {
		return
	}
	refreshLocked(now, "STATUS", false)
	if !currentData.CanTare {
		return
	}
	currentData.GPSPad = gps.gps.Alt
	currentData.GPSPadAuto = false
	padIdentity = gps.deviceKey()
	refreshLocked(now, "GPS_TARE", true)
}
