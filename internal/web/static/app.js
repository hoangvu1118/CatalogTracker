// Staff page: polls the JSON API and renders updates, changes, problems
// and the catalog. No framework, no build step.
"use strict";

const OUTCOMES = ["created", "updated", "withdrawn", "restocked", "duplicate", "stale"];
const state = { selected: null, outcome: "", productStatus: "", latestId: null };

const $ = (id) => document.getElementById(id);

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function api(path, options) {
  const res = await fetch(path, options);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(body.error || res.statusText), { status: res.status, body });
  return body;
}

function money(cents, currency) {
  if (!currency) return "—";
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(cents / 100);
}

function when(t) {
  return t ? new Date(t).toLocaleTimeString() : "—";
}

function badge(cls, text) {
  return `<span class="badge ${cls}">${esc(text)}</span>`;
}

function showMessage(text) {
  const el = $("message");
  el.textContent = text;
  el.hidden = !text;
}

// --- Latest update card ------------------------------------------------

function renderCurrent(u) {
  if (!u) return;
  const total = Math.max(u.target - u.start_bookmark, u.batches_done, 1);
  const pct = u.status === "completed" ? 100 : Math.min(100, Math.round((u.bookmark - u.start_bookmark) / total * 100));

  let progress;
  switch (u.status) {
    case "running":
      progress = `${u.batches_done} batches saved · now on batch ${u.next_batch}` + (u.target ? ` of ${u.target}` : "");
      break;
    case "completed":
      progress = `${u.batches_done} batches saved · up to date at batch ${u.bookmark}`;
      break;
    default:
      progress = `${u.batches_done} batches saved · next: batch ${u.next_batch}` + (u.target ? ` of ${u.target}` : "");
  }

  const counts = OUTCOMES.map((o) => badge("o-" + o, `${o} ${u.changes[o]}`)).join("");
  $("current-body").innerHTML = `
    <div>${badge("s-" + u.status, u.status)} <strong>Update #${u.id}</strong>
      ${u.refills ? `<span class="muted"> · window refilled ${u.refills}×</span>` : ""}</div>
    <div class="progress">${esc(progress)}</div>
    <div class="bar"><div style="width:${pct}%"></div></div>
    ${u.error ? `<div class="error">${esc(u.error)}</div>` : ""}
    ${u.can_resume ? `<button class="primary" data-resume="${u.id}">Resume from batch ${u.next_batch}</button>` : ""}
    <div class="counts">${counts}</div>
    <p class="muted">${u.open_problems} open problem(s) from this update.</p>`;
}

// --- History -----------------------------------------------------------

function renderHistory(updates) {
  $("history").innerHTML = updates.map((u) => {
    const applied = u.changes.created + u.changes.updated + u.changes.withdrawn + u.changes.restocked;
    const ignored = u.changes.duplicate + u.changes.stale;
    return `<tr class="clickable ${state.selected === u.id ? "selected" : ""}" data-select="${u.id}">
      <td>${u.id}</td>
      <td>${badge("s-" + u.status, u.status)}</td>
      <td>${u.batches_done}</td>
      <td>${applied} applied · ${ignored} ignored</td>
      <td>${u.open_problems}</td>
      <td>${when(u.started_at)}</td>
      <td>${when(u.finished_at)}</td>
    </tr>`;
  }).join("") || `<tr><td colspan="7" class="muted">No updates yet.</td></tr>`;
}

// --- Problems ----------------------------------------------------------

function problemItem(p) {
  const what = p.kind === "supplier_unavailable" ? "Supplier unavailable" : `Invalid record ${p.sku ? esc(p.sku) + " v" + p.version : "(no SKU)"}`;
  return `<li><strong>${what}</strong>${p.open ? "" : " " + badge("o-created", "resolved")}
    <div>${esc(p.message)}</div>
    <div class="meta">update #${p.update_id} · batch ${p.batch} · ${when(p.created_at)}</div></li>`;
}

function renderOpenProblems(problems) {
  $("open-count").textContent = problems.length;
  $("open-count").classList.toggle("has", problems.length > 0);
  $("open-problems").innerHTML = problems.map(problemItem).join("") || `<li class="muted">Nothing needs attention.</li>`;
}

