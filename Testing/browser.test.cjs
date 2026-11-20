const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(process.env.ARES_APP || path.join(__dirname, '..', 'public', 'js', 'app.js'), 'utf8');
function dashboard() {
  const elements = new Map();
  function element() { return {textContent:'', innerHTML:'', style:{}, dataset:{}, children:[], classList:{add(){},remove(){},toggle(){}}, appendChild(e){this.children.push(e)}, querySelectorAll(){return this.children}, remove(){}, getContext(){return {}}}; }
  const document = {getElementById(id){if(!elements.has(id)) elements.set(id,element());return elements.get(id)},createElement:element};
  const select = document.getElementById('ySelect');
  select.options = ['alt','vel','upvel','volt','rssi'].map(value=>({value,text:value}));
  select.selectedIndex = 0;
  Object.defineProperty(select,'value',{get(){return this.options[this.selectedIndex].value},set(v){this.selectedIndex=this.options.findIndex(o=>o.value===v)}});
  class Chart {static defaults={font:{}}; constructor(ctx,config){this.data=config.data;this.options=config.options;this.destroyed=false} update(){} destroy(){this.destroyed=true} }
  function layer(){return {addTo(){return this},setView(){return this},setLatLngs(){},setLatLng(){},panTo(){},getBounds(){return {contains(){return true}}},remove(){}}}
  const context = vm.createContext({document,Chart,L:{map:layer,control:{zoom:layer},tileLayer:layer,divIcon(){},polyline:layer,marker:layer},console,performance:{now:()=>1000},Date,setTimeout(){return 1},clearTimeout(){},setInterval(){},WebSocket:class {}});
  context.window=context;context.location={protocol:'http:',host:'localhost:8080'};
  vm.runInContext(source,context);
  return {run:code=>vm.runInContext(code,context),document};
}
test('midnight rollover advances acceleration baseline on every sample',()=>{
 const d=dashboard(); d.run(`updateUI({time:'23:59:59',upvel:'0'});updateUI({time:'00:00:00',upvel:'10'});updateUI({time:'00:00:01',upvel:'30'});`);
 assert.equal(d.run('lastTrackerTime'),1); assert.ok(Math.abs(d.run('currentG')-(1+20/32.174))<1e-9);
});
test('clear removes stale acceleration state',()=>{
 const d=dashboard();d.run(`currentG=8;lastUpVel=200;lastTrackerTime=20;clearAllData();`);
 assert.equal(d.run('currentG'),null);assert.equal(d.run('lastUpVel'),null);assert.equal(d.run('lastTrackerTime'),null);
});
test('snapshot never advances flight phase',()=>{
 const d=dashboard(); d.run(`currentG=4;updateUI({snapshot:true,alt:'1000',upvel:'100',time:'12:00:00'});`);assert.equal(d.run('flightPhase'),'PAD');
});
test('radio-only messages never advance flight phase using retained GPS',()=>{
 const d=dashboard(); d.run(`currentG=4;updateUI({packet:'RX_NOMTK',alt:'1000',upvel:'100',time:'12:00:00',rssi:'90'});`);assert.equal(d.run('flightPhase'),'PAD');
});
test('time parser rejects malformed and out-of-range times',()=>{
 const d=dashboard();for(const s of ['xx:00:00','24:00:00','12:60:00','12:00:60','12:00:01garbage'])assert.equal(d.run(`parseTrackerTime(${JSON.stringify(s)})`),null);
});
test('removing a graph destroys its Chart instance',()=>{
 const d=dashboard(); const chart=d.run('customGraphs[0].chart');d.document.getElementById('customGraphs').children[0].children[0].onclick();assert.equal(chart.destroyed,true);
});
test('map rejects invalid coordinates and no-fix GPS',()=>{
 const d=dashboard(); d.run(`updateMapPosition({lat:'95',lon:'-93',fix:'3'});updateMapPosition({lat:'42',lon:'-93',fix:'0'});`); assert.equal(d.run('rocketPath.length'),0);
});
test('launch can be detected from vertical velocity without acceleration',()=>{
 const d=dashboard();d.run(`updateUI({alt:'1000',upvel:'100',time:'12:00:00'});`);assert.equal(d.run('flightPhase'),'BOOST');
});
test('long telemetry gap clears stale acceleration',()=>{
 const d=dashboard();d.run(`currentG=5;lastUpVel=20;lastTrackerTime=100;updateUI({time:'00:02:00',upvel:'20'});`);assert.equal(d.run('currentG'),null);
});
test('synthetic flight progresses from pad through landing',()=>{
 const d=dashboard();
 const samples=[[1000,0],[1100,100],[1400,200],[1550,150],[1600,-10],[1400,-100],[1100,-20],[1000,0]];
 const phases=[];
 samples.forEach(([alt,upvel],i)=>{d.run(`updateUI({time:'12:00:0${i}',alt:'${alt}',upvel:'${upvel}',fix:'3'});`);phases.push(d.run('flightPhase'))});
 assert.deepEqual(phases,['PAD','BOOST','BOOST','COAST','APOGEE','DROGUE','MAIN','LANDED']);
});
test('radio update only adds radio graph points',()=>{
 const d=dashboard();d.run(`createGraph('rssi','Signal strength (%)');updateCustomGraphs({packet:'RX_NOMTK',time:'12:00:00',alt:'1000',upvel:'10',rssi:'80'});`);
 assert.equal(d.run('customGraphs[0].chart.data.labels.length'),0);
 assert.equal(d.run('customGraphs[2].chart.data.labels.length'),1);
});
test('duplicate GPS timestamps do not advance flight phase twice',()=>{
 const d=dashboard();d.run(`updateUI({time:'12:00:00',alt:'1000',upvel:'0'});updateUI({time:'12:00:00',alt:'1000',upvel:'200'});`);assert.equal(d.run('flightPhase'),'PAD');
});
test('disconnect clears acceleration and next valid sample can reacquire',()=>{
 const d=dashboard();d.run(`lastTrackerTime=500;lastUpVel=200;currentG=4;setTrackerDisconnected();updateUI({time:'00:00:01',alt:'1000',upvel:'0'});`);
 assert.equal(d.run('lastTrackerTime'),1);assert.equal(d.run('currentG'),null);
});
test('2-D GPS fix does not drive altitude flight state',()=>{
 const d=dashboard();d.run(`updateUI({time:'12:00:00',alt:'1000',upvel:'200',fix:'2'});`);assert.equal(d.run('flightPhase'),'PAD');
});

