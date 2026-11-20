const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${protocol}//${window.location.host}/ws`;
let socket = null;
let dualTelemetry = false;
let lastEventID = -1;
let selectedSource = '';
let sessionID = '';
let seenLoRa = false;
let flightLocked = false;
let serverMission = false;
let reconnectGap = false;
let selectedPeak = -Infinity;
let climbPeak = -Infinity;
let shownAltPeak = '--', shownClimbPeak = '--';

function setLink(id, up, name) {
  const el = document.getElementById(id);
  el.className = 'link ' + (up ? 'up' : 'down');
  el.innerHTML = `<i></i>${name} ${up ? 'connected' : 'offline'}`;
}

function connect() {
  lastEventID = -1;
  socket = new WebSocket(wsUrl);
  socket.onopen = () => setLink('server-status', true, 'Server');
  socket.onclose = () => {
    setLink('server-status', false, 'Server');
    setTrackerDisconnected();
    setLink('lora-status', false, 'LoRa');
    document.getElementById('source-detail').textContent = 'Connection lost. Keep Ares Dashboard.exe running. Retrying in 5 seconds.';
    document.getElementById('tareBtn').disabled = true;
    if (dualTelemetry) {
      reconnectGap = true;
      showRecording(null);
      for (const id of ['alt','upvel','vel','alt-source']) document.getElementById(id).textContent = '--';
    }
    setTimeout(connect, 5000);
  };
	socket.onerror = (err) => console.error('WebSocket error:', err);
	socket.onmessage = (event) => {
		try {
			const data = JSON.parse(event.data);
			if (data.rssi) {
				const rssiVal = parseFloat(data.rssi);
				let pct = 0;
				if (rssiVal >= -50) pct = 100;
				else if (rssiVal > -120) pct = Math.round(((rssiVal + 120) / 70) * 100);
				data.rssi = pct.toString();
			}
			// Every server message carries an event ID, including snapshots.
			handleReceiverEvent(data);
		} catch (e) {
			console.error('Error parsing telemetry:', e);
		}
	};
}

window.altOffset = 0;
window.lastRawAlt = null;

function showRecording(recording) {
  const state = recording?.state || 'unknown';
  const el = document.getElementById('recording-state');
  const labels = {armed:'CSV armed · waiting for ascent', recording:'CSV recording', error:'CSV write error', stopped:'CSV stopped', complete:'CSV saved · flight complete',
    starting:'Opening CSV', simulated:'Simulation · CSV off', unknown:'CSV status unknown'};
  el.textContent = labels[state] || labels.unknown;
  el.dataset.state = state;
  document.getElementById('recording-counts').textContent = recording && state !== 'simulated'
    ? `Saved · GPS ${recording.gps} · LoRa ${recording.lora}` : '';
}

// Hold each displayed mean until the next half-second of samples arrives.
const displayMeans = new Map();
function displayMean(key, value, time, isSample, reset) {
  if (reset) displayMeans.delete(key);
  let state = displayMeans.get(key);
  if (!isSample) return {value:state?.value ?? value, ready:false};
  if (!Number.isFinite(value)) {displayMeans.delete(key); return {value:NaN,ready:false};}
  if (!state || time < state.time || time-state.time > 3000) {
    displayMeans.set(key,{time,value,sum:0,count:0});
    return {value,ready:true};
  }
  state.sum += value; state.count++;
  if (time-state.time < 500) return {value:state.value,ready:false};
  state.value = state.sum/state.count; state.sum=0; state.count=0; state.time=time;
  return {value:state.value,ready:true};
}

function altitudeMessage(data) {
  if (data.recording?.state === 'simulated') return 'Simulated telemetry. No flight data is being recorded.';
  const gpsSource = data.source === 'Featherweight';
  const active = data.source === 'LoRa' ? `Using LoRa altitude on ${data.loraPort}.`
    : gpsSource ? `Using GPS altitude on ${data.gpsPort}.` : 'No live altitude.';
  if (data.loraPort === 'ambiguous') return active + ' Multiple USB ports are streaming numbers.';
  if (gpsSource) return active + (data.loraPort ? ' LoRa readings have stopped.' : ' Waiting for a LoRa stream.');
  if (data.gpsPort === 'ambiguous') return active + ' Multiple Featherweight receivers detected.';
  if (!data.gpsPort && !data.loraPort) return 'No receiver data yet. Connect a ground receiver and check that it is streaming.';
  if (!data.gpsPort) return active + (data.loraLive ? ' GPS backup is offline.' : ' LoRa is silent and GPS is offline.');
  if (!data.gpsFixFresh) return active + ' GPS backup needs a fresh 3-D fix.';
  if (data.gpsPad) return active + (data.gpsPadAuto ? ' GPS backup is ready, matched to LoRa.' : ' GPS backup is ready.');
  if (data.canTare) return active + ' At the launch pad, select “Tare GPS at pad” to enable GPS backup.';
  return active + (data.tareLocked ? ' GPS backup was not tared before flight.' : ' GPS tare requires a stationary receiver at the pad.');
}

