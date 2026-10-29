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

// --- Two-window panel --------------------------------------------------
//
// Window 1: batches still at the supplier.   Window 2: batches the running
// update took and is saving one by one. Boxes are kept between refreshes
// (keyed by batch number) so they can animate in and out.

const MAX_BOXES = 40;

function range(from, to) {
  const out = [];
  for (let s = from; s <= to; s++) out.push(s);
  return out;
}

// boxKeys caps a long list: the first MAX_BOXES batches, then "+N".
function boxKeys(seqs) {
  if (seqs.length <= MAX_BOXES) return seqs.map(String);
  return [...seqs.slice(0, MAX_BOXES).map(String), "more"];
}

// syncBoxes makes el show exactly `keys`, in order. Boxes no longer wanted
// play a leaving animation chosen by leaveClass(key) before removal.
function syncBoxes(el, keys, decorate, leaveClass) {
  const want = new Set(keys);
  for (const node of [...el.children]) {
    if (!node.classList.contains("leaving") && !want.has(node.dataset.key)) {
      const how = leaveClass(node.dataset.key);
      node.classList.remove("processing", "pending", "new");
      node.classList.add("leaving", how);
      if (how === "saved") node.innerHTML = `#${node.dataset.key}<small>saved ✓</small>`;
      setTimeout(() => node.remove(), 750);
    }
  }
  const live = () => [...el.children].filter((n) => !n.classList.contains("leaving"));
  let prev = null;
  for (const key of keys) {
    let node = live().find((n) => n.dataset.key === key);
    if (!node) {
      node = document.createElement("div");
      node.dataset.key = key;
      node.className = "box new";
      node.addEventListener("animationend", () => node.classList.remove("new"), { once: true });
    }
    decorate(node, key);
    // Place it right after the previous live box; leaving boxes stay put.
    let ref = prev ? prev.nextSibling : el.firstChild;
    while (ref && ref.classList.contains("leaving")) ref = ref.nextSibling;
    if (node !== ref) el.insertBefore(node, ref);
    prev = node;
  }
}

function setBox(node, key, label, extra) {
  const text = key === "more" ? label : `#${key}`;
  const html = text + (extra ? `<small>${esc(extra)}</small>` : "");
  if (node.innerHTML !== html) node.innerHTML = html;
}

let lastBookmark = null;

function renderWindow(w) {
  const unfinished = w.status === "running" || w.status === "failed" || w.status === "interrupted";
  const w2End = unfinished && w.target > w.bookmark ? w.target : w.bookmark;
  const w2 = unfinished ? range(w.bookmark + 1, w2End) : [];
  const w1 = w.supplier_error ? [] : range(Math.max(w2End, w.bookmark) + 1, w.supplier_latest);
  const stoppedAt = (w.status === "failed" || w.status === "interrupted") && w2.length ? w2[0] : 0;

  // Window 1
  syncBoxes($("w1-boxes"), boxKeys(w1),
    (node, key) => {
      node.classList.toggle("more", key === "more");
      setBox(node, key, `+${w1.length - MAX_BOXES}`);
    },
    () => "moved");
  $("w1-info").textContent = w.supplier_error ? "supplier unreachable" : `${w1.length} batch${w1.length === 1 ? "" : "es"}`;

  // Window 2
  syncBoxes($("w2-boxes"), boxKeys(w2),
    (node, key) => {
      const seq = Number(key);
      node.classList.toggle("more", key === "more");
      // Only the current page is downloaded; the rest of Window 2 is
      // still at the supplier until its page is fetched.
      const pending = key !== "more" && seq > w.fetched && seq !== stoppedAt;
      node.classList.toggle("pending", pending);
      node.classList.toggle("processing", seq === w.processing);
      node.classList.toggle("blocked", seq === stoppedAt);
      node.classList.toggle("interrupted", seq === stoppedAt && w.status === "interrupted");
      const extra = seq === w.processing ? "saving…" : seq === stoppedAt ? "stopped" : pending ? "to fetch" : "";
      setBox(node, key, `+${w2.length - MAX_BOXES}`, extra);
    },
    (key) => (Number(key) <= w.bookmark ? "saved" : "gone"));
  const inMemory = w2.filter((seq) => seq <= w.fetched).length;
  $("w2-info").textContent = unfinished
    ? `${w2.length} left · ${inMemory} downloaded (page size ${w.page_size}) · refill ${w.refills} of ${w.max_refills}`
    : `page size ${w.page_size}`;

  // Bookmark
  const bm = $("bookmark");
  bm.textContent = w.bookmark ? `#${w.bookmark}` : "—";
  if (lastBookmark !== null && w.bookmark !== lastBookmark) {
    bm.classList.remove("bump");
    void bm.offsetWidth; // restart the animation
    bm.classList.add("bump");
  }
  lastBookmark = w.bookmark;

  // Plain-language explanation
  const [text, tone] = explainWindow(w, w1, w2, stoppedAt);
  const ex = $("w-explain");
  ex.textContent = text;
  ex.className = "explain " + tone;
}