test('invalid and 2-D fixes cannot contaminate flight readings, peaks, graphs or tare',()=>{
 for(const fix of ['0','2']) {
  const d=dashboard();
  d.run(`updateUI({packet:'GPS_STAT',time:'12:00:00',alt:'1000',vel:'10',upvel:'0',fix:'3'});`);
  d.run(`const bad={packet:'GPS_STAT',time:'12:00:01',alt:'90000',vel:'999',upvel:'999',fix:'${fix}'};updateUI(bad);updateCustomGraphs(bad);tareAltitude();`);
  assert.equal(d.run('maxAlt'),1000);assert.equal(d.run('maxVel'),10);
  assert.equal(d.document.getElementById('alt').textContent,'--');
  assert.equal(d.run('customGraphs[0].chart.data.labels.length'),0);
  assert.equal(d.run('window.altOffset'),0);
 }
});
test('launch preserves the current horizontal-speed peak',()=>{
 const d=dashboard();d.run(`updateUI({packet:'GPS_STAT',time:'12:00:00',alt:'1100',vel:'300',upvel:'100',fix:'3'});`);
 assert.equal(d.run('flightPhase'),'BOOST');assert.equal(d.run('maxVel'),300);
 assert.equal(d.document.getElementById('max-vel').textContent,'300');
});
test('sparse flight can skip unobserved coast and recover through descent to landing',()=>{
 const d=dashboard();const phases=[];
 for(const [time,alt,upvel] of [['00','1000','100'],['03','1500','-10'],['06','1000','0']]){
  d.run(`updateUI({packet:'GPS_STAT',time:'12:00:${time}',alt:'${alt}',upvel:'${upvel}',fix:'3'});`);
  phases.push(d.run('flightPhase'));
 }
 assert.deepEqual(phases,['BOOST','APOGEE','LANDED']);
});
test('radio-tagged reconnect snapshot restores marker without live radio path points',()=>{
 const d=dashboard();d.run(`updateMapPosition({snapshot:true,packet:'RX_NOMTK',lat:'42',lon:'-93',fix:'3'});`);
 assert.equal(d.run('rocketPath.length'),1);assert.notEqual(d.run('rocketMarker'),null);
 d.run(`updateMapPosition({packet:'RX_NOMTK',lat:'43',lon:'-93',fix:'3'});`);
 assert.equal(d.run('rocketPath.length'),1);
});
test('snapshots and radio updates do not recreate cleared peaks',()=>{
 const d=dashboard();
 d.run(`updateUI({snapshot:true,packet:'GPS_STAT',alt:'1000',vel:'300',upvel:'100',fix:'3'});updateUI({packet:'RX_NOMTK',alt:'1000',vel:'300',upvel:'100',fix:'3'});`);
 assert.equal(d.run('maxAlt'),-Infinity);assert.equal(d.run('maxVel'),0);
});
test('no-fix recovery restarts acceleration baseline',()=>{
 const d=dashboard();d.run(`updateUI({time:'12:00:00',upvel:'0',fix:'3'});updateUI({time:'12:00:01',upvel:'999',fix:'0'});updateUI({time:'12:00:02',upvel:'20',fix:'3'});`);
 assert.equal(d.run('currentG'),null);
});
test('platform cards use corrected specifications and do not render NaN',()=>{
 const d=dashboard();assert.equal(d.run('rockets.harmonia.height'),'~15');assert.equal(d.run('rockets.scylla.height'),'~5');
 for(const id of ['harmonia','shelly','scylla','phobos']) {
  d.run(`selectPlatform('${id}')`);
  assert.ok(!d.document.getElementById('platform-content').innerHTML.includes('NaN'));
 }
 assert.match(d.run('rockets.shelly.structure'),/fiberglass/);
});
test('platform tabs display their catalog altitude, flight time and time to apogee', () => {
  const d = dashboard();
  const profiles = [
    ['harmonia', '~15,000', '~4.6 km', '427', '~38 s', 100],
    ['shelly', '~3,520', '~1 km', '106', '~14.2 s', 23],
    ['scylla', '~4,200', '~1.28 km', '110', '~16 s', 28],
    ['phobos', '~7,400', '~2.26 km', '248', '~21 s', 49]
  ];
  for (const [id, altitude, metric, duration, apogee, percent] of profiles) {
    d.run(`selectPlatform('${id}')`);
    const card = d.document.getElementById('platform-content').innerHTML;
    for (const value of ['Max altitude (est.)', 'Flight time (est.)', `${altitude} <small>ft</small>`,
      metric, `${duration} <small>s</small>`, `Apogee at ${apogee}`, `width:${percent}%`]) {
      assert.ok(card.includes(value), `${id} is missing ${value}`);
    }
  }
});

