// NaverBookingScraper 관리 창. Go 바인딩은 window.go.main.App.* (Wails가 런타임에 만든다).

const $ = (id) => document.getElementById(id);
let api;
let state = {};

function waitForAPI() {
  return new Promise((resolve) => {
    const check = () => (window.go?.main?.App ? resolve(window.go.main.App) : setTimeout(check, 30));
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

// 서버 시각(UTC)을 서울 시각으로
function seoul(iso, withDate = false) {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  const opt = { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false };
  if (withDate) Object.assign(opt, { year: "numeric", month: "2-digit", day: "2-digit" });
  return d.toLocaleString("ko-KR", opt);
}

const STATUS = { reserved: "예약", cancelled: "취소", waiting: "대기", done: "완료" };
const RESULT = { created: "등록", updated: "수정", skipped: "변경 없음", cancelled: "취소", error: "오류" };

function car(v) {
  return [v.plateNo === "NAVERIMPORT" ? "" : v.plateNo, v.brand, v.model].filter(Boolean).join(" ") || "-";
}

// ---- 화면 전환 ----

function showSetup(cancellable) {
  $("main").hidden = true;
  $("setup").hidden = false;
  $("setup-url").value = state.baseURL || "";
  $("setup-url").placeholder = state.defaultBaseURL;
  $("setup-key").value = "";
  $("setup-key").placeholder = state.configured ? `비워 두면 저장된 키(${state.maskedKey}) 유지` : "msvi_…";
  $("setup-hint").textContent = state.encrypted
    ? "키는 이 PC의 Windows 계정으로 암호화해 저장합니다 (다른 사용자는 읽을 수 없음)."
    : "이 OS에서는 키를 암호화하지 않고 본인만 읽을 수 있는 파일로 보관합니다.";
  $("setup-error").hidden = true;
  $("setup-cancel").hidden = !cancellable;
  $(state.configured ? "setup-url" : "setup-key").focus();
}

function showMain() {
  $("setup").hidden = true;
  $("main").hidden = false;
}

function setPartner(conn) {
  const badge = $("partner");
  const p = conn?.partner || state.partner || {};
  badge.textContent = p.partnerName || (p.partnerId ? `업체 ${p.partnerId}` : "업체 미확인");
  badge.classList.toggle("unknown", !p.partnerName);
  document.title = p.partnerName ? `네이버예약 스크랩 — ${p.partnerName}` : "네이버예약 스크랩";
  const banner = $("banner");
  if (conn?.partnerChanged) {
    banner.textContent = `⚠ 인증키의 업체가 바뀌었습니다: ${conn.previousPartner} → ${p.partnerName || p.partnerId}. 맞는 키인지 확인하세요.`;
    banner.hidden = false;
  }
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

// ---- 전송 현황 (로컬 저장소: 전송 완료 / 미전송) ----

const SENT_RESULT = { created: "등록", updated: "수정", skipped: "변경 없음", cancelled: "취소" };

function unsentState(r) {
  if (r.state === "other_partner") return ["다른 업체", "s-other_partner", "긁어 올 때와 다른 업체의 인증키입니다 — 보내지 않음"];
  if (r.state === "rejected") return ["거부", "s-rejected", r.rejectError];
  if (r.lastError) return ["전송 실패", "s-failed", r.lastError + " — 자동 재시도"];
  return [r.everSent ? "변경 대기" : "대기", "s-pending", r.everSent ? "내용이 바뀌어 다시 보내야 함" : "아직 보내지 않음"];
}

function service(r) {
  return r.itemName || "-";
}

loaders.records = async () => {
  let rec;
  try {
    rec = await api.Records();
  } catch (e) {
    toast(String(e?.message || e), true);
    return;
  }
  const c = rec.counts;
  const unsentTotal = c.pending + c.rejected + c.otherPartner;
  $("rec-summary").replaceChildren(
    el("span", { class: "chip ok" }, `전송 완료 ${c.sent}`),
    el("span", { class: "chip" + (unsentTotal ? " bad" : "") }, `미전송 ${unsentTotal}`),
    ...(c.failed ? [el("span", { class: "chip bad" }, `전송 실패 ${c.failed}`)] : []),
    ...(c.rejected ? [el("span", { class: "chip bad" }, `거부 ${c.rejected}`)] : []),
    ...(c.otherPartner ? [el("span", { class: "chip" }, `다른 업체 ${c.otherPartner}`)] : []),
  );
  $("rec-retry").hidden = !c.rejected;
  $("rec-send").disabled = !c.pending;

  $("rec-unsent-box").hidden = !rec.unsent.length;
  $("rec-unsent-count").textContent = `${rec.unsent.length}건`;
  $("rec-unsent").replaceChildren(...rec.unsent.map((r) => {
    const [label, cls, reason] = unsentState(r);
    return el("tr", {},
      el("td", { class: "nowrap " + cls }, label),
      el("td", { class: "nowrap" }, seoul(r.startAt, true)),
      el("td", { class: "nowrap" }, r.bookingID),
      el("td", { class: "nowrap" }, r.customer || "-"),
      el("td", { class: "nowrap" }, service(r)),
      el("td", {}, reason || "-"),
      el("td", { class: "nowrap" }, r.attemptAt ? seoul(r.attemptAt, true) : "-"),
    );
  }));

  $("rec-sent-count").textContent = `${rec.sent.length}건`;
  const sent = $("rec-sent");
  if (!rec.sent.length) {
    sent.replaceChildren(el("tr", {}, el("td", { colspan: 7, class: "muted" }, "아직 전송 완료된 예약이 없습니다.")));
  } else {
    sent.replaceChildren(...rec.sent.map((r) => el("tr", {},
      el("td", { class: "nowrap" }, seoul(r.startAt, true)),
      el("td", { class: "nowrap" }, r.bookingID),
      el("td", { class: "nowrap" }, r.customer || "-"),
      el("td", { class: "nowrap" }, service(r)),
      el("td", { class: "nowrap" }, r.status || "-"),
      el("td", { class: "nowrap r-" + r.sentResult }, SENT_RESULT[r.sentResult] || r.sentResult),
      el("td", { class: "nowrap" }, seoul(r.sentAt, true)),
    )));
  }
};

function uploadMessage(u) {
  let msg = `전송 완료 ${u.sent}건`;
  if (u.rejected) msg += `, 거부 ${u.rejected}건`;
  if (u.remaining) msg += `, 미전송 ${u.remaining}건`;
  if (u.error) msg += `\n전송 실패: ${u.error}`;
  return msg;
}

$("rec-send").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const u = await call(api.SendNow);
    toast(uploadMessage(u), !!u.error);
  } finally {
    loaders.records();
  }
});

$("rec-retry").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const u = await call(api.RetryRejected);
    toast(uploadMessage(u), !!(u.error || u.rejected));
  } finally {
    e.target.disabled = false;
    loaders.records();
  }
});