function handleReceiverEvent(data) {
  if (data.snapshot && data.sessionID && sessionID && sessionID !== data.sessionID) {
    clearAllData(true);
    selectedSource = ''; seenLoRa = false; reconnectGap = false; lastEventID = -1;
  }
  if (data.sessionID) sessionID = data.sessionID;
  if (data.eventID <= lastEventID) return;
  lastEventID = data.eventID;
  dualTelemetry = true;
  flightLocked = Boolean(data.tareLocked);
  serverMission = Boolean(data.phase);
  if (serverMission) {
    flightPhase = data.phase;
    const serverNow = data.snapshotClockMs || Date.parse(data.received);
    const localNow = performance.now();
    ignitionTime = data.launchMs ? localNow - (serverNow - data.launchMs) : null;
    window.landedTime = data.landedMs ? localNow - (serverNow - data.landedMs) : null;
    renderMissionHeader();
  }
  showRecording(data.recording);
  const previousSource = selectedSource;
  selectedSource = data.source || 'none';
  const liveGPSPacket = data.packet === 'GPS_STAT' && data.gpsPort && data.gpsPort !== 'ambiguous';
  const gpsEvent = liveGPSPacket || data.packet === 'RX_NOMTK';
  const measurement = !data.snapshot && ((liveGPSPacket && selectedSource === 'Featherweight')
      || (data.packet === 'LORA_ALT' && selectedSource === 'LoRa'));
  const gpsTare = data.packet === 'GPS_TARE' && selectedSource === 'Featherweight';
  const resumed = reconnectGap && !data.snapshot;
  if (resumed) reconnectGap = false;
  const changed = resumed || gpsTare || (previousSource !== '' && previousSource !== selectedSource);
  let transition = 'Altitude unavailable';
  if (resumed) transition = 'Feed resumed';
  else if (gpsTare) transition = 'GPS pad tare';
  else if (selectedSource === 'Featherweight') transition = previousSource === 'LoRa' ? 'GPS fallback' : 'GPS altitude';
  else if (selectedSource === 'LoRa') transition = seenLoRa ? 'LoRa recovered' : 'LoRa primary';
  if (selectedSource === 'LoRa') seenLoRa = true;
  const label = selectedSource === 'LoRa' ? 'StratoLogger via LoRa'
      : selectedSource === 'Featherweight' ? 'Featherweight GPS' : 'Unavailable';
  const text = (id, value) => { document.getElementById(id).textContent = value; };

  setLink('tracker-status', data.gpsLive, 'Featherweight');
  setLink('lora-status', data.loraLive, 'LoRa');
  document.getElementById('tracker-status').title = data.gpsPort || 'Searching for valid Featherweight packets';
  if (data.loraPort && !data.loraLive) {
    document.getElementById('lora-status').textContent = data.loraPort === 'ambiguous' ? 'LoRa ambiguous' : 'LoRa awaiting data';
  }
  document.getElementById('lora-status').title = data.loraPort || 'Searching for a numeric altitude stream';
  text('alt-source', label);
  text('source-detail', altitudeMessage(data));

  // Flight phase, horizontal speed, and GPS-derived G keep their GPS-only inputs.
  if (gpsEvent || data.snapshot) {
    window.altOffset = Number(data.gpsPad || 0);
    updateUI({...data});
    if (liveGPSPacket || data.snapshot) updateMapPosition(data);
  }
  if (data.packet === 'GPS_NONADVANCING' && data.fix) text('fix', data.fix);
  if (!data.gpsFixFresh) {
    currentG = null; lastUpVel = null; lastTrackerTime = null;
    text('gforce', '--'); text('vel', '--');
  }
  if (!data.snapshot && gpsTare) {
    selectedPeak = -Infinity; climbPeak = -Infinity;
  }
  const alt = Number.parseFloat(data.height), climb = Number.parseFloat(data.climb);
  if (!data.snapshot && (measurement || changed) && Number.isFinite(alt)) selectedPeak = Math.max(selectedPeak, alt);
  if (!data.snapshot && (measurement || changed) && Number.isFinite(climb)) climbPeak = Math.max(climbPeak, climb);
  let stamp = Date.parse(data.received);
  if (!Number.isFinite(stamp)) stamp = performance.now();
  const plotted = {...data};
  const ready = {};
  for (const [key, raw, sample] of [
    ['alt',alt,measurement], ['upvel',climb,measurement],
    ['vel',parseFloat(data.vel),liveGPSPacket && data.gpsFixFresh],
    ['rssi',parseFloat(data.rssi),data.packet === 'RX_NOMTK'],
    ['volt',parseFloat(data.volt),data.packet === 'RX_NOMTK']
  ]) {
    const vertical = key === 'alt' || key === 'upvel';
    const reset = data.snapshot || resumed || (vertical && changed) || (key === 'vel' && !data.gpsFixFresh);
    const mean = displayMean(key,raw,stamp,!data.snapshot && (sample || (vertical && changed)),reset);
    plotted[key] = mean.value; ready[key] = mean.ready;
    const valid = Number.isFinite(mean.value) && (key !== 'vel' || data.gpsFixFresh);
    text(key,valid ? String(Number(mean.value.toFixed(key==='upvel'?1:0))) : '--');
  }
  if (ready.alt || changed || data.snapshot) shownAltPeak = Number.isFinite(selectedPeak) ? String(selectedPeak) : '--';
  text('max-alt', shownAltPeak);
  if (ready.upvel || changed || data.snapshot) shownClimbPeak = Number.isFinite(climbPeak) ? String(climbPeak) : '--';
  text('max-upvel', shownClimbPeak);
  const tare = document.getElementById('tareBtn');
  tare.disabled = !data.canTare || flightLocked || !['PAD','LANDED'].includes(flightPhase);
  tare.title = flightLocked ? 'GPS pad reference locked for this flight' : data.gpsPadAuto ? 'GPS pad reference is matched to LoRa automatically' : data.gpsPad ? 'Pad reference saved for this session' : 'Requires fresh, stationary 3-D GPS at the pad';
  document.getElementById('clearDataBtn').disabled = (flightLocked && flightPhase !== 'LANDED') || !['PAD','LANDED'].includes(flightPhase);
  if (data.snapshot) return;
  if (measurement || gpsEvent) lastPacketTime = performance.now();
  routeGraphPoint(data, plotted, {measurement, changed, label, transition, ready}, stamp);
}

