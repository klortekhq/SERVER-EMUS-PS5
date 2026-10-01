package httpapi

import (
	"io"
	"net/http"
)

func (s *Server) adminRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	_, _ = io.WriteString(w, adminHTML)
}

const adminHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>SERVER-EMUS-PS5</title>
<style>
:root{font-family:system-ui,sans-serif;color-scheme:dark;background:#111;color:#eee}
body{max-width:1100px;margin:0 auto;padding:24px}
h1{margin:0 0 4px}.muted{color:#aaa}.row{display:flex;gap:8px;flex-wrap:wrap;align-items:center}
.card{border:1px solid #333;border-radius:10px;padding:16px;margin:16px 0;background:#181818}
input,textarea,button{font:inherit;background:#222;color:#eee;border:1px solid #444;border-radius:6px;padding:8px}
input{min-width:320px}textarea{width:100%;min-height:220px;box-sizing:border-box}
button{cursor:pointer}button:hover{background:#2d2d2d}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:7px;border-bottom:1px solid #333}
.ok{color:#8fda8f}.bad{color:#ff9b9b}pre{white-space:pre-wrap;word-break:break-word}
</style>
</head>
<body>
<h1>SERVER-EMUS-PS5</h1>
<div class="muted">Local/LAN administration shell. The bearer token is stored only for this browser tab.</div>

<div class="card">
  <div class="row">
    <input id="token" type="password" autocomplete="off" placeholder="Bearer token (leave empty when server auth is disabled)">
    <button id="saveToken">Use token</button>
    <button id="refresh">Refresh</button>
  </div>
  <p id="status" class="muted">Not connected.</p>
</div>

<div class="card">
  <h2>Libraries</h2>
  <table><thead><tr><th>Name</th><th>System</th><th>Recursive</th><th>Extensions</th><th>Files</th></tr></thead><tbody id="libraries"></tbody></table>
  <p class="muted">Physical paths are intentionally not returned by discovery APIs.</p>
</div>

<div class="card">
  <h2>Transport metrics</h2>
  <pre id="metrics">—</pre>
  <div class="row">
    <button id="resetMetrics">Reset metrics</button>
    <button id="rebuild">Rebuild catalog</button>
  </div>
</div>

<div class="card">
  <h2>Replace libraries</h2>
  <p class="muted">This is a complete replacement. Use “Template” to copy safe metadata, then re-enter each physical path before applying.</p>
  <button id="template">Template from current libraries</button>
  <textarea id="editor" spellcheck="false">{"libraries":[]}</textarea>
  <div class="row"><button id="apply">Validate and replace libraries</button></div>
  <pre id="result"></pre>
</div>

<script>
const $=id=>document.getElementById(id);
let libraries=[];

function token(){ return sessionStorage.getItem("serverEmusToken") || ""; }
function headers(json=false){
  const h={};
  const t=token();
  if(t) h.Authorization="Bearer "+t;
  if(json) h["Content-Type"]="application/json";
  return h;
}
async function api(path, options={}){
  options.headers={...headers(Boolean(options.body)),...(options.headers||{})};
  const r=await fetch(path,options);
  const text=await r.text();
  let body=text;
  try{ body=text?JSON.parse(text):null; }catch{}
  if(!r.ok) throw new Error((body&&body.error)||("HTTP "+r.status));
  return body;
}
function escCell(value){
  const td=document.createElement("td");
  td.textContent=String(value??"");
  return td;
}
function renderLibraries(items){
  libraries=items||[];
  const tbody=$("libraries"); tbody.replaceChildren();
  for(const lib of libraries){
    const tr=document.createElement("tr");
    tr.append(escCell(lib.name),escCell(lib.system),escCell(lib.recursive),
      escCell((lib.extensions||[]).join(", ")),escCell(lib.files));
    tbody.append(tr);
  }
}
async function refresh(){
  $("status").textContent="Connecting…"; $("status").className="muted";
  try{
    const [health,libs,metrics]=await Promise.all([
      api("/api/v1/health"),api("/api/v1/libraries"),api("/api/v1/metrics")
    ]);
    renderLibraries(libs);
    $("metrics").textContent=JSON.stringify(metrics,null,2);
    $("status").textContent="Connected · API "+health.api+
      " · library editing "+(health.capabilities.library_editing?"enabled":"disabled");
    $("status").className="ok";
  }catch(e){
    $("status").textContent=e.message; $("status").className="bad";
  }
}
$("saveToken").onclick=()=>{
  sessionStorage.setItem("serverEmusToken",$("token").value.trim());
  refresh();
};
$("refresh").onclick=refresh;
$("resetMetrics").onclick=async()=>{
  try{ await api("/api/v1/admin/metrics/reset",{method:"POST"}); await refresh(); }
  catch(e){ $("result").textContent=e.message; }
};
$("rebuild").onclick=async()=>{
  try{ const r=await api("/api/v1/catalog/rebuild",{method:"POST"}); $("result").textContent=JSON.stringify(r,null,2); await refresh(); }
  catch(e){ $("result").textContent=e.message; }
};
$("template").onclick=()=>{
  $("editor").value=JSON.stringify({libraries:libraries.map(l=>({
    name:l.name,system:l.system,path:"",recursive:l.recursive,extensions:l.extensions||[]
  }))},null,2);
};
$("apply").onclick=async()=>{
  try{
    const parsed=JSON.parse($("editor").value);
    if(!parsed||!Array.isArray(parsed.libraries)||!parsed.libraries.length) throw new Error("At least one library is required.");
    for(const lib of parsed.libraries){
      if(!lib.name||!lib.system||!lib.path) throw new Error("Every library needs name, system and physical path.");
    }
    const r=await api("/api/v1/admin/libraries",{method:"PUT",body:JSON.stringify(parsed)});
    $("result").textContent=JSON.stringify(r,null,2);
    await refresh();
  }catch(e){ $("result").textContent=e.message; }
};
$("token").value=token();
refresh();
</script>
</body>
</html>
`;
