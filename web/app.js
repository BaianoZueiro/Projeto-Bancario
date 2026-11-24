"use strict";
const $ = id => document.getElementById(id);
const money = cents => (cents/100).toLocaleString("pt-BR",{style:"currency",currency:"BRL"});
const clock = time => new Date(time).toLocaleTimeString("pt-BR");
const esc = value => String(value).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
const states = {AUTORIZADA:"Autorizada",REJEITADA:"Rejeitada",EM_ANALISE:"Em análise",EM_RECONCILIACAO:"Em reconciliação"};
const reasons = {OK:"Controles atendidos e pagamento confirmado.",MFA:"O segundo fator simulado não foi validado.",CONTA:"Conta desabilitada no cadastro.",DISPOSITIVO:"Valor acima do teto acadêmico para dispositivo novo.",TETO_PESSOAL:"O valor excede o limite voluntário do cliente.",CARTAO_FREQUENTE:"Frequência elevada de compras no mesmo cartão.",PIX_FRACIONADO:"Sequência acumulada de Pix para o mesmo novo destino.",VALOR_ATIPICO:"Valor atípico combinado com destinatário novo.",REGIAO_E_DESTINO:"Região incomum combinada com destino novo.",SALDO:"Saldo disponível insuficiente.",GATEWAY_RECUSOU:"O gateway simulado recusou o pedido.",RESPOSTA_PENDENTE:"Valor reservado até consulta ao recibo.",RECIBO_CONFIRMADO:"O recibo confirmou a liquidação.",MOTOR_opa:"Controle obrigatório indisponível. Encaminhada para análise.",MOTOR_zen:"Motor de risco indisponível. Encaminhada para análise."};
const base = {client:"Bruno",product:"pix",amount:15000,destination:"mercado",mfa:true,registered:true,new:false,region:false,gateway:"sucesso",fault:""};
const scenarios = [
 ["Pagamento habitual",{}],["Compra no cartão",{product:"cartao"}],["Teto pessoal de Ana",{client:"Ana",amount:120000}],
 ["MFA ausente",{mfa:false}],["Dispositivo novo acima do teto",{registered:false,amount:25000}],
 ["Coação simulada com sinais atípicos",{client:"Carla",new:true,amount:600000}],
 ["Coação sem sinais observáveis",{}],["Três Pix fracionados",{new:true,amount:19000},3],
 ["Onze compras pequenas",{product:"cartao",amount:500},11],["Resposta tardia do gateway",{gateway:"timeout"}],
 ["Falha confirmada do gateway",{gateway:"falha"}],["Motor obrigatório indisponível",{fault:"opa"}],
 ["Erro SQL antes do commit",{fault:"sql"}],["Região incomum isolada",{region:true}]
];
let model, last, selected, busy=false;
const fields=["mfa","registered","new","region"];
$("checks").innerHTML=fields.map((id,i)=>'<label><input type="checkbox" id="'+id+'"> '+["MFA validado","Dispositivo cadastrado","Destino novo","Região incomum"][i]+'</label>').join("");
scenarios.forEach(([name],i)=>$("scenario").add(new Option(name,i)));
function preset(){
 const s={...base,...scenarios[$("scenario").value][1]};
 for(const k of ["client","product","gateway","destination"])$(k).value=s[k];
 $("amount").value=(s.amount/100).toFixed(2);
 fields.forEach(k=>$(k).checked=s[k]);
}
$("scenario").onchange=preset;preset();
async function api(path,body){
 const res=await fetch(path,body===undefined?{}:{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
 if(!res.ok){const text=await res.text();let msg=text;try{msg=JSON.parse(text).error}catch{}throw Error(msg.trim())}
 return res.json();
}
const pill=s=>'<span class="pill '+esc(s)+'">'+esc(states[s]||s)+'</span>';
function showResult(r){
 $("decision").innerHTML='<div class="result">'+pill(r.State)+'<p>'+esc(reasons[r.Reason]||r.Reason)+'</p><span class="muted">'+esc(r.ID)+' · '+(r.Replay?"Reenvio reconhecido":"Decisão registrada")+'</span></div>';
}
async function refresh(){
 try{
 model=await api("/api/painel");
 $("connection").textContent="● Painel conectado";
 const stats=model.stats[0];
 $("metrics").innerHTML=[["Operações",stats.total],["Autorizadas",stats.approved],["Em análise",stats.review],["Rejeitadas",stats.denied]].map(([k,v])=>'<div>'+k+'<strong>'+v+'</strong></div>').join("");
 $("accounts").innerHTML=model.accounts.map(a=>'<div class="acct"><b>'+esc(a.id)+'</b><div>'+money(a.balance-a.reserved)+' <span>disponíveis'+(a.reserved?' · '+money(a.reserved)+' reservados':'')+'</span></div></div>').join("");
 $("history").innerHTML=model.operations.map(o=>'<tr><td>'+clock(o.created)+'</td><td>'+esc(o.client)+'</td><td>'+esc(o.product.toUpperCase())+'</td><td>'+money(o.amount)+'</td><td>'+pill(o.state)+'</td><td><button class="secondary" data-id="'+esc(o.id)+'">Detalhes</button></td></tr>').join("")||'<tr><td colspan="6">Execute um cenário para iniciar o histórico.</td></tr>';
 $("health").textContent="Decisão p95: "+model.p95.toFixed(2)+" ms · Eventos na fila: "+model.queue[0].pending+" · Erros internos desde o início: "+model.errors;
 $("siem").textContent=model.settings[0].enabled?"Pausar entrega ao SIEM":"Retomar entrega ao SIEM";
 $("events").innerHTML=model.events.map(e=>'<article><span>'+esc(e.kind)+' · '+esc(reasons[e.detail]||e.detail)+'</span><small>'+clock(e.created)+'</small></article>').join("")||"Nenhum evento entregue.";
 }catch(e){$("connection").textContent="Falha de conexão · "+e.message}
}
async function send(retry){
 if(busy)return;busy=true;$("submit").disabled=$("retry").disabled=true;
 try{
 const [,presetData,n=1]=scenarios[$("scenario").value];
 for(let i=0;i<(retry?1:n);i++){
 if(!retry){last={...base,id:crypto.randomUUID(),fault:presetData.fault||""};
 for(const k of ["client","product","gateway","destination"])last[k]=$(k).value;
 fields.forEach(k=>last[k]=$(k).checked);last.amount=Math.round(Number($("amount").value)*100);
 }
 const r=await api("/transacoes",last);showResult(r);
 $("status").textContent=retry?"Reenvio concluído.":(i+1)+" de "+n+" operação(ões) registrada(s).";
 }
 if(!retry && $("scenario").value==="6")$("status").textContent+=" A coação descrita no cenário ficou invisível aos controles.";
 }catch(e){$("status").textContent=e.message;$("decision").textContent=""}finally{busy=false;$("submit").disabled=$("retry").disabled=false;$("retry").hidden=!last;await refresh()}
}
$("form").onsubmit=e=>{e.preventDefault();send(false)};$("retry").onclick=()=>send(true);
async function inspect(id){
 selected=id;const r=await api("/transacoes/"+encodeURIComponent(id));
 const ticket=model?.disputes.find(c=>c.operation===id);
 $("inspection").innerHTML=pill(r.State)+'<p>'+esc(reasons[r.Reason]||r.Reason)+'</p><b>'+esc(id)+'</b><pre>'+esc(JSON.stringify(JSON.parse(r.Evidence),null,2))+'</pre>'+(ticket?'Contestação aberta · '+esc(ticket.protocol):'');
 $("resolve").hidden=r.State!=="EM_RECONCILIACAO";$("dispute").hidden=!!ticket;
 if(!$("detail").open)$("detail").showModal();
}
$("history").onclick=async e=>{if(e.target.dataset.id){$("detailStatus").textContent="";$("note").value="";try{await inspect(e.target.dataset.id)}catch(err){$("status").textContent=err.message}}};
$("close").onclick=()=>$("detail").close();
$("resolve").onclick=async()=>{try{const r=await api("/transacoes/"+selected+"/reconciliar",{});showResult(r);await refresh();await inspect(selected)}catch(e){$("detailStatus").textContent=e.message}};
$("dispute").onsubmit=async e=>{e.preventDefault();try{const d=await api("/transacoes/"+selected+"/contestar",{note:$("note").value});$("detailStatus").textContent="Protocolo "+d.protocol;await refresh();await inspect(selected)}catch(err){$("detailStatus").textContent=err.message}};
$("siem").onclick=async()=>{try{await api("/api/siem",{enabled:!model.settings[0].enabled});await refresh()}catch(e){$("health").textContent=e.message}};
$("policy").onclick=async()=>{try{await api("/api/politica",{})}catch(e){$("health").textContent=e.message;$("policy").textContent=e.message}};
refresh();setInterval(refresh,2500);