function tareAltitude() {
	if (flightPhase !== "PAD" && flightPhase !== "LANDED") return;
	if (dualTelemetry) {
      if (!document.getElementById('tareBtn').disabled && socket?.readyState === WebSocket.OPEN) socket.send('tare-gps');
      return;
    }
    if (!Number.isFinite(window.lastRawAlt)) return;
	window.altOffset = window.lastRawAlt;
	document.getElementById('alt').textContent = "0";
	maxAlt = 0;
	document.getElementById('max-alt').textContent = "0";
}

function setTrackerDisconnected() {
  setLink('tracker-status', false, dualTelemetry ? 'Featherweight' : 'Tracker');
  currentG = null; lastUpVel = null; lastTrackerTime = null;
  document.getElementById('gforce').textContent = '--';
}

let lastPacketTime = 0;
let lastUpVel = null;
let lastTrackerTime = null;

let maxAlt = -Infinity;
let maxVel = 0;
let maxUpVel = -Infinity;
let maxG = -Infinity;
let currentG = null;
let flightPhase = "PAD";
let ignitionTime = null;

function parseTrackerTime(tStr) {
  if (typeof tStr !== 'string' || !/^\d{2}:\d{2}:\d{2}(?:\.\d+)?$/.test(tStr)) return null;
  const [h, m, sec] = tStr.split(':').map(Number);
  if (h >= 24 || m >= 60 || sec >= 60) return null;
  return h * 3600 + m * 60 + sec;
}

function hasFlightFix(data) {
  // Legacy simulator samples omit fix; actual tracker packets must have a 3-D fix.
  return Number(data.fix) === 3 || (!data.packet && (data.fix == null || data.fix === ''));
}