// ---- 서버 예약 조회 ----

loaders.visits = async () => {
  const date = $("day").value;
  const body = $("day-body");
  body.replaceChildren(el("tr", {}, el("td", { colspan: 7, class: "muted" }, "불러오는 중…")));
  let res;
  try {
    res = await call(api.ListDay, date);
  } catch {
    body.replaceChildren(el("tr", {}, el("td", { colspan: 7, class: "error" }, "불러오지 못했습니다.")));
    return;
  }
  if (res.partnerName) setPartner({ partner: { partnerId: res.partnerId, partnerName: res.partnerName } });
  const visits = (res.visits || []).slice().sort((a, b) => (a.scheduledAt || "").localeCompare(b.scheduledAt || ""));
  $("day-count").textContent = `${visits.length}건`;
  body.replaceChildren();
  if (!visits.length) {
    body.append(el("tr", {}, el("td", { colspan: 7, class: "muted" }, "이날 예약이 없습니다.")));
    return;
  }
  for (const v of visits) {
    body.append(el("tr", {},
      el("td", { class: "nowrap" }, seoul(v.scheduledAt)),
      el("td", { class: "nowrap st-" + v.status }, STATUS[v.status] || v.status),
      el("td", {}, v.name || "-"),
      el("td", { class: "nowrap" }, v.phone || "-"),
      el("td", {}, car(v)),
      el("td", {}, v.requestNote || "-"),
      el("td", { class: "nowrap" }, v.externalBookingId),
    ));
  }
};