test('live LoRa displays and graphs each reading with GPS offline, including zero',()=>{
 const d=dashboard();
 const base={gpsPort:'',loraPort:'COM2',gpsLive:false,loraLive:true,gpsFixFresh:false,packet:'LORA_ALT',source:'LoRa',height:'0',climb:'',received:'now'};
 for(const i of [1,2]) d.run('handleReceiverEvent('+JSON.stringify({...base,eventID:i,received:new Date(i*1000).toISOString()})+')');
 assert.equal(d.document.getElementById('alt').textContent,'0');
 assert.equal(d.run('customGraphs[0].chart.data.datasets[0].data.length'),2);
 assert.equal(d.run('rocketPath.length'),0);
 assert.equal(d.run('flightPhase'),'PAD');
 d.run('handleReceiverEvent('+JSON.stringify({...base,eventID:1,height:'999'})+')');
 assert.equal(d.document.getElementById('alt').textContent,'0');
});
test('fallback shows pad-relative GPS and marks the altitude chart discontinuity',()=>{
 const d=dashboard();
 const base={gpsPort:'COM1',loraPort:'COM2',gpsLive:true,gpsFixFresh:true,gpsPad:'800',alt:'810',upvel:'0',vel:'2',fix:'3',lat:'42',lon:'-93',time:'12:00:00',received:'now'};
 for(const e of [{eventID:1,packet:'LORA_ALT',loraLive:true,source:'LoRa',height:'8',climb:'1'},
 {eventID:2,packet:'STATUS',loraLive:false,source:'Featherweight',height:'10',climb:'0'}]){
  d.run('handleReceiverEvent('+JSON.stringify({...base,...e})+')');
 }
 assert.equal(d.document.getElementById('alt').textContent,'10');
 assert.equal(d.document.getElementById('alt-source').textContent,'Featherweight GPS');
 assert.deepEqual(Array.from(d.run('customGraphs[0].chart.data.datasets[0].data')),[8,null,10]);
 assert.equal(d.run('rocketPath.length'),0);
});
test('dual snapshot restores map and altitude without adding a sample or advancing flight',()=>{
 const d=dashboard();
 d.run("handleReceiverEvent({eventID:9,snapshot:true,packet:'LORA_ALT',gpsPort:'A',loraPort:'B',loraLive:true,gpsLive:true,gpsFixFresh:true,fix:'3',lat:'42',lon:'-93',alt:'810',upvel:'200',height:'20',source:'LoRa'})");
 assert.equal(d.document.getElementById('alt').textContent,'20');
 assert.equal(d.document.getElementById('max-alt').textContent,'--');
 assert.equal(d.run('customGraphs[0].chart.data.labels.length'),0);
 assert.equal(d.run('rocketPath.length'),1);
 assert.equal(d.run('flightPhase'),'PAD');
});
test('late GPS launch preserves existing LoRa peak, path and altitude samples',()=>{
 const d=dashboard();
 const base={loraLive:true,recording:{state:'recording',gps:20,lora:30},source:'LoRa',loraPort:'B',packet:'LORA_ALT',height:'1500',received:'now'};
 const send=e=>d.run('handleReceiverEvent('+JSON.stringify({...base,...e})+')');
 send({eventID:1,height:'1800',received:'2026-11-19T12:00:00Z'});send({eventID:2,received:'2026-11-19T12:00:01Z'});
 send({eventID:3,packet:'GPS_STAT',gpsPort:'A',gpsLive:true,gpsFixFresh:true,fix:'3',upvel:'180',vel:'90',time:'12:00:00',alt:'2340',lat:'42',lon:'-93',tareLocked:true});
 assert.equal(d.run('flightPhase'),'BOOST');
 assert.equal(d.document.getElementById('max-alt').textContent,'1800');
 assert.equal(d.run('customGraphs[0].chart.data.labels.length'),2);
 assert.equal(d.run('rocketPath.length'),1);
 assert.equal(d.document.getElementById('tareBtn').disabled,true);
 assert.equal(d.document.getElementById('recording-counts').textContent,'Saved · GPS 20 · LoRa 30');
});
test('actual altitude markers identify fallback, recovery and gap without duplicate annotations',()=>{
 const d=dashboard();
 const send=(i,source,packet)=>d.run('handleReceiverEvent('+JSON.stringify({eventID:i,source,packet,height:'12',climb:'1',received:'2026-11-19T12:00:00.123Z'})+')');
 send(1,'LoRa','LORA_ALT');send(2,'Featherweight','STATUS');send(3,'Featherweight','RECORDER');send(4,'LoRa','LORA_ALT');
 assert.deepEqual(Array.from(d.run('customGraphs[0].chart.sourceMarkers.map(m=>m.text)')),['GPS fallback','LoRa recovered']);
 assert.equal(d.run('customGraphs[0].chart.data.datasets[0].data.filter(v=>v===null).length'),2);
 d.run('clearAllData(true)');assert.equal(d.run('customGraphs[0].chart.sourceMarkers.length'),0);
});
test('recorder failure, disconnect and a new backend session clear misleading status',()=>{
 const d=dashboard(); const send=e=>d.run('handleReceiverEvent('+JSON.stringify(e)+')');
 send({eventID:10,sessionID:'a',source:'LoRa',packet:'LORA_ALT',height:'420',recording:{state:'error',gps:4,lora:9}});
 assert.equal(d.document.getElementById('recording-state').textContent,'CSV write error');
 d.run('socket.onclose()');assert.equal(d.document.getElementById('recording-state').textContent,'CSV status unknown');
 send({eventID:1,sessionID:'b',snapshot:true,source:'none',recording:{state:'recording',gps:0,lora:0}});
 assert.equal(d.document.getElementById('max-alt').textContent,'--');
 assert.equal(d.document.getElementById('recording-counts').textContent,'Saved · GPS 0 · LoRa 0');
});
test('twenty Hz LoRa produces half-second means on both card and chart; CSV inputs stay untouched',()=>{
 const d=dashboard();let id=0;
 for(let i=0;i<=20;i++){
 const data={eventID:++id,packet:'LORA_ALT',loraPort:'A',source:'LoRa',height:String(i),climb:String(i),loraLive:true,received:new Date(i*50).toISOString()};
 d.run('handleReceiverEvent('+JSON.stringify(data)+')');
 if(i===8)assert.equal(d.document.getElementById('alt').textContent,'0');
 }
 assert.deepEqual(Array.from(d.run('customGraphs[0].chart.data.datasets[0].data')),[0,5.5,15.5]);
 assert.equal(d.document.getElementById('alt').textContent,'16');
 assert.equal(d.document.getElementById('upvel').textContent,'15.5');
 assert.equal(d.document.getElementById('max-alt').textContent,'20');
});

