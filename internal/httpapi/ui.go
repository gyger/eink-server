package httpapi

import "net/http"

func (a *API) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Joan Tablet Server</title><style>
:root{font-family:system-ui,sans-serif;color:#1d2520;background:#edf0eb}body{max-width:1100px;margin:2rem auto;padding:0 1rem}h1{font-size:1.7rem}.bar{display:flex;gap:.7rem;align-items:center;flex-wrap:wrap;margin-bottom:1rem}.card{background:white;border:1px solid #d6dbd4;border-radius:10px;padding:1rem;margin:.8rem 0;display:grid;grid-template-columns:minmax(180px,1fr) 2fr;gap:1rem}.meta{color:#566159;font-size:.9rem;line-height:1.6}.online{color:#16713b}.offline{color:#9b352d}button,input,select{font:inherit;padding:.45rem;border:1px solid #adb6ad;border-radius:5px}button{background:#234f36;color:white;cursor:pointer}.preview{max-width:100%;max-height:250px;border:1px solid #ddd}.controls{display:flex;gap:.45rem;flex-wrap:wrap;margin:.5rem 0}@media(max-width:650px){.card{grid-template-columns:1fr}}</style></head><body>
<h1>Joan Tablet Server</h1><div class="bar"><input id="broadcast" type="file" accept="image/png,image/jpeg"><button onclick="sendAll()">Send to all</button><span id="notice"></span></div><main id="devices">Loading…</main>
<script>
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
async function load(){let r=await fetch('/api/v1/devices');let ds=await r.json();devices.innerHTML=ds.length?'':'No tablets have connected yet.';for(const d of ds){let c=document.createElement('section');c.className='card';let state=d.connected?'Connected':d.online?'Recently seen':'Offline';let frame=d.desired_frame?esc(d.desired_frame.state)+' #'+esc(d.desired_frame.frame_id):'none';c.innerHTML='<div><strong>'+esc(d.name||d.uuid.slice(0,12))+'</strong><div class="meta '+(d.online?'online':'offline')+'">'+state+'<br>'+esc(d.width)+'×'+esc(d.height)+' · FW '+esc(d.firmware)+'<br>Battery '+esc(d.battery)+'% · '+esc(d.temperature)+'°C · Humidity '+esc(d.humidity)+'%<br>Last seen '+new Date(d.last_seen).toLocaleString()+'<br>Frame '+frame+'</div></div><div><img class="preview" src="/api/v1/devices/'+encodeURIComponent(d.uuid)+'/image?t='+Date.now()+'" onerror="this.style.display=\'none\'"><div class="controls"><input class="file" type="file" accept="image/png,image/jpeg"><button class="send">Send</button></div><div class="controls"><select class="fit"><option>contain</option><option>cover</option><option>stretch</option><option>exact</option></select><select class="background"><option>white</option><option>black</option></select><select class="rotation"><option value="0">0°</option><option value="90">90°</option><option value="180">180°</option><option value="270">270°</option></select><select class="dither"><option value="none">No dither</option><option value="floyd-steinberg">Floyd–Steinberg</option></select><label><input class="invert" type="checkbox"> Invert</label></div><div class="controls"><input class="name" value="'+esc(d.name)+'" placeholder="Device name"><button class="save">Save settings</button></div></div>';for(const k of ['fit','background','rotation','dither'])c.querySelector('.'+k).value=String(d.image_defaults[k]);c.querySelector('.invert').checked=d.image_defaults.invert;c.querySelector('.send').onclick=()=>sendOne(d.uuid,c);c.querySelector('.save').onclick=()=>save(d.uuid,c);devices.append(c)}}
function settings(c){return {fit:c.querySelector('.fit').value,background:c.querySelector('.background').value,rotation:Number(c.querySelector('.rotation').value),invert:c.querySelector('.invert').checked,dither:c.querySelector('.dither').value}}
async function sendOne(id,c){let f=c.querySelector('.file').files[0];if(!f)return note('Choose an image first');let q=new URLSearchParams(settings(c));let r=await fetch('/api/v1/devices/'+encodeURIComponent(id)+'/image?'+q,{method:'PUT',headers:{'Content-Type':f.type},body:f});note(r.ok?'Image queued':await err(r));load()}
async function sendAll(){let f=broadcast.files[0];if(!f)return note('Choose an image first');let r=await fetch('/api/v1/images:broadcast',{method:'POST',headers:{'Content-Type':f.type},body:f});note(r.ok?'Broadcast queued':await err(r));load()}
async function save(id,c){let r=await fetch('/api/v1/devices/'+encodeURIComponent(id),{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:c.querySelector('.name').value,image_defaults:settings(c)})});note(r.ok?'Saved':await err(r));load()}
async function err(r){try{return (await r.json()).error.message}catch{return r.statusText}}function note(s){notice.textContent=s;setTimeout(()=>notice.textContent='',4000)}load();setInterval(load,30000);
</script></body></html>`
