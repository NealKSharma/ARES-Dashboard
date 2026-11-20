// serial.go finds USB serial ports, opens one reader per port and splits
// the incoming bytes into lines.

package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

type portInfo struct{ name, identity string }

type receiver struct {
	portInfo
	cancel             context.CancelFunc
	done               bool
	retry              time.Time
	failures           int
	role               string
	last, gpsAt, first time.Time
	count              int
	gps                TelemetryData
	raw, climb         string
	history            []heightSample
}

var readerWorkers sync.WaitGroup

var receivers = map[string]*receiver{} // protected by dataMutex, together with currentData

func (r *receiver) deviceKey() string {
	if r.identity != "" {
		return r.identity
	}
	return "port:" + r.name
}

func discoverPorts() ([]portInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	var ports []portInfo
	if err == nil {
		for _, p := range details {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Product), "bluetooth") {
				continue
			}
			if !p.IsUSB && p.VID == "" && !strings.EqualFold(p.Name, portFlag) {
				continue
			}
			identity := ""
			if p.SerialNumber != "" {
				identity = p.VID + "/" + p.PID + "/" + p.SerialNumber
			}
			ports = append(ports, portInfo{p.Name, identity})
		}
	} else {
		names, e := serial.GetPortsList()
		if e != nil {
			return nil, e
		}
		for _, name := range names {
			ports = append(ports, portInfo{name: name})
		}
	}
	if portFlag != "" {
		found := false
		for _, p := range ports {
			if strings.EqualFold(p.name, portFlag) {
				found = true
			}
		}
		if !found {
			ports = append(ports, portInfo{name: portFlag})
		}
	}
	return ports, nil
}

type serialInput interface {
	io.ReadCloser
	SetReadTimeout(time.Duration) error
}

type openInput func(string) (serialInput, error)

func serialReader(ctx context.Context) {
	defer readerWorkers.Wait()
	scan := time.NewTicker(time.Second)
	defer scan.Stop()
	// Device enumeration can block inside a Windows driver. Expire stale data
	// on an independent clock even if that scan is delayed.
	clockDone := make(chan struct{})
	go func() {
		defer close(clockDone)
		clock := time.NewTicker(100 * time.Millisecond)
		defer clock.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-clock.C:
				dataMutex.Lock()
				refreshLocked(time.Now(), "STATUS", false)
				dataMutex.Unlock()
			}
		}
	}()
	defer func() { <-clockDone }()
	open := func(name string) (serialInput, error) { return serial.Open(name, &serial.Mode{BaudRate: 115200}) }
	discover := func() {
		ports, err := discoverPorts()
		if err == nil {
			syncPorts(ctx, ports, open, time.Now())
		}
	}
	discover()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			discover()
		}
	}
}

func syncPorts(ctx context.Context, ports []portInfo, open openInput, now time.Time) {
	dataMutex.Lock()
	defer dataMutex.Unlock()
	present := map[string]bool{}
	for _, p := range ports {
		present[p.name] = true
		old := receivers[p.name]
		failures := 0
		if old != nil && old.identity == p.identity {
			if !old.done || now.Before(old.retry) {
				continue
			}
			failures = old.failures
		}
		if old != nil && old.cancel != nil {
			old.cancel()
		}
		child, cancel := context.WithCancel(ctx)
		r := &receiver{portInfo: p, cancel: cancel, failures: failures}
		receivers[p.name] = r
		readerWorkers.Add(1)
		go func() { defer readerWorkers.Done(); listenPort(child, r, open) }()
	}
	for name, r := range receivers {
		if !present[name] {
			r.cancel()
			delete(receivers, name)
		}
	}
	refreshLocked(now, "STATUS", false)
}

func listenPort(ctx context.Context, r *receiver, open openInput) {
	defer func() {
		dataMutex.Lock()
		defer dataMutex.Unlock()
		if receivers[r.name] != r {
			return
		}
		r.done = true
		r.cancel()
		r.failures = min(r.failures+1, 4)
		r.retry = time.Now().Add(time.Second * time.Duration(1<<(r.failures-1)))
		refreshLocked(time.Now(), "USB_CLOSED", false)
	}()
	port, err := open(r.name)
	if err != nil {
		return
	}
	defer port.Close()
	if port.SetReadTimeout(200*time.Millisecond) != nil {
		return
	}
	// Timeouts let this reader observe cancellation without closing a Windows
	// handle while another goroutine is reading it. Discard oversized records
	// through their delimiter, and never decode an unterminated tail on unplug.
	var pending []byte
	discard := false
	buf := make([]byte, 4096)
	// A quiet ground receiver can start streaming long after USB enumeration.
	// Retry noisy, unrecognized devices without cycling an idle receiver.
	var probeUntil time.Time
	for ctx.Err() == nil {
		if r.role == "" && !probeUntil.IsZero() && time.Now().After(probeUntil) {
			return
		}
		n, err := port.Read(buf)
		if n > 0 && probeUntil.IsZero() {
			probeUntil = time.Now().Add(12 * time.Second)
		}
		chunk := buf[:n]
		for len(chunk) > 0 {
			i := bytes.IndexAny(chunk, "\r\n")
			count := i
			if count < 0 {
				count = len(chunk)
			}
			if len(pending)+count > 65536 {
				discard = true
				pending = nil
			}
			if !discard {
				pending = append(pending, chunk[:count]...)
			}
			if i < 0 {
				break
			}
			if !discard && len(pending) > 0 {
				receiveLine(r, string(pending), time.Now())
			}
			discard = false
			pending = pending[:0]
			chunk = chunk[i+1:]
		}
		if err != nil {
			return
		}
	}
}