test('altitude diagnostics only prompt for tare when GPS is eligible',()=>{
 const d=dashboard();const msg=o=>d.run('altitudeMessage('+JSON.stringify(o)+')');
 assert.match(msg({source:'LoRa',loraLive:true,loraPort:'COM7'}),/COM7.*GPS backup is offline/);
 assert.match(msg({gpsPort:'COM8',gpsFixFresh:true,canTare:true}),/select.*Tare GPS/);
 assert.match(msg({source:'LoRa',loraPort:'COM7',gpsPort:'COM8',gpsFixFresh:true,gpsPad:'0'}),/backup is ready/);
 d.run('socket.onclose()');
 assert.match(d.document.getElementById('source-detail').textContent,/Retrying in 5 seconds/);
 assert.equal(d.document.getElementById('tareBtn').disabled,true);
});

test('shared mission header restores the running timer and freezes on landing', () => {
  const d = dashboard();
  const header = (phase, extra) => d.run('handleReceiverEvent(' + JSON.stringify({
    phase, snapshot:true, sessionID:'test', received:'2026-01-01T00:00:00Z', ...extra
  }) + ')');
  const el = id => d.document.getElementById(id);
  header('PAD', {eventID:1, tareLocked:true});
  assert.equal(el('flight-phase').textContent,'PAD');
  assert.match(el('mission-clock').textContent,/^\d{2}:\d{2}:\d{2}$/);
  header('DESCENT', {eventID:2,launchMs:10000,snapshotClockMs:36000});
  assert.equal(el('flight-phase').dataset.phase,'DESCENT');
  assert.equal(el('mission-clock').textContent,'T+00:26');
  header('LANDED', {eventID:3,launchMs:10000,landedMs:70000,snapshotClockMs:90000});
  d.run('performance.now = () => 120000; clearAllData(); updateClock()');
  assert.equal(el('flight-phase').dataset.phase,'LANDED');
  assert.equal(el('mission-clock').textContent,'T+01:00');
});