function shiftDay(n) {
  const d = new Date($("day").value + "T00:00:00"); // 달력 날짜만 쓰므로 로컬 자정 기준
  d.setDate(d.getDate() + n);
  $("day").value = localISO(d);
  loaders.visits();
}
function localISO(d) {
  const p = (x) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
$("day-prev").addEventListener("click", () => shiftDay(-1));
$("day-next").addEventListener("click", () => shiftDay(1));
$("day-today").addEventListener("click", async () => {
  $("day").value = await api.Today();
  loaders.visits();
});
$("day-refresh").addEventListener("click", () => loaders.visits());
$("day").addEventListener("change", () => loaders.visits());

// ---- 예약 찾기 ----

$("find-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const id = $("find-id").value.trim();
  const out = $("find-result");
  out.replaceChildren(el("p", { class: "muted" }, "찾는 중…"));
  const r = await call(api.FindVisit, id).catch(() => null);
  if (!r) return out.replaceChildren();
  if (!r.found) return out.replaceChildren(el("p", {}, `서버에 예약 ${id}이(가) 없습니다.`));
  const v = r.result.visit;
  const dl = el("dl", { class: "info" });
  const row = (k, val) => dl.append(el("dt", {}, k), el("dd", {}, val ?? "-"));
  row("예약번호", v.externalBookingId);
  row("상태", STATUS[v.status] || v.status);
  row("일시", seoul(v.scheduledAt, true));
  row("이름", v.name || "-");
  row("전화", v.phone || "-");
  row("차량", car(v));
  row("연식 / 주행", `${v.year ?? "-"} / ${v.mileage ? v.mileage.toLocaleString() + "km" : "-"}`);
  row("요청", v.requestNote || "-");
  row("출처", v.source || "-");
  row("업체", r.result.partnerName || "-");
  out.replaceChildren(el("div", { class: "detail" }, dl));
});

// ---- 업로드 ----

let upPath = "";
$("up-pick").addEventListener("click", async () => {
  const path = await call(api.PickFile);
  if (!path) return;
  upPath = path;
  $("up-file").textContent = path;
  $("up-dry").disabled = false;
  $("up-real").disabled = false;
  $("up-result").replaceChildren();
});

function renderUpload(r) {
  const res = r.result;
  const head = res.dryRun
    ? `[미리보기] ${r.partner.partnerName || "업체"}에 아직 쓰지 않았습니다. 실제로 올리면:`
    : `${r.partner.partnerName || "업체"}(으)로 올렸습니다.`;
  const chips = el("div", { class: "summary" },
    el("span", { class: "chip r-created" }, `등록 ${res.created}`),
    el("span", { class: "chip r-updated" }, `수정 ${res.updated}`),
    el("span", { class: "chip r-skipped" }, `변경 없음 ${res.skipped}`),
    el("span", { class: "chip r-cancelled" }, `취소 ${res.cancelled}`),
    el("span", { class: "chip r-error" }, `오류 ${(res.errors || []).length}`),
  );
  const body = el("tbody", {});
  for (const it of res.results || []) {
    body.append(el("tr", {},
      el("td", { class: "nowrap" }, it.externalBookingId || "(예약번호 없음)"),
      el("td", { class: "nowrap r-" + it.result }, RESULT[it.result] || it.result),
      el("td", {}, it.error || ""),
    ));
  }
  $("up-result").replaceChildren(
    el("p", {}, el("b", {}, head), ` (${r.file}, ${r.count}건)`), chips,
    el("table", {}, el("thead", {}, el("tr", {}, el("th", {}, "예약번호"), el("th", {}, "결과"), el("th", {}, "사유"))), body),
  );
}