function explainWindow(w, w1, w2, stoppedAt) {
  if (w.status === "failed") {
    return [`Stopped: the supplier failed while sending batch #${stoppedAt || w.bookmark + 1}. ` +
      `Everything up to the bookmark #${w.bookmark} is saved. Resume continues from #${w.bookmark + 1}.`, "bad"];
  }
  if (w.status === "interrupted") {
    return [`Interrupted: the application stopped during this update. ` +
      `Batches up to #${w.bookmark} are saved; nothing half-saved was kept. Resume continues from #${w.bookmark + 1}.`, "warn"];
  }
  if (w.status === "running") {
    if (w.processing) {
      return [`Saving batch #${w.processing} (all its changes in one transaction). ` +
        `When it is saved it leaves Window 2 and the bookmark moves to #${w.processing}. ` +
        `Batches marked “to fetch” are still at the supplier; they are downloaded ${w.page_size} at a time.`, ""];
    }
    if (w2.length) {
      return [`Downloading the next page: up to ${w.page_size} batches from #${w.bookmark + 1}.`, ""];
    }
    if (!w.target) {
      return [`Filling Window 2: asking the supplier for its newest batch.`, ""];
    }
    return [`Window 2 is empty: looking at Window 1 for batches that arrived meanwhile ` +
      `(refill ${w.refills} of ${w.max_refills} used).`, ""];
  }
  if (w.supplier_error) {
    const hint = /refused|no such host|dial/i.test(w.supplier_error)
      ? " Is the fictional supplier running (make supplier, from the project folder)?"
      : /503|unavailable/i.test(w.supplier_error)
        ? " The supplier answers but is unavailable (an outage may be switched on)."
        : "";
    return [`Cannot reach the supplier, so Window 1 is unknown. The catalog is unchanged.${hint} ` +
      `Details: ${w.supplier_error}`, "warn"];
  }
  if (w1.length === 0) {
    return [w.bookmark
      ? `Up to date: the supplier has nothing after batch #${w.bookmark}. ` +
        `Starting an update now reads no batches, so it shows 0 applied · 0 ignored. ` +
        `“Re-import everything” reads from #1 again to show that nothing is duplicated.`
      : `The supplier has not published any batches yet.`, "ok"];
  }
  const after = w.bookmark ? ` after the bookmark #${w.bookmark}` : "";
  return [`${w1.length} new batch${w1.length === 1 ? " is" : "es are"} waiting at the supplier${after}. ` +
    `Press “Start update” to move them into Window 2.`, ""];
}

let windowBusy = false;
async function refreshWindow() {
  if (windowBusy) return;
  windowBusy = true;
  try {
    renderWindow(await api("/api/window"));
  } catch (err) {
    // The main refresh shows connection errors.
  } finally {
    windowBusy = false;
  }
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
  refreshWindow();
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
  refreshWindow();
});

$("start").addEventListener("click", () => start(false));
$("reimport").addEventListener("click", () => {
  if (confirm("Read the whole supplier feed again? Products already up to date are left unchanged.")) start(true);
});

refresh();
refreshWindow();
setInterval(refresh, 1000);
setInterval(refreshWindow, 150);