test('default and newly added graphs select readings by horizontal pointer position', () => {
  const d = dashboard();
  d.run("createGraph('volt','Battery')");
  for (const {chart} of d.run('customGraphs')) {
    assert.equal(chart.data.datasets[0].borderWidth,3);
    assert.equal(chart.options.interaction.intersect,false);
    assert.equal(chart.options.interaction.mode,'index');
    assert.equal(chart.options.interaction.axis,'x');
  }
});

test('graphs restart from a 5 s pad bank at launch and stop 5 s after landing', () => {
  const d = dashboard();
  const t0 = Date.parse('2026-11-19T12:00:00Z');
  const at = i => t0 + i * 50;
  const launchMs = at(200), landedMs = at(1300);
  for (let i = 0; i <= 1600; i++) {
    let h = 0, phase = 'PAD';
    if (i >= 200) { phase = 'ASCENT'; h = Math.min(i - 200, 600) * 2; }
    if (i >= 800) { phase = 'DESCENT'; h = Math.max(0, 1200 - (i - 800) * 3); }
    if (i >= 1300) { phase = 'LANDED'; h = 0; }
    const e = {eventID: i + 1, sessionID: 's', packet: 'LORA_ALT', source: 'LoRa', loraPort: 'COM6', loraLive: true,
      height: String(h), climb: '0', phase, received: new Date(at(i)).toISOString()};
    if (i >= 200) e.launchMs = launchMs;
    if (i >= 1300) e.landedMs = landedMs;
    d.run('handleReceiverEvent(' + JSON.stringify(e) + ')');
    if (i === 199) {
      assert.ok(d.run('customGraphs[0].chart.data.labels.length') > 15, 'pad graph runs live before launch');
    }
  }
  const labels = Array.from(d.run('customGraphs[0].chart.data.labels'));
  const data = Array.from(d.run('customGraphs[0].chart.data.datasets[0].data'));
  const times = labels.map(l => Date.parse('2026-11-19T' + l.split(' ')[0] + 'Z'));
  assert.ok(times.length > 0);
  assert.ok(times[0] >= launchMs - 5000 && times[0] < launchMs - 4000, 'first point comes from the 5 s bank');
  const flat = times.filter(t => t < launchMs).length;
  assert.ok(flat >= 8 && data.slice(0, flat).every(v => v === 0), 'pad lead-in stays flat at 0');
  assert.ok(times.at(-1) <= landedMs + 5000 && times.at(-1) > landedMs + 4000, 'plotting stops 5 s after landing');
  for (let i = 1; i < times.length; i++) assert.ok(times[i] - times[i - 1] <= 600, 'no gap in the trace');
  assert.ok(!data.some(v => v === null), 'no empty segment at launch');
  assert.equal(d.run('graphWindow'), 'done');
});

