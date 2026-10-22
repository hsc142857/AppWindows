// ActivityLogKeeper 관리 창. Go 바인딩은 window.go.main.API.* (Wails가 런타임에 만든다).

const $ = (id) => document.getElementById(id);
let api;

function waitForAPI() {
  return new Promise((resolve) => {
    const check = () => (window.go?.main?.API ? resolve(window.go.main.API) : setTimeout(check, 30));
    check();
  });
}

// ---- 공통 ----

let toastTimer;
function toast(msg, isError = false) {
  const t = $("toast");
  t.textContent = msg;
  t.className = isError ? "error" : "";
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), isError ? 8000 : 4000);
}

async function call(fn, ...args) {
  try {
    return await fn(...args);
  } catch (e) {
    toast(String(e?.message || e), true);
    throw e;
  }
}

function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v);
  }
  for (const c of children) e.append(c ?? "");
  return e;
}

function fmtTime(s) {
  if (!s) return "-";
  const d = new Date(s);
  return isNaN(d) ? s : d.toLocaleString();
}

// 표에서는 짧게: 올해면 "10-19 15:43", 아니면 "2025-10-19 15:43"
function fmtShort(s) {
  if (!s) return "-";
  const d = new Date(s);
  if (isNaN(d)) return s;
  const p = (n) => String(n).padStart(2, "0");
  const md = `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  return d.getFullYear() === new Date().getFullYear() ? md : `${d.getFullYear()}-${md}`;
}

function fmtSeconds(sec) {
  if (sec % 3600 === 0) return `${sec / 3600}시간`;
  if (sec % 60 === 0) return `${sec / 60}분`;
  return `${sec}초`;
}

const STATUS_LABEL = { ok: "정상", empty: "표 못 찾음", login_required: "로그인 필요", error: "오류", "": "-" };
function statusCell(status, title = "") {
  return el("td", { class: "nowrap status-" + status, title }, STATUS_LABEL[status] ?? status);
}

const targetLabel = (t) => t.name || t.url;

// fillTargetFilter는 대상 선택 상자를 다시 채우고 이전 선택(또는 data-want로 요청한 값)을 유지한다.
async function fillTargetFilter(select) {
  const current = select.dataset.want ?? select.value;
  delete select.dataset.want;
  const targets = await call(api.Targets);
  select.replaceChildren(el("option", { value: "" }, "전체 대상"),
    ...targets.map((t) => el("option", { value: t.url }, targetLabel(t))));
  select.value = current;
  return targets;
}

// ---- 탭 ----

const loaders = {};
function showTab(name) {
  document.querySelectorAll("nav button").forEach((x) => x.classList.toggle("active", x.dataset.tab === name));
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.id === "tab-" + name));
  loaders[name]?.();
}
document.querySelectorAll("nav button").forEach((b) => b.addEventListener("click", () => showTab(b.dataset.tab)));

// ---- 개요 ----

loaders.overview = async () => {
  const o = await call(api.Overview);
  $("ov-targets").textContent = `${o.enabled} / ${o.targets}`;
  $("ov-entries").textContent = o.entries.toLocaleString();
  $("ov-unsynced").textContent = o.unsynced.toLocaleString();
  $("ov-last").textContent = fmtTime(o.lastCapture);
  $("ov-interval").textContent = `${fmtSeconds(o.settings.intervalSeconds)} (로딩 후 ${o.settings.loadDelaySeconds}초 대기)`;
  $("ov-api").textContent = o.api;
  $("ov-rejected").hidden = !o.rejected;
  $("ov-rejected-text").textContent = `서버가 거부한 로그 ${o.rejected}행은 업로드에서 빠져 있습니다. (사유는 CSV의 서버 전송 열)`;
  $("ov-host").textContent = o.hostInstalled ? `등록됨 (${o.hostDetail})` : `등록 안 됨 — 설정 탭에서 등록하세요 (${o.hostDetail})`;
  $("ov-home").textContent = o.home;
  $("ov-version").textContent = o.version;
  const box = $("ov-attention");
  if (o.needAttention.length) {
    box.replaceChildren(
      el("b", {}, "수집이 정상이 아닌 대상이 있습니다."),
      el("ul", {}, ...o.needAttention.map((t) => el("li", {},
        `${targetLabel(t)} — ${STATUS_LABEL[t.lastStatus] ?? t.lastStatus} (${fmtTime(t.lastCapturedAt)})`,
        t.lastStatus === "login_required" ? " · Chrome에서 다시 로그인하세요."
          : t.lastStatus === "empty" ? " · 화면이 바뀌었는지 확인하고 선택자를 지정하세요."
          : t.lastError ? ` · ${t.lastError}` : "")))
    );
    box.hidden = false;
  } else {
    box.hidden = true;
  }
};

$("ov-refresh").addEventListener("click", () => loaders.overview());
$("ov-retry").addEventListener("click", async () => {
  const n = await call(api.RetryRejected);
  toast(`거부된 ${n}행을 다음 동기화 때 다시 보냅니다.`);
  loaders.overview();
});
$("ov-sync").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const r = await call(api.SyncNow);
    toast(r.apiConfigured
      ? `동기화 완료: 보냄 ${r.sent}행, 남음 ${r.remaining}행` + (r.rejected ? `, 서버 거부 ${r.rejected}행` : "")
      : "Web API가 설정되지 않았습니다. 설정 탭에서 입력하세요.");
  } finally {
    e.target.disabled = false;
    loaders.overview();
  }
});

// ---- 대상 ----

loaders.targets = async () => {
  const list = await call(api.Targets);
  const body = $("targets-body");
  body.replaceChildren();
  if (!list.length) {
    body.append(el("tr", {}, el("td", { colspan: 9, class: "muted" }, "등록된 대상이 없습니다.")));
    return;
  }
  for (const t of list) {
    const enabled = el("input", { type: "checkbox" });
    enabled.checked = t.enabled;
    enabled.addEventListener("change", async () => {
      try { await call(api.SetTargetEnabled, t.id, enabled.checked); } catch { enabled.checked = !enabled.checked; }
    });
    const edit = el("button", { class: "small", onclick: () => openEdit(t) }, "수정");
    const logs = el("button", { class: "small", onclick: () => {
      $("ent-filter").dataset.want = t.url;
      showTab("entries");
    } }, "로그");
    const remove = el("button", { class: "small danger", onclick: async () => {
      if (!confirm(`대상 #${t.id}을(를) 삭제할까요?\n${t.url}\n(이미 모은 로그와 수집 기록은 남습니다)`)) return;
      await call(api.RemoveTarget, t.id);
      loaders.targets();
    } }, "삭제");
    body.append(el("tr", {},
      el("td", { class: "num" }, String(t.id)),
      el("td", {}, enabled),
      el("td", {}, t.name || "-"),
      el("td", { class: "url" }, t.url),
      el("td", { class: "code" }, t.selector || "(자동)"),
      el("td", { class: "num" }, t.entries.toLocaleString()),
      statusCell(t.lastStatus, t.lastError),
      el("td", { class: "nowrap", title: fmtTime(t.lastCapturedAt) }, fmtShort(t.lastCapturedAt)),
      el("td", { class: "nowrap" }, edit, " ", logs, " ", remove),
    ));
  }
};

