const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${protocol}//${window.location.host}/ws`;
let socket = null;
let trackerTimeout = null;

function setLink(id, up, name) {
  const el = document.getElementById(id);
  el.className = 'link ' + (up ? 'up' : 'down');
  el.innerHTML = `<i></i>${name} ${up ? 'connected' : 'offline'}`;
}

function connect() {
  socket = new WebSocket(wsUrl);
  socket.onopen = () => setLink('server-status', true, 'Server');
  socket.onclose = () => {
    setLink('server-status', false, 'Server');
    setTrackerDisconnected();
    setTimeout(connect, 5000);
  };
	socket.onerror = (err) => console.error('WebSocket error:', err);
	socket.onmessage = (event) => {
		try {
			const data = JSON.parse(event.data);
			if (!data.snapshot && (data.lat || data.alt || data.rssi)) setTrackerConnected();

			if (data.rssi) {
				const rssiVal = parseFloat(data.rssi);
				let pct = 0;
				if (rssiVal >= -50) pct = 100;
				else if (rssiVal > -120) pct = Math.round(((rssiVal + 120) / 70) * 100);
				data.rssi = pct.toString();
			}
			try { updateUI(data); } catch(e) { console.error("UI error:", e); }
			try { updateMapPosition(data); } catch(e) { console.error("Map error:", e); }
			try { 
				if (!data.snapshot && (data.packet === 'RX_NOMTK' || data.time !== window.lastGraphTime)) {
					updateCustomGraphs(data); 
					if (data.packet !== 'RX_NOMTK') window.lastGraphTime = data.time;
				}
			} catch(e) { console.error("Graph error:", e); }
		} catch (e) {
			console.error('Error parsing telemetry:', e);
		}
	};
}

window.altOffset = 0;
window.lastRawAlt = null;

function tareAltitude() {
	if (flightPhase !== "PAD" && flightPhase !== "LANDED") return;
	if (!Number.isFinite(window.lastRawAlt)) return;
	window.altOffset = window.lastRawAlt;
	document.getElementById('alt').textContent = "0";
	maxAlt = 0;
	document.getElementById('max-alt').textContent = "0";
}

