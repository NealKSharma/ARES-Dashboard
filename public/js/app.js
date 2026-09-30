// ---------- WebSocket ----------
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
			if (data.lat || data.alt || data.rssi) setTrackerConnected();

			if (data.rssi) {
				const rssiVal = parseFloat(data.rssi);
				let pct = 0;
				if (rssiVal >= -50) pct = 100;
				else if (rssiVal > -120) pct = Math.round(((rssiVal + 120) / 70) * 100);
				data.rssi = pct.toString();
			}
			updateUI(data);
			updateMapPosition(data);
			updateCustomGraphs(data);
		} catch (e) {
			console.error('Error parsing telemetry:', e);
		}
	};
}

window.altOffset = 0;
window.lastRawAlt = 0;

function tareAltitude() {
	window.altOffset = window.lastRawAlt;
	// Update UI immediately with 0
	document.getElementById('alt').textContent = "0";
}

function setTrackerConnected() {
  setLink('tracker-status', true, 'Tracker');
  if (trackerTimeout) clearTimeout(trackerTimeout);
  trackerTimeout = setTimeout(setTrackerDisconnected, 3000);
}
function setTrackerDisconnected() { setLink('tracker-status', false, 'Tracker'); }

let lastPacketTime = 0;

function updateUI(data) {
  const set = (id, v) => { document.getElementById(id).textContent = v || '--'; };
  ['lat', 'lon', 'alt', 'vel', 'upvel', 'fix', 'sats', 'volt', 'rssi'].forEach(k => {
    let val = data[k];

    if (k === 'alt' && val) {
      const rawAlt = parseFloat(val);
      if (!isNaN(rawAlt)) {
        window.lastRawAlt = rawAlt;
        val = (rawAlt - window.altOffset).toString();
        data.alt = val; // ensure graphs use the tared value
      }
    }

    if (val && (k === 'lat' || k === 'lon')) {
      const num = parseFloat(val);
      if (!isNaN(num)) val = parseFloat(num.toFixed(5)).toString(); // Max 5 decimal places, strips trailing zeros
    }
    set(k, val);
  });
  
  if (data.time || data.lat || data.rssi) {
    lastPacketTime = performance.now();
  }
}

setInterval(() => {
  if (lastPacketTime > 0) {
    const sec = ((performance.now() - lastPacketTime) / 1000).toFixed(1);
    document.getElementById('time').textContent = `${sec} s ago`;
  }
}, 100);

// ---------- Map ----------
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
  if (isNaN(lat) || isNaN(lon)) return;

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

// ---------- Graphs ----------
const customGraphs = [];
const palette = ['#ff6a3d', '#5ec8e5', '#e9c46a', '#7fd48b', '#c39bd3'];
Chart.defaults.color = '#8291a1';
Chart.defaults.font.family = "'Barlow Semi Condensed', sans-serif";
let timeIndex = 0;

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
  const color = palette[customGraphs.length % palette.length];

  const box = document.createElement('div');
  box.className = 'graph-box';
  
  const closeBtn = document.createElement('button');
  closeBtn.className = 'close-graph';
  closeBtn.innerHTML = '&times;';
  closeBtn.title = 'Remove graph';
  closeBtn.onclick = function() {
    const index = customGraphs.findIndex(g => g.yKey === yKey);
    if (index > -1) customGraphs.splice(index, 1);
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
  timeIndex++;
  const label = data.time || timeIndex.toString();
  customGraphs.forEach(({ chart, yKey }) => {
    const y = parseFloat(data[yKey]);
    if (isNaN(y)) return;
    chart.data.labels.push(label);
    chart.data.datasets[0].data.push(y);
    if (chart.data.labels.length > 10000) {
      chart.data.labels.shift();
      chart.data.datasets[0].data.shift();
    }
    chart.update();
  });
}

function clearAllData() {
  customGraphs.forEach(({ chart }) => {
    chart.data.labels = [];
    chart.data.datasets[0].data = [];
    chart.update();
  });
  rocketPath.length = 0;
  pathLine.setLatLngs(rocketPath);
}

connect();
createGraph('alt', 'Altitude (ft)');
createGraph('upvel', 'Vertical velocity (ft/s)');
createGraph('rssi', 'Signal strength (%)');

// ---------- Mission clock ----------
function updateClock() {
  document.getElementById('mission-clock').textContent = new Date().toLocaleTimeString('en-GB', { hour12: false });
}
setInterval(updateClock, 1000);
updateClock();

// ---------- Rockets ----------
const MAX_ALT = 15000;
const rockets = {
  harmonia: {
    name: 'Harmonia', note: 'Fastest (~1.25 Mach)',
    height: '12.5', structure: 'Carbon fiber',
    alt: 15000, altText: '~15,000', altMetric: '~4.6 km',
    flight: '427', apogee: '~38 s',
    payload: '3U bay, max 15 lb (10×10×30 cm)'
  },
  shelly: {
    name: 'Shelly', note: 'Versatile modular design',
    height: '~4.95', structure: '3D print, laser cut, tube',
    alt: 3520, altText: '~3,520', altMetric: '~1 km',
    flight: '106', apogee: '~14.2 s',
    payload: 'Modular (35cm L × 6.48cm D)'
  },
  scylla: {
    name: 'Scylla', note: '142.77 m/s',
    height: '~4.1', structure: '3D printed',
    alt: 4200, altText: '~4,200', altMetric: '',
    flight: '110', apogee: '~16 s',
    payload: 'Budget-friendly (18cm L × 7cm D)'
  },
  phobos: {
    name: 'Phobos', note: 'Our First Rocket',
    height: '10+', structure: 'Fiber glass',
    alt: 7400, altText: '~7,400', altMetric: '~2.2 km',
    flight: '248', apogee: '~21 s',
    payload: 'EM pass-through (~55cm L × 15cm D)'
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
      <div class="scale"><b style="width:${(r.alt / MAX_ALT * 100).toFixed(0)}%"></b></div>
    </div>
    <div><span class="r-key">Flight time</span><span class="r-val">${v(r.flight)} <small>${r.flight ? 's' : ''}</small></span><span class="r-sub">${r.apogee ? 'Apogee at ' + r.apogee : '&nbsp;'}</span></div>
    <div><span class="r-key">Payload</span><span class="r-text">${v(r.payload)}</span></div>`;
}
selectPlatform('harmonia');