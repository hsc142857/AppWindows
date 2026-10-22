// PageVault 관리 창. Go 바인딩은 window.go.main.API.* (Wails가 런타임에 만든다).

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

const STATUS_LABEL = { ok: "정상", login_required: "로그인 필요", error: "오류", "": "-" };
function statusCell(status) {
  return el("td", { class: "nowrap status-" + status }, STATUS_LABEL[status] ?? status);
}

// ---- 탭 ----

const loaders = {};
document.querySelectorAll("nav button").forEach((b) =>
  b.addEventListener("click", () => {
    document.querySelectorAll("nav button").forEach((x) => x.classList.toggle("active", x === b));
    document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.id === "tab-" + b.dataset.tab));
    loaders[b.dataset.tab]?.();
  })
);

// ---- 개요 ----

loaders.overview = async () => {
  const o = await call(api.Overview);
  $("ov-targets").textContent = `${o.enabled} / ${o.targets}`;
  $("ov-snapshots").textContent = o.snapshots;
  $("ov-unsynced").textContent = o.unsynced;
  $("ov-last").textContent = fmtTime(o.lastCapture);
  $("ov-interval").textContent = `${fmtSeconds(o.settings.intervalSeconds)} (로딩 후 ${o.settings.loadDelaySeconds}초 대기)`;
  $("ov-api").textContent = o.api;
  $("ov-rejected").hidden = !o.rejected;
  $("ov-rejected-text").textContent = `서버가 거부한 스냅샷 ${o.rejected}개는 업로드에서 빠져 있습니다. (사유는 스냅샷 탭)`;
  $("ov-host").textContent = o.hostInstalled ? `등록됨 (${o.hostDetail})` : `등록 안 됨 — 설정 탭에서 등록하세요 (${o.hostDetail})`;
  $("ov-home").textContent = o.home;
  const login = $("ov-login");
  if (o.loginRequired.length) {
    login.replaceChildren(
      el("b", {}, "로그인이 필요한 대상이 있습니다. Chrome에서 다시 로그인하세요."),
      el("ul", {}, ...o.loginRequired.map((t) => el("li", {}, `${t.name || t.url} — ${fmtTime(t.lastSeenAt)}`)))
    );
    login.hidden = false;
  } else {
    login.hidden = true;
  }
};

$("ov-refresh").addEventListener("click", () => loaders.overview());
$("ov-retry").addEventListener("click", async () => {
  const n = await call(api.RetryRejected);
  toast(`거부된 ${n}개를 다음 동기화 때 다시 보냅니다.`);
  loaders.overview();
});
$("ov-sync").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const r = await call(api.SyncNow);
    toast(r.apiConfigured
      ? `동기화 완료: 보냄 ${r.sent}개, 남음 ${r.remaining}개` + (r.rejected ? `, 서버 거부 ${r.rejected}개` : "")
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
    body.append(el("tr", {}, el("td", { colspan: 7, class: "muted" }, "등록된 대상이 없습니다.")));
    return;
  }
  for (const t of list) {
    const enabled = el("input", { type: "checkbox" });
    enabled.checked = t.enabled;
    enabled.addEventListener("change", async () => {
      try { await call(api.SetTargetEnabled, t.id, enabled.checked); } catch { enabled.checked = !enabled.checked; }
    });
    const rename = el("button", { class: "small", onclick: async () => {
      const name = prompt("이름", t.name);
      if (name === null) return;
      await call(api.RenameTarget, t.id, name);
      loaders.targets();
    } }, "이름");
    const remove = el("button", { class: "small danger", onclick: async () => {
      if (!confirm(`대상 #${t.id}을(를) 삭제할까요?\n${t.url}\n(이미 저장된 스냅샷은 남습니다)`)) return;
      await call(api.RemoveTarget, t.id);
      loaders.targets();
    } }, "삭제");
    body.append(el("tr", {},
      el("td", { class: "num" }, String(t.id)),
      el("td", {}, enabled),
      el("td", {}, t.name || "-"),
      el("td", { class: "url" }, t.url),
      statusCell(t.lastStatus),
      el("td", { class: "nowrap", title: fmtTime(t.lastSeenAt) }, fmtShort(t.lastSeenAt)),
      el("td", { class: "nowrap" }, rename, " ", remove),
    ));
  }
};

$("add-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const t = await call(api.AddTarget, $("add-url").value, $("add-name").value);
  toast(`추가: #${t.id} ${t.url}`);
  $("add-url").value = "";
  $("add-name").value = "";
  loaders.targets();
});

// ---- 스냅샷 ----

let selectedSnap = null;
loaders.snapshots = async () => {
  const filter = $("snap-filter");
  const current = filter.value;
  const targets = await call(api.Targets);
  filter.replaceChildren(el("option", { value: "" }, "전체 대상"),
    ...targets.map((t) => el("option", { value: t.url }, t.name || t.url)));
  filter.value = current;

  const list = await call(api.Snapshots, Number($("snap-limit").value), filter.value);
  const body = $("snap-body");
  body.replaceChildren();
  if (!list.length) {
    body.append(el("tr", {}, el("td", { colspan: 6, class: "muted" }, "스냅샷이 없습니다.")));
    return;
  }
  for (const s of list) {
    const tr = el("tr", { onclick: () => showSnapshot(s.id, tr) },
      el("td", { class: "num" }, String(s.id)),
      statusCell(s.status),
      el("td", {}, s.title || s.targetName || s.targetURL),
      el("td", { class: "nowrap", title: fmtTime(s.capturedAt) }, fmtShort(s.capturedAt)),
      el("td", { class: "nowrap", title: fmtTime(s.lastSeenAt) }, fmtShort(s.lastSeenAt)),
      el("td", { class: "nowrap" + (s.syncError ? " status-error" : "") }, s.syncedAt ? "완료" : s.syncError ? "거부" : "대기"),
    );
    if (s.id === selectedSnap) tr.classList.add("selected");
    body.append(tr);
  }
};

async function showSnapshot(id, tr) {
  selectedSnap = id;
  document.querySelectorAll("#snap-body tr").forEach((r) => r.classList.toggle("selected", r === tr));
  const s = await call(api.Snapshot, id);
  const dl = el("dl", {});
  const row = (k, v) => dl.append(el("dt", {}, k), el("dd", {}, v || "-"));
  row("상태", STATUS_LABEL[s.status] ?? s.status);
  row("대상", `${s.targetName ? s.targetName + " — " : ""}${s.targetURL}`);
  row("최종 URL", s.finalURL);
  row("수집", fmtTime(s.capturedAt));
  row("마지막 확인", fmtTime(s.lastSeenAt));
  row("동기화", fmtTime(s.syncedAt));
  if (s.syncError) row("서버 거부", s.syncError);
  row("UID", s.uid);
  if (s.status === "ok") row("HTML", `${s.htmlSize.toLocaleString()}바이트`);
  if (s.error) row("오류", s.error);
  $("snap-detail").replaceChildren(el("h3", {}, s.title || "(제목 없음)"), dl,
    s.status === "ok" ? el("pre", {}, s.text) : "");
}

$("snap-refresh").addEventListener("click", () => loaders.snapshots());
$("snap-filter").addEventListener("change", () => loaders.snapshots());
$("snap-limit").addEventListener("change", () => loaders.snapshots());

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
    : `등록 안 됨 (${o.hostDetail}). PageVault.exe와 같은 폴더의 pagevault-cli.exe를 등록합니다.`;
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
  if (!confirm("Web API 설정을 삭제할까요? (로컬 스냅샷은 그대로 남습니다)")) return;
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