$("add-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const t = await call(api.AddTarget, $("add-url").value, $("add-name").value, $("add-selector").value);
  toast(`추가: #${t.id} ${t.url}`);
  $("add-url").value = "";
  $("add-name").value = "";
  $("add-selector").value = "";
  loaders.targets();
});

let editing = null;
function openEdit(t) {
  editing = t;
  $("edit-title").textContent = `대상 #${t.id} 수정`;
  $("edit-url").value = t.url;
  $("edit-name").value = t.name;
  $("edit-selector").value = t.selector;
  $("edit-dialog").showModal();
}
$("edit-cancel").addEventListener("click", () => $("edit-dialog").close());
$("edit-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await call(api.UpdateTarget, editing.id, $("edit-name").value, $("edit-selector").value);
  } catch {
    return;
  }
  $("edit-dialog").close();
  toast("저장했습니다. 다음 수집부터 적용됩니다.");
  loaders.targets();
});

// ---- 로그 ----

const entryQuery = () => [$("ent-filter").value, $("ent-from").value, $("ent-to").value];

loaders.entries = async () => {
  await fillTargetFilter($("ent-filter"));
  const list = await call(api.Entries, ...entryQuery(), Number($("ent-limit").value));
  // 대상 하나를 고르면 그 화면의 머리글로 열을 나누고, 섞여 있으면 한 칸에 이어 붙인다.
  const single = $("ent-filter").value !== "";
  let headers = [];
  if (single) for (const e of list) if ((e.headers?.length || 0) > headers.length) headers = e.headers;
  const width = single ? Math.max(headers.length, ...list.map((e) => e.cells.length), 0) : 0;

  const fixed = ["ID", "처음 확인", "마지막 확인", "전송"];
  const cols = single
    ? Array.from({ length: width }, (_, i) => headers[i] || `열${i + 1}`)
    : ["대상", "내용"];
  $("ent-head").replaceChildren(el("tr", {}, ...[...fixed, ...cols].map((h) => el("th", {}, h))));

  $("ent-count").textContent = list.length ? `최근 ${list.length.toLocaleString()}행` : "";
  const body = $("ent-body");
  body.replaceChildren();
  if (!list.length) {
    body.append(el("tr", {}, el("td", { colspan: fixed.length + cols.length, class: "muted" }, "조건에 맞는 로그가 없습니다.")));
    return;
  }
  for (const e of list) {
    const synced = e.syncedAt ? "완료" : e.syncError ? "거부" : "대기";
    const tds = [
      el("td", { class: "num" }, String(e.id)),
      el("td", { class: "nowrap", title: fmtTime(e.firstSeenAt) }, fmtShort(e.firstSeenAt)),
      el("td", { class: "nowrap", title: fmtTime(e.lastSeenAt) }, fmtShort(e.lastSeenAt)),
      el("td", { class: "nowrap" + (e.syncError ? " status-error" : ""), title: e.syncError || fmtTime(e.syncedAt) }, synced),
    ];
    if (single) {
      for (let i = 0; i < width; i++) tds.push(el("td", { class: "cell" }, e.cells[i] ?? ""));
    } else {
      tds.push(el("td", { class: "nowrap", title: e.targetURL }, e.targetName || e.targetURL),
        el("td", { class: "cell" }, e.cells.join(" | ")));
    }
    body.append(el("tr", {}, ...tds));
  }
};

