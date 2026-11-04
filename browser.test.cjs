const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(process.env.ARES_APP || path.join(__dirname, 'public/js/app.js'), 'utf8');
function dashboard() {
  const elements = new Map();
  function element() { return {textContent:'', innerHTML:'', style:{}, dataset:{}, children:[], classList:{add(){},remove(){},toggle(){}}, appendChild(e){this.children.push(e)}, querySelectorAll(){return this.children}, remove(){}, getContext(){return {}}}; }
  const document = {getElementById(id){if(!elements.has(id)) elements.set(id,element());return elements.get(id)},createElement:element};
  const select = document.getElementById('ySelect');
  select.options = ['alt','vel','upvel','volt','rssi'].map(value=>({value,text:value}));
  select.selectedIndex = 0;
  Object.defineProperty(select,'value',{get(){return this.options[this.selectedIndex].value},set(v){this.selectedIndex=this.options.findIndex(o=>o.value===v)}});
  class Chart {static defaults={font:{}}; constructor(ctx,config){this.data=config.data;this.destroyed=false} update(){} destroy(){this.destroyed=true} }
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
test('clear removes stale acceleration and graph deduplication state',()=>{
 const d=dashboard();d.run(`currentG=8;lastUpVel=200;lastTrackerTime=20;window.lastGraphTime='old';clearAllData();`);
 assert.equal(d.run('currentG'),null);assert.equal(d.run('lastUpVel'),null);assert.equal(d.run('lastTrackerTime'),null);assert.equal(d.run('window.lastGraphTime'),undefined);
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
 const d=dashboard();d.run(`updateCustomGraphs({packet:'RX_NOMTK',time:'12:00:00',alt:'1000',upvel:'10',rssi:'80'});`);
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
