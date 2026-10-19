const hostEl = document.getElementById("host");
const btn = document.getElementById("collect");
const resultEl = document.getElementById("result");

function fmt(iso) {
  return iso ? new Date(iso).toLocaleString() : "-";
}

function el(tag, text, cls) {
  const e = document.createElement(tag);
  if (text !== undefined) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}

function render(r) {
  resultEl.replaceChildren();
  if (!r) {
    resultEl.append(el("p", "아직 수집 기록이 없습니다."));
    return;
  }
  const dl = el("dl");
  const row = (k, v, cls) => dl.append(el("dt", k), el("dd", v, cls));
  row("마지막 수집", fmt(r.finishedAt || r.at));
  if (r.error) row("오류", r.error, "bad");
  if (r.total !== undefined) row("대상", `${r.total}개 중 ${r.saved}개 저장, ${r.changed}개 변경`);
  if (r.sync) {
    row("동기화", r.sync.ok === false
      ? `실패: ${r.sync.error}`
      : r.sync.postgresConfigured ? `보냄 ${r.sync.sent}, 남음 ${r.sync.remaining}` : "Postgres 미설정",
      r.sync.ok === false ? "bad" : "");
  }
  resultEl.append(dl);
  const list = (title, items) => {
    if (!items?.length) return;
    resultEl.append(el("p", title, "bad"));
    const ul = el("ul");
    items.forEach((i) => ul.append(el("li", i)));
    resultEl.append(ul);
  };
  list("로그인 필요", r.loginRequired);
  list("오류", r.errors);
}

async function load() {
  const { lastResult } = await chrome.storage.local.get("lastResult");
  render(lastResult);
}

chrome.runtime.sendMessage({ type: "ping" }, (r) => {
  if (r?.ok) {
    hostEl.textContent = `호스트 연결됨 (v${r.version})`;
    hostEl.className = "host ok";
  } else {
    hostEl.textContent = "호스트 연결 실패: " + (r?.error || chrome.runtime.lastError?.message || "알 수 없음") +
      " — pagevault install-host 를 실행했는지 확인하세요.";
    hostEl.className = "host bad";
  }
});

btn.addEventListener("click", () => {
  btn.disabled = true;
  btn.textContent = "수집 중…";
  chrome.runtime.sendMessage({ type: "collectNow" }, (r) => {
    btn.disabled = false;
    btn.textContent = "지금 수집";
    if (r && r.ok === false && r.error && r.total === undefined) {
      render({ at: new Date().toISOString(), error: r.error });
    } else {
      load();
    }
  });
});

chrome.storage.onChanged.addListener((changes) => {
  if (changes.lastResult) render(changes.lastResult.newValue);
});

load();