function updateUI(data) {
  const liveGPS = !data.snapshot && data.packet !== 'RX_NOMTK' && hasFlightFix(data);
  const set = (id, v) => { 
    const el = document.getElementById(id);
    if (el) {
      if (v === undefined || v === null || v === '') {
        el.textContent = '--';
      } else {
        el.textContent = v;
      }
    }
  };
  
  ['lat', 'lon', 'alt', 'vel', 'upvel', 'fix', 'sats', 'volt', 'rssi'].forEach(k => {
    let val = data[k];
    if (['alt', 'vel', 'upvel'].includes(k) && !hasFlightFix(data)) {
      set(k, null);
      if (k === 'alt') window.lastRawAlt = null;
      return;
    }

    if (k === 'alt' && val) {
      const rawAlt = parseFloat(val);
      if (!isNaN(rawAlt)) {
        window.lastRawAlt = rawAlt;
        val = parseFloat((rawAlt - window.altOffset).toFixed(2)).toString();
        data.alt = val;
        
        const taredAlt = parseFloat(val);
        if (liveGPS && taredAlt > maxAlt) {
          maxAlt = taredAlt;
          set('max-alt', maxAlt.toString());
        }
      }
    }

    if (k === 'vel' && val) {
      const v = parseFloat(val);
      if (liveGPS && Number.isFinite(v) && v > maxVel) {
        maxVel = v;
        set('max-vel', maxVel.toString());
      }
    }
    
    if (k === 'upvel' && val) {
      const uv = parseFloat(val);
      if (liveGPS && Number.isFinite(uv) && uv > maxUpVel) {
        maxUpVel = uv;
        set('max-upvel', maxUpVel.toString());
      }
    }

    if (val && (k === 'lat' || k === 'lon')) {
      const num = parseFloat(val);
      if (!isNaN(num)) val = parseFloat(num.toFixed(5)).toString();
    }
    set(k, val);
  });
  
  if (!hasFlightFix(data)) {
    currentG = null; lastUpVel = null; lastTrackerTime = null;
    set('gforce', null);
  }
  let freshGPS = false;
  if (liveGPS && data.upvel !== '' && data.upvel != null) {
    const trackerTime = parseTrackerTime(data.time);
    const up = Number(data.upvel);
    if (trackerTime !== null && Number.isFinite(up)) {
      let dt = lastTrackerTime === null ? null : trackerTime - lastTrackerTime;
      if (dt !== null && dt < -43200) dt += 86400;
      if (dt === null || dt > 0) {
        freshGPS = true;
        currentG = null;
        if (dt !== null && dt < 2 && lastUpVel !== null) {
          currentG = ((up - lastUpVel) / dt / 32.174) + 1;
          set('gforce', (currentG > 0 ? '+' : '') + currentG.toFixed(2));
          if (currentG > maxG) {
            maxG = currentG;
            set('max-g', (maxG > 0 ? '+' : '') + maxG.toFixed(2));
          }
        } else set('gforce', '--');
        lastUpVel = up;
        lastTrackerTime = trackerTime;
      }
    }
  }

  if ((data.time || data.lat || data.rssi) && !data.snapshot) {
    lastPacketTime = performance.now();
  }

  const taredAlt = parseFloat(data.alt);
  const upvel = parseFloat(data.upvel);
  if (!serverMission && freshGPS && Number.isFinite(taredAlt) && Number.isFinite(upvel)) {
    checkFlightPhase(taredAlt, upvel, currentG, Number(data.vel));
  }
}

setInterval(() => {
  if (lastPacketTime > 0) {
    const sec = ((performance.now() - lastPacketTime) / 1000).toFixed(1);
    document.getElementById('time').textContent = `${sec} s ago`;
  }
}, 100);

const START = [42.0266, -93.6465];
const map = L.map('map', { zoomControl: false }).setView(START, 16);
L.control.zoom({ position: 'bottomright' }).addTo(map);
L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
  attribution: '&copy; OpenStreetMap contributors'
}).addTo(map);

const rocketIcon = L.divIcon({
  html: '<div class="pulse-ring"></div><div class="pulse-dot"></div>',
  className: '', iconSize: [32, 32], iconAnchor: [16, 16]
});
const rocketPath = [];
const pathLine = L.polyline(rocketPath, { color: '#ff6a3d', weight: 3 }).addTo(map);
let rocketMarker = null;