function setTrackerConnected() {
  setLink('tracker-status', true, 'Tracker');
  if (trackerTimeout) clearTimeout(trackerTimeout);
  trackerTimeout = setTimeout(setTrackerDisconnected, 3000);
}
function setTrackerDisconnected() {
  setLink('tracker-status', false, 'Tracker');
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
  if (freshGPS && Number.isFinite(taredAlt) && Number.isFinite(upvel)) {
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
    data: { labels: [], datasets: [{ label: yLabel, data: [], borderColor: color, borderWidth: 1.5, tension: 0.15, pointRadius: 0 }] },
    options: {
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
  customGraphs.push({ chart, yKey });
  updateGraphDropdown();
}

function updateCustomGraphs(data) {
  if (data.snapshot) return;
  timeIndex++;
  const label = data.time || timeIndex.toString();
  customGraphs.forEach(({ chart, yKey }) => {
    if (data.packet === 'RX_NOMTK' && yKey !== 'rssi' && yKey !== 'volt') return;
    if (['alt', 'vel', 'upvel'].includes(yKey) && !hasFlightFix(data)) return;
    const y = parseFloat(data[yKey]);
    if (!Number.isFinite(y)) return;
    chart.data.labels.push(label);
    chart.data.datasets[0].data.push(y);
    if (chart.data.labels.length > 10000) {
      chart.data.labels.shift();
      chart.data.datasets[0].data.shift();
    }
    chart.update();
  });
}

function clearAllData(override = false) {
  if (!override && flightPhase !== "PAD" && flightPhase !== "LANDED") return;
  customGraphs.forEach(({ chart }) => {
    chart.data.labels = [];
    chart.data.datasets[0].data = [];
    chart.update();
  });
  rocketPath.length = 0;
  pathLine.setLatLngs(rocketPath);
  
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
  window.lastGraphTime = undefined;
  document.getElementById('gforce').textContent = '--';
  if (rocketMarker) { rocketMarker.remove(); rocketMarker = null; }
  flightPhase = "PAD";
  ignitionTime = null;
  window.landedTime = null;
  padRawAlt = 0;
  timeIndex = 0;
  document.getElementById('flight-phase').textContent = "PAD";
  document.getElementById('flight-phase').classList.remove('active-phase');
}

connect();
createGraph('alt', 'Altitude (ft)');
createGraph('upvel', 'Vertical velocity (ft/s)');
createGraph('rssi', 'Signal strength (%)');

function updateClock() {
  if (flightPhase === "PAD" || !ignitionTime) {
    document.getElementById('mission-clock').textContent = new Date().toLocaleTimeString('en-GB', { hour12: false });
  } else {
    let elapsed = Math.floor((performance.now() - ignitionTime) / 1000);
    if (flightPhase === "LANDED" && window.landedTime) {
      elapsed = Math.floor((window.landedTime - ignitionTime) / 1000);
    }
    const m = String(Math.floor(elapsed / 60)).padStart(2, '0');
    const s = String(elapsed % 60).padStart(2, '0');
    document.getElementById('mission-clock').textContent = `T+ ${m}:${s}`;
  }
}
setInterval(updateClock, 250);
updateClock();

let padRawAlt = 0;

function checkFlightPhase(taredAlt, upvel, g, horizontalVelocity) {
  if (flightPhase === "PAD") {
    if ((g !== null && g > 3.0) || upvel > 60) {
      const baselineUp = lastUpVel, baselineTime = lastTrackerTime;
      clearAllData();
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
      
      const badge = document.getElementById('flight-phase');
      badge.textContent = flightPhase;
      badge.classList.add('active-phase');
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
      document.getElementById('flight-phase').classList.remove('active-phase');
    } else if (upvel < -40) flightPhase = "DROGUE";
    else if (upvel > -40 && upvel < -5 && (maxAlt - taredAlt) > 300) flightPhase = "MAIN";
  } else if (flightPhase === "DROGUE") {
    if (upvel > -35 && upvel < -5) flightPhase = "MAIN";
    else if (Math.abs(upvel) < 5 && Math.abs(window.lastRawAlt - padRawAlt) < 500) {
      flightPhase = "LANDED";
      window.landedTime = performance.now();
      document.getElementById('flight-phase').classList.remove('active-phase');
    }
  } else if (flightPhase === "MAIN") {
    if (Math.abs(upvel) < 5 && Math.abs(window.lastRawAlt - padRawAlt) < 500) {
      flightPhase = "LANDED";
      window.landedTime = performance.now();
      document.getElementById('flight-phase').classList.remove('active-phase');
    }
  }
  document.getElementById('flight-phase').textContent = flightPhase;
}

const MAX_ALT = 15000;
const rockets = {
  harmonia: {
    name: 'Harmonia', note: 'Designed for supersonic flight',
    height: '~15', structure: 'Carbon fiber',
    payload: '3U payload bay'
  },
  shelly: {
    name: 'Shelly', note: 'In development · PDR complete',
    structure: '3D printed and fiberglass',
    payload: 'Planned replacement for Scylla'
  },
  scylla: {
    name: 'Scylla', note: '2.5 in diameter',
    height: '~5', structure: 'Fully 3D printed',
    payload: 'Small payloads, including CLUE boards and micro:bits'
  },
  phobos: {
    name: 'Phobos', note: 'Our first rocket',
    height: '~10', structure: 'Fiberglass',
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
      <span class="r-key">Max altitude</span>
      <span class="r-val">${v(r.altText)} <small>${r.altText ? 'ft' : ''}</small></span>
      <span class="r-sub">${r.altMetric || "&nbsp;"}</span>
      <div class="scale"><b style="width:${Number.isFinite(r.alt) ? (r.alt / MAX_ALT * 100).toFixed(0) : 0}%"></b></div>
    </div>
    <div><span class="r-key">Flight time</span><span class="r-val">${v(r.flight)} <small>${r.flight ? 's' : ''}</small></span><span class="r-sub">${r.apogee ? 'Apogee at ' + r.apogee : '&nbsp;'}</span></div>
    <div><span class="r-key">Payload</span><span class="r-text">${v(r.payload)}</span></div>`;
}
selectPlatform('harmonia');