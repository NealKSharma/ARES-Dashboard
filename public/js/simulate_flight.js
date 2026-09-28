/*
  FLIGHT SIMULATION SCRIPT
  Use this script to test the dashboard if the Featherweight GPS is not connected.
  
  Instructions:
  1. Open the dashboard in your browser.
  2. Open Developer Tools (F12 or Ctrl+Shift+I) and navigate to the Console tab.
  3. Copy the entire contents of this file and paste it into the console, then press Enter.
*/

if (window.fakeFlight) clearInterval(window.fakeFlight);

let t = 0;
let phase = "PAD";
let alt = 1000;
let upvel = 0;
let vel = 0;
let lat = 42.0266;
let lon = -93.6465;

window.fakeFlight = setInterval(() => {
  if (t < 5) {
    phase = "PAD";
  } else if (t < 15) { 
    if (phase !== "BOOST") console.log("LAUNCH: Motor ignition");
    phase = "BOOST";
    upvel += 120;
    vel += 20;
  } else if (upvel > 0) {
    if (phase !== "COAST") console.log("COAST: Motor burnout");
    phase = "COAST";
    upvel -= 32;
    vel *= 0.98;
  } else if (alt > 3000) {
    if (phase !== "DROGUE") console.log("DEPLOYMENT: Drogue parachute deployed");
    phase = "DROGUE";
    upvel = -150;
    vel = 30;
  } else if (alt > 1000) {
    if (phase !== "MAIN") console.log("DEPLOYMENT: Main parachute deployed");
    phase = "MAIN";
    upvel = -20;
    vel = 15;
  } else {
    if (phase !== "LANDED") console.log("LANDED: Touchdown confirmed");
    phase = "LANDED";
    upvel = 0;
    vel = 0;
    alt = 1000;
  }

  if (phase !== "PAD" && phase !== "LANDED") {
    alt += upvel;
    lat += (vel * 0.0000005); 
    lon += (vel * 0.0000005);
  }

  const rawRssi = phase === "LANDED" ? -(110 + Math.floor(Math.random() * 10)) : -(50 + Math.floor(Math.random() * 20));
  let rssiPct = 0;
  if (rawRssi >= -50) rssiPct = 100;
  else if (rawRssi > -120) rssiPct = Math.round(((rawRssi + 120) / 70) * 100);
  const sats = phase === "PAD" ? 7 : 11;
  const volt = 4200 - (t * 2);

  const data = {
    time: new Date().toLocaleTimeString('en-GB', { hour12: false }),
    alt: Math.round(alt).toString(),
    lat: lat.toString(),
    lon: lon.toString(),
    vel: Math.round(vel).toString(),
    upvel: Math.round(upvel).toString(),
    sats: sats.toString(),
    fix: "3",
    rssi: rssiPct.toString(),
    volt: Math.max(3500, volt).toString()
  };

  if (typeof setTrackerConnected === 'function') setTrackerConnected();
  if (typeof updateUI === 'function') updateUI(data);
  if (typeof updateMapPosition === 'function') updateMapPosition(data);
  if (typeof updateCustomGraphs === 'function') updateCustomGraphs(data);
  
  t++;
}, 1000);

console.log("Telemetry simulation active");