function updateMapPosition(data) {
  const lat = parseFloat(data.lat);
  const lon = parseFloat(data.lon);
  if (data.packet === 'RX_NOMTK' && !data.snapshot) return;
  if (data.fix != null && data.fix !== '' && Number(data.fix) < 2) return;
  if (!Number.isFinite(lat) || !Number.isFinite(lon) || Math.abs(lat) > 90 || Math.abs(lon) > 180) return;

  const last = rocketPath[rocketPath.length - 1];
  if (last && last[0] === lat && last[1] === lon) return;

  rocketPath.push([lat, lon]);
  if (rocketPath.length > 5000) rocketPath.shift();
  pathLine.setLatLngs(rocketPath);

  if (!rocketMarker) {
    rocketMarker = L.marker([lat, lon], { icon: rocketIcon }).addTo(map);
    map.setView([lat, lon], 16);
  } else {
    rocketMarker.setLatLng([lat, lon]);
    if (!map.getBounds().contains([lat, lon])) map.panTo([lat, lon], { animate: false });
  }
}

const customGraphs = [];
const palette = ['#ff6a3d', '#5ec8e5', '#e9c46a', '#7fd48b', '#c39bd3'];
Chart.defaults.color = '#8291a1';
Chart.defaults.font.family = "'Barlow Semi Condensed', sans-serif";
let timeIndex = 0;
let graphCounter = 0;

function updateGraphDropdown() {
  const sel = document.getElementById('ySelect');
  const btn = document.getElementById('addGraphBtn');
  
  Array.from(sel.options).forEach(opt => {
    const exists = customGraphs.some(g => g.yKey === opt.value);
    if (exists) {
      if (!opt.text.startsWith('✓')) opt.text = '✓ ' + opt.text;
      opt.disabled = true;
    } else {
      if (opt.text.startsWith('✓ ')) opt.text = opt.text.substring(2);
      opt.disabled = false;
    }
  });

  if (sel.options[sel.selectedIndex].disabled) {
    const firstAvail = Array.from(sel.options).find(o => !o.disabled);
    if (firstAvail) sel.value = firstAvail.value;
  }
  
  if (sel.options[sel.selectedIndex] && sel.options[sel.selectedIndex].disabled) {
    btn.disabled = true;
    btn.textContent = 'Added';
    btn.style.opacity = '0.5';
    btn.style.cursor = 'not-allowed';
  } else {
    btn.disabled = false;
    btn.textContent = 'Add graph';
    btn.style.opacity = '1';
    btn.style.cursor = 'pointer';
  }
}

// Category-axis indices move when old points are trimmed. Store markers in
// absolute sample order and translate only the visible ones during drawing.
const sourceMarkerPlugin = {
  id: 'sourceChanges',
  afterDatasetsDraw(chart) {
    const {ctx, chartArea: area} = chart;
    if (!area || !chart.sourceMarkers?.length) return;
    ctx.save();
    ctx.beginPath(); ctx.rect(area.left, area.top, area.right-area.left, area.bottom-area.top); ctx.clip();
    ctx.font = '11px system-ui';
    // Labels that would overlap drop to a second row instead of disappearing.
    const occupied = [Infinity, Infinity];
    for (let i = chart.sourceMarkers.length - 1; i >= 0; i--) {
      const marker = chart.sourceMarkers[i];
      const x = chart.scales.x.getPixelForValue(marker.index - chart.firstSample);
      ctx.strokeStyle = '#e9c46a'; ctx.setLineDash([3,3]);
      ctx.beginPath(); ctx.moveTo(x, area.top); ctx.lineTo(x, area.bottom); ctx.stroke();
      const width = ctx.measureText(marker.text).width + 10;
      const left = Math.max(area.left, Math.min(x+3, area.right-width));
      const row = occupied.findIndex(edge => left + width <= edge);
      if (row < 0) continue;
      const top = area.top + row * 19;
      ctx.fillStyle = '#27241c'; ctx.fillRect(left, top, width, 17);
      ctx.fillStyle = '#ffe196'; ctx.fillText(marker.text, left+5, top+12);
      occupied[row] = left-4;
    }
    ctx.restore();
  }
};