test('reload after the flight window shows no new graph points', () => {
  const d = dashboard();
  const t = Date.parse('2026-11-19T12:10:00Z');
  for (let i = 0; i < 40; i++) {
    d.run('handleReceiverEvent(' + JSON.stringify({eventID: i + 1, packet: 'LORA_ALT', source: 'LoRa', loraLive: true,
      height: '0', climb: '0', phase: 'LANDED', launchMs: t - 90000, landedMs: t - 10000,
      received: new Date(t + i * 50).toISOString()}) + ')');
  }
  assert.equal(d.run('customGraphs[0].chart.data.labels.length'), 0);
});

test('recorder shows a completed flight', () => {
  const d = dashboard();
  d.run("showRecording({state:'complete',gps:3,lora:900})");
  assert.equal(d.document.getElementById('recording-state').textContent, 'CSV saved · flight complete');
});

test('automatic GPS reference is described in the status line', () => {
  const d = dashboard();
  assert.match(d.run("altitudeMessage({source:'LoRa',loraLive:true,loraPort:'COM6',gpsPort:'COM5',gpsFixFresh:true,gpsPad:'840.00',gpsPadAuto:true})"), /matched to LoRa/);
});

test('signal strength graph is off by default and can still be added', () => {
  const d = dashboard();
  assert.deepEqual(Array.from(d.run('customGraphs.map(g => g.yKey)')), ['alt', 'upvel']);
  d.run("createGraph('rssi','Signal strength (%)')");
  assert.ok(d.run("customGraphs.some(g => g.yKey === 'rssi')"));
});