$("ent-refresh").addEventListener("click", () => loaders.entries());
$("ent-filter").addEventListener("change", () => loaders.entries());
$("ent-limit").addEventListener("change", () => loaders.entries());
$("ent-export").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const msg = await call(api.ExportCSV, ...entryQuery());
    if (msg) toast(msg);
  } finally {
    e.target.disabled = false;
  }
});

// ---- 수집 기록 ----

loaders.captures = async () => {
  await fillTargetFilter($("cap-filter"));
  const list = await call(api.Captures, Number($("cap-limit").value), $("cap-filter").value);
  const body = $("cap-body");
  body.replaceChildren();
  if (!list.length) {
    body.append(el("tr", {}, el("td", { colspan: 7, class: "muted" }, "수집 기록이 없습니다.")));
    return;
  }
  for (const c of list) {
    body.append(el("tr", {},
      el("td", { class: "num" }, String(c.id)),
      el("td", { class: "nowrap", title: fmtTime(c.capturedAt) }, fmtShort(c.capturedAt)),
      el("td", { class: "nowrap", title: c.targetURL }, c.targetName || c.targetURL),
      statusCell(c.status),
      el("td", { class: "num" }, String(c.rowCount)),
      el("td", { class: "num" }, String(c.newRows)),
      el("td", { class: "url", title: c.finalURL }, c.error || c.title || c.finalURL || "-"),
    ));
  }
};

$("cap-refresh").addEventListener("click", () => loaders.captures());
$("cap-filter").addEventListener("change", () => loaders.captures());
$("cap-limit").addEventListener("change", () => loaders.captures());

// ---- 설정 ----

loaders.settings = async () => {
  const o = await call(api.Overview);
  $("set-interval").value = o.settings.intervalSeconds;
  $("set-delay").value = o.settings.loadDelaySeconds;
  const v = await call(api.APIShow);
  $("api-url").value = v.baseURL || "";
  $("api-token").value = "";
  $("api-token").placeholder = v.configured ? `저장된 토큰 ${v.maskedToken} (바꿀 때만 입력)` : "Bearer 토큰";
  $("host-detail").textContent = o.hostInstalled
    ? `등록됨: ${o.hostDetail} · 확장 ID ${o.extensionID}`
    : `등록 안 됨 (${o.hostDetail}). ActivityLogKeeper.exe와 같은 폴더의 activitylogkeeper-cli.exe를 등록합니다.`;
};

$("settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await call(api.SaveSettings, Number($("set-interval").value), Number($("set-delay").value));
  toast("저장했습니다. 확장은 다음 라운드부터 새 주기를 적용합니다.");
});

$("api-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await call(api.APISave, $("api-url").value, $("api-token").value);
  loaders.settings();
  toast("Web API 설정을 저장했습니다.");
});

$("api-test").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    toast(await call(api.APITest, $("api-url").value, $("api-token").value));
  } finally {
    e.target.disabled = false;
  }
});

$("api-clear").addEventListener("click", async () => {
  if (!confirm("Web API 설정을 삭제할까요? (로컬 로그는 그대로 남습니다)")) return;
  await call(api.APIClear);
  loaders.settings();
  toast("Web API 설정을 삭제했습니다.");
});

$("host-install").addEventListener("click", async () => {
  toast(await call(api.InstallHost));
  loaders.settings();
});

// ---- 시작 ----

waitForAPI().then((a) => {
  api = a;
  loaders.overview();
  // 확장이 백그라운드에서 수집하므로 개요는 주기적으로 갱신한다.
  setInterval(() => {
    if ($("tab-overview").classList.contains("active")) loaders.overview().catch(() => {});
  }, 15000);
});