function createGraph(key, label) {
  const sel = document.getElementById('ySelect');
  const yKey = key || sel.value;
  
  if (customGraphs.some(g => g.yKey === yKey)) return;

  const yLabel = label || sel.options[sel.selectedIndex].text.replace('✓ ', '');
  const color = palette[graphCounter % palette.length];
  graphCounter++;

  const box = document.createElement('div');
  box.className = 'graph-box';
  
  const closeBtn = document.createElement('button');
  closeBtn.className = 'close-graph';
  closeBtn.innerHTML = '&times;';
  closeBtn.title = 'Remove graph';
  closeBtn.onclick = function() {
    const index = customGraphs.findIndex(g => g.yKey === yKey);
    if (index > -1) customGraphs.splice(index, 1);
    chart.destroy();
    box.remove();
    updateGraphDropdown();
  };
  box.appendChild(closeBtn);

  const canvas = document.createElement('canvas');
  box.appendChild(canvas);
  document.getElementById('customGraphs').appendChild(box);

  const chart = new Chart(canvas.getContext('2d'), {
    type: 'line',
    plugins: ['alt', 'upvel'].includes(yKey) ? [sourceMarkerPlugin] : [],
    data: { labels: [], datasets: [{ label: yLabel, data: [], borderColor: color, borderWidth: 3, tension: 0.15, pointRadius: 0, pointHoverRadius: 5 }] },
    options: {
      interaction: { mode: 'index', axis: 'x', intersect: false },
      animation: false,
      responsive: true,
      maintainAspectRatio: false,
      scales: {
        x: { ticks: { maxTicksLimit: 5, maxRotation: 0 }, grid: { color: 'rgba(130,145,161,0.12)' } },
        y: { grid: { color: 'rgba(130,145,161,0.12)' } }
      },
      plugins: { legend: { align: 'start', labels: { boxWidth: 10, boxHeight: 2 } } }
    }
  });
  chart.sourceMarkers = []; chart.firstSample = 0;
  customGraphs.push({ chart, yKey });
  updateGraphDropdown();
}

// Graph window for a live mission. On the pad the graphs run live and keep a
// 5 s bank. At detected launch they clear and restart from that bank, so the
// trace starts just before liftoff with no gap. Plotting stops 5 s after
// detected landing. The backend applies the same window to the CSV.
const GRAPH_PREROLL_MS = 5000, GRAPH_POSTLAND_MS = 5000;
let graphWindow = 'pad';
let padBank = [];

function resetGraphs() {
  customGraphs.forEach(({ chart }) => {
    chart.sourceMarkers = []; chart.firstSample = 0;
    chart.data.labels = [];
    chart.data.datasets[0].data = [];
    chart.update();
  });
  timeIndex = 0;
}

function routeGraphPoint(data, plotted, selection, stamp) {
  if (!serverMission) { updateCustomGraphs(plotted, selection); return; }
  if (!data.launchMs) {
    graphWindow = 'pad';
    padBank.push({plotted, selection, stamp});
    while (padBank.length && stamp - padBank[0].stamp > GRAPH_PREROLL_MS) padBank.shift();
    updateCustomGraphs(plotted, selection);
    return;
  }
  if (data.landedMs && stamp > data.landedMs + GRAPH_POSTLAND_MS) {
    graphWindow = 'done';
    padBank = [];
    return;
  }
  if (graphWindow !== 'flight') {
    graphWindow = 'flight';
    resetGraphs();
    for (const point of padBank) {
      if (point.stamp >= data.launchMs - GRAPH_PREROLL_MS) updateCustomGraphs(point.plotted, point.selection);
    }
    padBank = [];
  }
  updateCustomGraphs(plotted, selection);
}

function updateCustomGraphs(data, selection) {
  if (data.snapshot) return;
  timeIndex++;
  const label = selection ? (data.received || '').replace(/^.*T/, '').replace(/(\.\d{3})\d*Z$/, '$1') : data.time || timeIndex.toString();
  customGraphs.forEach(({ chart, yKey }) => {
    let y = parseFloat(data[yKey]);
    if (selection) {
      if (selection.ready && !selection.ready[yKey] && !(selection.changed && ['alt','upvel'].includes(yKey))) return;
      if (yKey === 'alt' || yKey === 'upvel') {
        if (!selection.measurement && !selection.changed) return;
        if (selection.changed) {
          chart.sourceMarkers.push({index:chart.firstSample + chart.data.labels.length, text:selection.transition});
          chart.data.labels.push(label + ' · ' + selection.transition);
          chart.data.datasets[0].data.push(null);
        }
        if (!Number.isFinite(y)) y = null;
      } else if (yKey === 'vel') {
        if (data.packet !== 'GPS_STAT' || !data.gpsFixFresh) return;
      } else if (data.packet !== 'RX_NOMTK') return;
    } else {
      if (data.packet === 'RX_NOMTK' && yKey !== 'rssi' && yKey !== 'volt') return;
      if (['alt', 'vel', 'upvel'].includes(yKey) && !hasFlightFix(data)) return;
    }
    if (y !== null && !Number.isFinite(y)) return;
    chart.data.labels.push(label);
    chart.data.datasets[0].data.push(y);
    while (chart.data.labels.length > 10000) {
      chart.data.labels.shift();
      chart.data.datasets[0].data.shift();
      chart.firstSample++;
      while (chart.sourceMarkers.length && chart.sourceMarkers[0].index < chart.firstSample) chart.sourceMarkers.shift();
    }
    chart.update();
  });
}

