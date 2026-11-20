// flight.go tracks the flight phase (PAD to LANDED), launch and landing times
// and the ascent detector.

package main

import (
	"math"
	"strconv"
	"time"
)

// The header describes observations at the ground station. Only fresh measurement
// events advance it. Its timestamps and last phase survive browser reconnects.
type missionHeader struct {
	Phase    string `json:"phase"`
	LaunchMS int64  `json:"launchMs,omitempty"`
	LandedMS int64  `json:"landedMs,omitempty"`
}

type missionTracker struct {
	missionHeader
	ascent                  ascentDetector
	source, gpsPadPort      string
	last, falling, apogee   time.Time
	peak, peakClimb, gpsPad float64
	settle                  []heightSample
}

// Landing: the altitude stays within landingSpread for landingHold, inside
// landingBand of the pad, with no gap or source change from the active receiver.
const (
	landingBand   = 30.0
	landingSpread = 20.0
	landingHold   = 5 * time.Second
)

// settled keeps the most recent run of readings that fits inside landingSpread
// and reports whether that run has lasted landingHold.
func (m *missionTracker) settled(now time.Time, height float64) bool {
	if math.Abs(height) > landingBand {
		m.settle = m.settle[:0]
		return false
	}
	m.settle = append(m.settle, heightSample{now, height})
	for len(m.settle) > 1 {
		low, high := m.settle[0].value, m.settle[0].value
		for _, p := range m.settle[1:] {
			low, high = math.Min(low, p.value), math.Max(high, p.value)
		}
		if high-low <= landingSpread && len(m.settle) <= 4096 {
			break
		}
		m.settle = m.settle[1:]
	}
	return now.Sub(m.settle[0].at) >= landingHold
}

var mission missionTracker // protected by dataMutex

func (m *missionTracker) observe(d TelemetryData, event string, in inputRecord, now time.Time) missionHeader {
	if m.Phase == "" {
		m.Phase = "PAD"
	}
	if m.Phase == "LANDED" {
		return m.missionHeader
	}

	gps := event == "GPS_STAT" && d.GPSFixFresh && in.port == d.GPSPort
	lora := event == "LORA_ALT" && d.LoRaLive && in.port == d.LoRaPort
	h, speed := d.Height, d.Climb
	if gps {
		raw, _ := strconv.ParseFloat(d.Alt, 64)
		v, _ := strconv.ParseFloat(d.UpVel, 64)
		if m.Phase == "PAD" && m.gpsPadPort != in.port && math.Abs(v) < 5 {
			m.gpsPadPort, m.gpsPad = in.port, raw
		}
		if d.LoRaLive {
			// Supplement the altitude-only labels only when the independent
			// vertical velocities agree on motion. A stationary bench GPS is common.
			climb, err := strconv.ParseFloat(d.Climb, 64)
			if err == nil && climb > 0 && v > 0 {
				if m.Phase == "ASCENT" && v > 60 {
					m.Phase = "BOOST"
				}
				if m.Phase == "BOOST" && climb < m.peakClimb-5 && v < m.peakClimb-5 {
					m.Phase = "COAST"
				}
			}
			if m.Phase == "DESCENT" && climb < -40 && v < -40 {
				m.Phase = "DROGUE"
			}
			if m.Phase == "DROGUE" && climb > -35 && climb < -5 && v > -35 && v < -5 {
				m.Phase = "MAIN"
			}
			return m.missionHeader
		}
		if d.GPSPad == "" {
			h = d.Alt
			if in.port == m.gpsPadPort {
				h = number(raw - m.gpsPad)
			}
		}
		speed = d.UpVel
	} else if !lora {
		return m.missionHeader
	}
	height, err := strconv.ParseFloat(h, 64)
	if err != nil {
		return m.missionHeader
	}
	climb, speedErr := strconv.ParseFloat(speed, 64)
	if speedErr != nil {
		climb = math.NaN()
	}
	key := in.source + "/" + in.port
	if m.source != key || now.Sub(m.last) > 2*time.Second || now.Before(m.last) {
		m.source, m.peak, m.peakClimb = key, height, 0
		m.falling = time.Time{}
		m.settle = nil
		m.ascent = ascentDetector{}
	}
	m.last = now
	m.peak = math.Max(m.peak, height)
	if !math.IsNaN(climb) {
		m.peakClimb = math.Max(m.peakClimb, climb)
	}
	switch m.Phase {
	case "PAD":
		if m.ascent.rising(key, now, height) || (gps && climb > 60) {
			m.Phase = "ASCENT"
			if gps {
				m.Phase = "BOOST"
			}
			m.LaunchMS = now.UnixMilli()
		}
	case "ASCENT", "BOOST", "COAST":
		if gps && m.Phase == "BOOST" && climb > 0 && climb < m.peakClimb-5 {
			m.Phase = "COAST"
		}
		if height < m.peak-5 && climb < -3 {
			if m.falling.IsZero() {
				m.falling = now
			}
			if now.Sub(m.falling) >= 400*time.Millisecond {
				m.Phase = "APOGEE"
				m.apogee = now
			}
		} else {
			m.falling = time.Time{}
		}
	case "APOGEE", "DESCENT", "DROGUE", "MAIN":
		if m.Phase == "APOGEE" && now.Sub(m.apogee) < time.Second {
			break
		}
		if m.Phase == "APOGEE" {
			m.Phase = "DESCENT"
		}
		if gps {
			if m.Phase == "DESCENT" && climb < -40 {
				m.Phase = "DROGUE"
			}
			if (m.Phase == "DROGUE" || m.Phase == "DESCENT" && m.peak-height > 300) && climb > -35 && climb < -5 {
				m.Phase = "MAIN"
			}
		}
		padKnown := lora || d.GPSPad != "" || m.gpsPadPort == in.port
		if padKnown && m.settled(now, height) {
			m.Phase = "LANDED"
			m.LandedMS = now.UnixMilli()
		}
	}
	return m.missionHeader
}

type ascentDetector struct {
	port    string
	points  []heightSample
	above   int
	crossed heightSample
}

// Compare readings from one physical source only. Repeated values above a
// threshold after one abrupt step cannot confirm continuing ascent.
func (d *ascentDetector) rising(port string, at time.Time, h float64) bool {
	if d.port != port || len(d.points) > 0 && (at.Before(d.points[len(d.points)-1].at) || at.Sub(d.points[len(d.points)-1].at) > 2*time.Second) {
		*d = ascentDetector{port: port}
	}
	for len(d.points) > 0 && (at.Sub(d.points[0].at) > 2500*time.Millisecond || len(d.points) >= 256) {
		d.points = d.points[1:]
	}
	// A single low outlier must not establish the reference for an ascent.
	low, reference := h, h
	for _, p := range d.points {
		if p.value <= low {
			reference = low
			low = p.value
		} else if p.value < reference {
			reference = p.value
		}
	}
	d.points = append(d.points, heightSample{at, h})
	if h-reference < 20 {
		d.above = 0
		return false
	}
	if d.above == 0 {
		d.crossed = heightSample{at, h}
	}
	d.above++
	return d.above >= 3 && at.Sub(d.crossed.at) >= 150*time.Millisecond && h-d.crossed.value >= 5
}
