const screens = {
  simulador: ["Simulador de operações", "Simule um pagamento e acompanhe a decisão, as regras e o efeito no saldo."],
  historico: ["Auditoria de transações", "Consulte as últimas 60 operações e abra os detalhes para reconciliar um pagamento ou registrar uma contestação."],
  noc: ["Monitoramento operacional", "Acompanhe o tempo de decisão, a fila de entrega e os erros internos da API."],
  soc: ["Central de segurança", "Investigue os últimos 60 eventos entregues ao SOC e consulte as operações relacionadas."],
  contestacoes: ["Contestações", "Consulte o relato e a operação de origem de cada protocolo registrado pela auditoria."]
};

function navigate() {
  let route = location.hash.slice(1) || "simulador";
  if (route === "monitor") route = "noc";
  if (!Object.hasOwn(screens, route)) route = "simulador";
  document.querySelectorAll("[data-view]").forEach(view => view.hidden = view.dataset.view !== route);
  document.querySelectorAll("nav a").forEach(link => {
    if (link.hash === "#" + route) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  });
  const [title, caption] = screens[route];
  $("screen-title").textContent = title;
  $("screen-caption").textContent = caption;
  $("view-error").textContent = "";
  document.title = title + " · Finslet";
  window.scrollTo(0, 0);
}

function detailButton(id, label) {
  return '<button class="secondary" data-id="' + esc(id) + '">' + esc(label) + '</button>';
}

function renderAudit(data) {
  const term = $("audit-search").value.trim().toLocaleLowerCase("pt-BR");
  const state = $("audit-state").value;
  const visible = data.operations.filter(op =>
    (!state || op.state === state) &&
    [op.id, op.client, op.destination].some(value => value.toLocaleLowerCase("pt-BR").includes(term))
  );
  $("audit-count").textContent = visible.length + " de " + data.operations.length + " operações recentes";
  $("history").innerHTML = visible.map(op => `<tr>
    <td>${clock(op.created)}</td><td>${esc(op.client)}</td><td>${esc(op.product.toUpperCase())}</td>
    <td>${money(op.amount)}</td><td>${pill(op.state)}</td><td>${detailButton(op.id, "Detalhes")}</td>
  </tr>`).join("") || '<tr><td colspan="6">Nenhuma operação encontrada.</td></tr>';
}

function renderEvents(data) {
  const filter = $("soc-filter").value;
  const items = data.events.filter(e => !filter || e.kind === filter ||
    (filter === "risco" && ["REJEITADA", "EM_ANALISE"].includes(e.kind)));
  $("events").innerHTML = items.map(e => `<article>
    <div><b>${esc(states[e.kind] || e.kind)}</b><p>${esc(reasons[e.detail] || e.detail)}</p></div>
    <div class="event-meta"><time>${clock(e.created)}</time>
    ${e.kind === "ACESSO_NEGADO" ? "" : detailButton(e.operation, "Ver operação")}</div>
  </article>`).join("") || "<p>Nenhum evento entregue para este filtro.</p>";
}

function renderViews(data) {
  renderAudit(data);
  renderEvents(data);
  const enabled = Boolean(data.settings[0].enabled);
  $("noc-metrics").innerHTML = [["Tempo de decisão · p95",data.p95.toLocaleString("pt-BR",{maximumFractionDigits:2})+" ms"],
    ["Eventos na fila",data.queue[0].pending],["Erros desde o início",data.errors]]
    .map(([name,value]) => `<div>${name}<strong>${value}</strong></div>`).join("");
  $("delivery").textContent = enabled ? "Entrega ativa. A fila é consultada a cada segundo." : "Entrega pausada. Os eventos continuam registrados na fila.";
  $("siem").disabled = false;
  $("siem").textContent = enabled ? "Pausar entrega ao SIEM" : "Retomar entrega ao SIEM";
  $("tickets").innerHTML = data.disputes.map(d => `<tr><td>${esc(d.protocol)}</td><td>${esc(d.note)}</td><td>${detailButton(d.operation, "Abrir operação")}</td></tr>`).join("") ||
    '<tr><td colspan="3">Nenhuma contestação registrada. Abra uma operação na <a href="#historico">auditoria</a> para criar um protocolo.</td></tr>';
}

function initViews() {
  window.addEventListener("hashchange", navigate);
  $("audit-search").oninput = $("audit-state").onchange = () => { if (model) renderAudit(model); };
  $("soc-filter").onchange = () => { if (model) renderEvents(model); };
  navigate();
}