$("up-dry").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    renderUpload(await call(api.Preview, upPath));
  } finally {
    e.target.disabled = false;
  }
});

$("up-real").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    const r = await call(api.Collect, upPath);
    const s = r.saved;
    toast(`가져옴: 새 예약 ${s.new}, 변경 ${s.changed}, 그대로 ${s.unchanged}\n` + uploadMessage(r.upload), !!r.upload.error);
    $("up-result").replaceChildren();
    document.querySelector('nav button[data-tab="records"]').click();
  } finally {
    e.target.disabled = false;
  }
});

// ---- 설정 ----

loaders.settings = async () => {
  state = await call(api.State);
  $("set-partner").textContent = state.partner?.partnerName
    ? `${state.partner.partnerName} (ID ${state.partner.partnerId})` : "미확인";
  $("set-url").textContent = state.baseURL;
  $("set-key").textContent = state.maskedKey;
  $("set-enc").textContent = state.encrypted ? "Windows DPAPI 암호화 (이 PC·이 사용자만 읽음)" : "암호화 없음, 본인만 읽을 수 있는 파일";
  $("set-home").textContent = state.home;
  $("set-version").textContent = state.version;
};

$("set-change").addEventListener("click", () => showSetup(true));
$("set-check").addEventListener("click", async () => {
  const c = await call(api.Connect);
  setPartner(c);
  toast(`연결됨: ${c.partner.partnerName || c.partner.partnerId}`);
  loaders.settings();
});
$("set-clear").addEventListener("click", async () => {
  if (!confirm("저장된 인증키와 업체 정보를 삭제할까요? 다시 쓰려면 키를 입력해야 합니다.")) return;
  await call(api.ClearKey);
  state = await api.State();
  showSetup(false);
});

$("setup-cancel").addEventListener("click", () => showMain());
$("setup-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const btn = $("setup-save");
  btn.disabled = true;
  btn.textContent = "확인 중…";
  $("setup-error").hidden = true;
  try {
    const c = await api.SaveKey($("setup-url").value.trim(), $("setup-key").value.trim());
    state = await api.State();
    showMain();
    setPartner(c);
    toast(`저장했습니다. 업체: ${c.partner.partnerName || c.partner.partnerId}`);
    loaders.records();
    startTimers();
  } catch (err) {
    $("setup-error").textContent = String(err?.message || err);
    $("setup-error").hidden = false;
  } finally {
    btn.disabled = false;
    btn.textContent = "확인하고 저장";
  }
});

// ---- 시작: 키가 없으면 설정 화면, 있으면 업체를 확인하고 오늘 예약 ----

waitForAPI().then(async (a) => {
  api = a;
  state = await api.State();
  $("day").value = await api.Today();
  if (!state.configured) {
    showSetup(false);
    return;
  }
  showMain();
  setPartner(null); // 기억해 둔 업체를 먼저 보여 준다
  try {
    setPartner(await api.Connect());
  } catch (e) {
    $("partner").textContent = "연결 실패";
    $("partner").classList.add("unknown");
    $("banner").textContent = `서버에 연결하지 못했습니다: ${e?.message || e}  — 설정 탭에서 주소를 확인하세요.`;
    $("banner").hidden = false;
  }
  loaders.records();
  startTimers();
});

// 전송 현황은 10초마다 다시 읽고 (수집은 Chrome 확장·호스트가 따로 할 수 있다),
// 미전송(대기·전송 실패)이 있으면 1분마다 자동으로 보낸다. 거부는 자동으로 보내지 않는다.
let timersStarted = false;
function startTimers() {
  if (timersStarted) return;
  timersStarted = true;
  setInterval(() => {
    if (!$("main").hidden && $("tab-records").classList.contains("active")) loaders.records();
  }, 10_000);
  setInterval(async () => {
    if ($("main").hidden) return;
    try {
      const rec = await api.Records();
      if (!rec.counts.pending) return;
      await api.SendNow();
      loaders.records();
    } catch {
      /* 서버가 안 되면 다음 주기에 다시 */
    }
  }, 60_000);
}