// --- Selected update detail --------------------------------------------

function renderFilters(el, values, current, attr) {
  el.innerHTML = values.map(([value, label]) =>
    `<button class="${current === value ? "on" : ""}" ${attr}="${value}">${esc(label)}</button>`).join("");
}

async function renderDetail() {
  if (!state.selected) return;
  const id = state.selected;
  const query = state.outcome ? `?outcome=${state.outcome}` : "";
  const [changes, problems] = await Promise.all([
    api(`/api/updates/${id}/changes${query}`),
    api(`/api/updates/${id}/problems`),
  ]);
  $("detail").hidden = false;
  $("detail-id").textContent = "#" + id;
  renderFilters($("outcome-filters"), [["", "All"], ...OUTCOMES.map((o) => [o, o])], state.outcome, "data-outcome");
  $("changes").innerHTML = changes.map((c) => `<tr>
      <td>${c.batch}</td><td class="mono">${esc(c.sku)}</td><td>v${c.version}</td>
      <td>${badge("o-" + c.outcome, c.outcome)}</td><td class="detail">${esc(c.detail)}</td></tr>`).join("")
    || `<tr><td colspan="5" class="muted">No changes.</td></tr>`;
  $("update-problems").innerHTML = problems.map(problemItem).join("") || `<li class="muted">None.</li>`;
}

// --- Catalog -----------------------------------------------------------

async function renderProducts() {
  const query = state.productStatus ? `?status=${state.productStatus}` : "";
  const products = await api(`/api/products${query}`);
  $("product-count").textContent = products.length;
  renderFilters($("product-filters"), [["", "All"], ["active", "Active"], ["withdrawn", "Withdrawn"]], state.productStatus, "data-status");
  $("products").innerHTML = products.map((p) => `<tr>
      <td class="mono">${esc(p.sku)}</td><td>${esc(p.name) || '<span class="muted">unknown</span>'}</td>
      <td>${money(p.price_cents, p.currency)}</td><td>${badge("p-" + p.status, p.status)}</td><td>v${p.version}</td></tr>`).join("")
    || `<tr><td colspan="5" class="muted">The catalog is empty.</td></tr>`;
}

// --- Refresh loop ------------------------------------------------------

async function refresh() {
  try {
    const [updates, open] = await Promise.all([api("/api/updates"), api("/api/problems?open=true")]);
    const latest = updates[0];
    if (latest && state.latestId !== latest.id) {
      state.latestId = latest.id;
      state.selected = latest.id; // follow the newest update
    }
    renderCurrent(latest);
    renderHistory(updates);
    renderOpenProblems(open);
    $("start").disabled = $("reimport").disabled = Boolean(latest && latest.status !== "completed");
    await Promise.all([renderDetail(), renderProducts()]);
  } catch (err) {
    showMessage("Cannot reach the application: " + err.message);
  }
}

async function start(fromBeginning) {
  showMessage("");
  try {
    await api("/api/updates", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ from_beginning: fromBeginning }) });
  } catch (err) {
    showMessage(err.body?.unfinished_update_id
      ? `Update #${err.body.unfinished_update_id} is not finished. Resume it first.`
      : err.message);
  }
  refresh();
}

document.addEventListener("click", async (e) => {
  const t = e.target.closest("[data-resume],[data-select],[data-outcome],[data-status]");
  if (!t) return;
  if (t.dataset.resume) {
    showMessage("");
    try { await api(`/api/updates/${t.dataset.resume}/resume`, { method: "POST" }); } catch (err) { showMessage(err.message); }
  } else if (t.dataset.select) {
    state.selected = Number(t.dataset.select);
    state.outcome = "";
  } else if (t.dataset.outcome !== undefined) {
    state.outcome = t.dataset.outcome;
  } else if (t.dataset.status !== undefined) {
    state.productStatus = t.dataset.status;
  }
  refresh();
});

$("start").addEventListener("click", () => start(false));
$("reimport").addEventListener("click", () => {
  if (confirm("Read the whole supplier feed again? Products already up to date are left unchanged.")) start(true);
});

refresh();
setInterval(refresh, 1500);