function clearAllData(override = false) {
  if (!override && flightLocked && flightPhase !== 'LANDED') return;
  if (!override && flightPhase !== "PAD" && flightPhase !== "LANDED") return;
  displayMeans.clear();
  shownAltPeak = '--'; shownClimbPeak = '--';
  resetGraphs();
  rocketPath.length = 0;
  pathLine.setLatLngs(rocketPath);
  
  selectedPeak = -Infinity; climbPeak = -Infinity;
  maxAlt = -Infinity;
  maxVel = 0;
  maxUpVel = -Infinity;
  maxG = -Infinity;
  document.getElementById('max-alt').textContent = '--';
  document.getElementById('max-vel').textContent = '--';
  document.getElementById('max-upvel').textContent = '--';
  document.getElementById('max-g').textContent = '--';
  
  currentG = null;
  lastUpVel = null;
  lastTrackerTime = null;
  document.getElementById('gforce').textContent = '--';
  if (rocketMarker) { rocketMarker.remove(); rocketMarker = null; }
  if (override) { graphWindow = 'pad'; padBank = []; }
  if (override || !serverMission) {
    flightPhase = "PAD";
    ignitionTime = null;
    window.landedTime = null;
  }
  padRawAlt = 0;
  timeIndex = 0;
  renderMissionHeader();
}

const simulationMode = /(?:^|[?&])simulate=1(?:&|$)/.test(location.search || '');
if (simulationMode) {
  const script = document.createElement('script');
  script.src = 'js/simulate_flight.js'; document.head.appendChild(script);
} else connect();
createGraph('alt', 'Altitude (ft)');
createGraph('upvel', 'Vertical velocity (ft/s)');

function renderMissionHeader() {
  const badge = document.getElementById('flight-phase');
  badge.textContent = flightPhase;
  badge.dataset.phase = flightPhase;
  updateClock();
}

function updateClock() {
  if (flightPhase === "PAD" || ignitionTime === null) {
    document.getElementById('mission-clock').textContent = new Date().toTimeString().slice(0, 8);
  } else {
    let elapsed = Math.floor((performance.now() - ignitionTime) / 1000);
    if (flightPhase === "LANDED" && window.landedTime != null) {
      elapsed = Math.floor((window.landedTime - ignitionTime) / 1000);
    }
    elapsed = Math.max(0, elapsed);
    const m = String(Math.floor(elapsed / 60)).padStart(2, '0');
    const s = String(elapsed % 60).padStart(2, '0');
    document.getElementById('mission-clock').textContent = `T+${m}:${s}`;
  }
}
setInterval(updateClock, 250);
updateClock();

let padRawAlt = 0;

function checkFlightPhase(taredAlt, upvel, g, horizontalVelocity) {
  if (flightPhase === "PAD") {
    if ((g !== null && g > 3.0) || upvel > 60) {
      const baselineUp = lastUpVel, baselineTime = lastTrackerTime;
      if (!dualTelemetry) clearAllData();
      lastUpVel = baselineUp; lastTrackerTime = baselineTime; currentG = g;
      maxAlt = taredAlt; maxUpVel = upvel;
      if (Number.isFinite(horizontalVelocity)) {
        maxVel = horizontalVelocity;
        document.getElementById('max-vel').textContent = String(maxVel);
      }
      document.getElementById('max-alt').textContent = String(maxAlt);
      document.getElementById('max-upvel').textContent = String(maxUpVel);
      if (g !== null) {
        maxG = g;
        document.getElementById('gforce').textContent = g.toFixed(2);
        document.getElementById('max-g').textContent = g.toFixed(2);
      }

      padRawAlt = window.lastRawAlt;
      
      flightPhase = "BOOST";
      ignitionTime = performance.now();
      

    }
  } else if (flightPhase === "BOOST") {
    // Descent remains observable even when sparse samples cannot estimate G.
    if (upvel < 0) flightPhase = "APOGEE";
    else if (g !== null && g < 1.0 && upvel > 0) flightPhase = "COAST";
  } else if (flightPhase === "COAST") {
    if (upvel < 0) flightPhase = "APOGEE";
  } else if (flightPhase === "APOGEE") {
    if (Math.abs(upvel) < 5 && Math.abs(window.lastRawAlt - padRawAlt) < 500) {
      flightPhase = "LANDED";
      window.landedTime = performance.now();
    } else if (upvel < -40) flightPhase = "DROGUE";
    else if (upvel > -40 && upvel < -5 && (maxAlt - taredAlt) > 300) flightPhase = "MAIN";
  } else if (flightPhase === "DROGUE") {
    if (upvel > -35 && upvel < -5) flightPhase = "MAIN";
    else if (Math.abs(upvel) < 5 && Math.abs(window.lastRawAlt - padRawAlt) < 500) {
      flightPhase = "LANDED";
      window.landedTime = performance.now();
    }
  } else if (flightPhase === "MAIN") {
    if (Math.abs(upvel) < 5 && Math.abs(window.lastRawAlt - padRawAlt) < 500) {
      flightPhase = "LANDED";
      window.landedTime = performance.now();
    }
  }
  renderMissionHeader();
}

const MAX_ALT = 15000;
const rockets = {
  harmonia: {
    name: 'Harmonia', note: 'Designed for supersonic flight',
    height: '~15', structure: 'Carbon fiber',
    alt: 15000, altText: '~15,000', altMetric: '~4.6 km',
    flight: '427', apogee: '~38 s',
    payload: '3U payload bay'
  },
  shelly: {
    name: 'Shelly', note: 'In development · PDR complete',
    structure: '3D printed and fiberglass',
    alt: 3520, altText: '~3,520', altMetric: '~1 km',
    flight: '106', apogee: '~14.2 s',
    payload: 'Planned replacement for Scylla'
  },
  scylla: {
    name: 'Scylla', note: '2.5 in diameter',
    height: '~5', structure: 'Fully 3D printed',
    alt: 4200, altText: '~4,200', altMetric: '~1.28 km',
    flight: '110', apogee: '~16 s',
    payload: 'Small payloads, including CLUE boards and micro:bits'
  },
  phobos: {
    name: 'Phobos', note: 'Our first rocket',
    height: '~10', structure: 'Fiberglass',
    alt: 7400, altText: '~7,400', altMetric: '~2.26 km',
    flight: '248', apogee: '~21 s',
    payload: 'Larger payloads'
  }
};

const list = document.getElementById('rocket-list');
Object.entries(rockets).forEach(([id, r]) => {
  const b = document.createElement('button');
  b.textContent = r.name;
  b.dataset.id = id;
  b.onclick = () => selectPlatform(id);
  list.appendChild(b);
});

function selectPlatform(id) {
  const r = rockets[id];
  const v = (x) => x || '--';
  list.querySelectorAll('button').forEach(b => b.classList.toggle('active', b.dataset.id === id));
  document.getElementById('platform-content').innerHTML = `
    <div><span class="r-name">${r.name}</span><span class="r-note">${r.note || "&nbsp;"}</span></div>
    <div><span class="r-key">Height</span><span class="r-val">${v(r.height)} <small>${r.height ? 'ft' : ''}</small></span></div>
    <div><span class="r-key">Structure</span><span class="r-val" style="font-size:18px">${v(r.structure)}</span></div>
    <div>
      <span class="r-key">Max altitude (est.)</span>
      <span class="r-val">${v(r.altText)} <small>${r.altText ? 'ft' : ''}</small></span>
      <span class="r-sub">${r.altMetric || "&nbsp;"}</span>
      <div class="scale"><b style="width:${Number.isFinite(r.alt) ? (r.alt / MAX_ALT * 100).toFixed(0) : 0}%"></b></div>
    </div>
    <div><span class="r-key">Flight time (est.)</span><span class="r-val">${v(r.flight)} <small>${r.flight ? 's' : ''}</small></span><span class="r-sub">${r.apogee ? 'Apogee at ' + r.apogee : '&nbsp;'}</span></div>
    <div><span class="r-key">Payload</span><span class="r-text">${v(r.payload)}</span></div>`;
}
selectPlatform('harmonia');