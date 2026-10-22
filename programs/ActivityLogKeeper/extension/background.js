// ActivityLogKeeper background service worker (MV3)
// 알람마다 Native Messaging 호스트(com.appwindows.activitylogkeeper)를 실행해 설정을 받고,
// 최소화된 창에서 대상 URL을 하나씩 열어 활동 로그 표의 행을 읽어 저장한 뒤 서버 전송을 요청한다.
// 서버가 없거나 끊겨도 호스트가 먼저 로컬 SQLite에 저장하므로 로그는 남는다.

const HOST = "com.appwindows.activitylogkeeper";
const ALARM = "activitylogkeeper-collect";
const MIN_INTERVAL_SECONDS = 30;
const LOAD_TIMEOUT_MS = 60_000;
const ROUND_STALE_MS = 30 * 60_000; // 이보다 오래된 실행 중 표시는 비정상 종료로 본다
const MAX_ROWS = 5000; // 호스트(core.MaxRows)와 맞춘다
const EMPTY_RETRIES = 3; // 표가 늦게 그려지는 화면(SPA)을 위해 행이 없으면 2초씩 다시 본다

// ---- Native Messaging 포트 (요청 id로 응답을 짝짓는다) ----

class HostPort {
  constructor() {
    this.port = chrome.runtime.connectNative(HOST);
    this.pending = new Map();
    this.nextId = 1;
    this.closedError = null;
    this.port.onMessage.addListener((msg) => {
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      msg.ok ? p.resolve(msg) : p.reject(new Error(msg.error || "호스트 오류"));
    });
    this.port.onDisconnect.addListener(() => {
      const err = chrome.runtime.lastError?.message || "호스트 연결이 끊겼습니다";
      this.closedError = new Error(err);
      for (const p of this.pending.values()) p.reject(this.closedError);
      this.pending.clear();
    });
  }

  request(type, extra = {}) {
    if (this.closedError) return Promise.reject(this.closedError);
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try {
        this.port.postMessage({ id, type, ...extra });
      } catch (e) {
        this.pending.delete(id);
        reject(e);
      }
    });
  }

  close() {
    try { this.port.disconnect(); } catch (_) {}
  }
}

// ---- 알람 ----

async function ensureAlarm(intervalSeconds) {
  const sec = Math.max(MIN_INTERVAL_SECONDS, Number(intervalSeconds) || 600);
  const { alarmSeconds } = await chrome.storage.local.get("alarmSeconds");
  const existing = await chrome.alarms.get(ALARM);
  if (existing && alarmSeconds === sec) return;
  await chrome.alarms.clear(ALARM);
  await chrome.alarms.create(ALARM, { periodInMinutes: sec / 60, delayInMinutes: sec / 60 });
  await chrome.storage.local.set({ alarmSeconds: sec });
}

// 설치·시작 시 호스트에 주기를 물어 알람을 맞춘다. 호스트가 없으면 10분 기본값.
async function setupAlarm() {
  let port;
  try {
    port = new HostPort();
    const cfg = await port.request("config");
    await ensureAlarm(cfg.intervalSeconds);
  } catch (e) {
    await ensureAlarm(600);
    await saveResult({ at: new Date().toISOString(), error: "호스트 연결 실패: " + e.message });
  } finally {
    port?.close();
  }
}

chrome.runtime.onInstalled.addListener(setupAlarm);
chrome.runtime.onStartup.addListener(setupAlarm);
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === ALARM) collectRound("alarm");
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.type === "collectNow") {
    collectRound("manual").then(sendResponse);
    return true;
  }
  if (msg?.type === "ping") {
    const port = new HostPort();
    port.request("ping")
      .then((r) => sendResponse({ ok: true, version: r.version }))
      .catch((e) => sendResponse({ ok: false, error: e.message }))
      .finally(() => port.close());
    return true;
  }
  return false;
});

// ---- 수집 라운드 ----

async function saveResult(result) {
  await chrome.storage.local.set({ lastResult: result });
}

// 서비스 워커가 다시 시작돼도 중복 실행을 막도록 session 저장소에 표시한다.
async function acquireRound() {
  const { roundStartedAt } = await chrome.storage.session.get("roundStartedAt");
  if (roundStartedAt && Date.now() - roundStartedAt < ROUND_STALE_MS) return false;
  await chrome.storage.session.set({ roundStartedAt: Date.now() });
  return true;
}

async function releaseRound() {
  await chrome.storage.session.remove("roundStartedAt");
}

async function collectRound(trigger) {
  if (!(await acquireRound())) {
    return { ok: false, error: "이미 수집 중입니다" };
  }
  const startedAt = new Date().toISOString();
  const result = { at: startedAt, trigger, total: 0, saved: 0, newRows: 0, loginRequired: [], empty: [], errors: [] };
  let port;
  let windowId;
  try {
    port = new HostPort();
    const cfg = await port.request("config");
    await ensureAlarm(cfg.intervalSeconds); // 주기가 바뀌었으면 다음 라운드부터 적용
    const targets = cfg.targets || [];
    result.total = targets.length;
    if (targets.length > 0) {
      const win = await chrome.windows.create({ url: "about:blank", state: "minimized", focused: false });
      windowId = win.id;
      const tabId = win.tabs[0].id;
      for (const t of targets) {
        const capture = await captureTarget(tabId, t, cfg.loadDelaySeconds ?? 3);
        try {
          const r = await port.request("save", { capture });
          result.saved++;
          result.newRows += r.newRows || 0;
          if (r.status === "login_required") result.loginRequired.push(t.name || t.url);
          if (r.status === "empty") result.empty.push(t.name || t.url);
          if (r.status === "error") result.errors.push(`${t.name || t.url}: ${capture.error}`);
        } catch (e) {
          result.errors.push(`${t.name || t.url}: 저장 실패 ${e.message}`);
        }
      }
    }
    try {
      result.sync = await port.request("sync");
    } catch (e) {
      result.sync = { ok: false, error: e.message };
    }
    result.ok = true;
  } catch (e) {
    result.ok = false;
    result.error = e.message;
  } finally {
    if (windowId !== undefined) {
      try { await chrome.windows.remove(windowId); } catch (_) {}
    }
    port?.close();
    result.finishedAt = new Date().toISOString();
    await saveResult(result);
    await releaseRound();
  }
  return result;
}

async function captureTarget(tabId, target, loadDelaySeconds) {
  const capture = {
    targetID: target.id,
    requestedURL: target.url,
    finalURL: "",
    title: "",
    headers: [],
    rows: [],
    error: "",
  };
  try {
    await navigate(tabId, target.url);
    if (loadDelaySeconds > 0) await sleep(loadDelaySeconds * 1000);
    let best;
    for (let i = 0; i <= EMPTY_RETRIES; i++) {
      if (i > 0) await sleep(2000);
      // 옛 업무 프로그램은 로그가 iframe 안에 있기도 해서 모든 프레임에서 읽고 행이 가장 많은 것을 쓴다.
      const injections = await chrome.scripting.executeScript({
        target: { tabId, allFrames: true },
        func: extractLogTable,
        args: [target.selector || ""],
      });
      const top = injections.find((r) => r.frameId === 0)?.result;
      if (!top) throw new Error("페이지 내용을 읽지 못했습니다");
      best = injections.map((r) => r.result).filter(Boolean)
        .reduce((a, b) => (b.rows.length > a.rows.length ? b : a), { headers: [], rows: [] });
      capture.finalURL = top.href;
      capture.title = top.title;
      if (best.rows.length > 0) break;
    }
    capture.headers = best.headers;
    capture.rows = best.rows.slice(0, MAX_ROWS);
  } catch (e) {
    capture.error = e.message || String(e);
    try {
      const tab = await chrome.tabs.get(tabId);
      capture.finalURL = tab.url || "";
    } catch (_) {}
  }
  return capture;
}

// extractLogTable은 페이지(각 프레임) 안에서 실행된다. 이 함수 밖의 변수를 쓰면 안 된다.
// selector가 비어 있으면 행이 가장 많은 <table> 또는 ARIA 표(role=grid/table)를 고른다.
// selector가 요소 하나에 맞으면 그 표(표가 아니면 자식 요소를 행으로), 여러 개에 맞으면 각 요소를 행으로 읽는다.
function extractLogTable(selector) {
  const text = (el) => (el.innerText ?? el.textContent ?? "").replace(/\s+/g, " ").trim();
  const isGrid = (el) => el.matches('[role="grid"], [role="table"], [role="treegrid"]');

  function fromTable(table) {
    let headers = [...table.querySelectorAll(":scope > thead > tr:last-child > th, :scope > thead > tr:last-child > td")].map(text);
    const rows = [];
    for (const tr of table.querySelectorAll(":scope > tbody > tr, :scope > tr")) {
      const cells = [...tr.children].filter((c) => c.tagName === "TD" || c.tagName === "TH");
      if (cells.length === 0) continue;
      if (headers.length === 0 && rows.length === 0 && cells.every((c) => c.tagName === "TH")) {
        headers = cells.map(text);
        continue;
      }
      rows.push(cells.map(text));
    }
    return { headers, rows };
  }

  function fromGrid(grid) {
    let headers = [];
    const rows = [];
    for (const r of grid.querySelectorAll('[role="row"]')) {
      const cells = [...r.querySelectorAll('[role="cell"], [role="gridcell"], [role="columnheader"], [role="rowheader"]')];
      if (cells.length === 0) continue;
      if (cells.every((c) => c.getAttribute("role") === "columnheader")) {
        if (headers.length === 0) headers = cells.map(text);
        continue;
      }
      rows.push(cells.map(text));
    }
    return { headers, rows };
  }

  const fromRows = (els) => ({
    headers: [],
    rows: els.map((el) => (el.children.length > 0 ? [...el.children].map(text) : [text(el)])),
  });

  function parse(el) {
    if (el.tagName === "TABLE") return fromTable(el);
    if (isGrid(el)) return fromGrid(el);
    const inner = el.querySelector("table");
    if (inner) return fromTable(inner);
    return fromRows([...el.children]);
  }

  let out = { headers: [], rows: [] };
  if (selector) {
    let els = [];
    try {
      els = [...document.querySelectorAll(selector)];
    } catch (_) {
      els = [];
    }
    if (els.length === 1) out = parse(els[0]);
    else if (els.length > 1) out = fromRows(els);
  } else {
    for (const el of document.querySelectorAll('table, [role="grid"], [role="table"], [role="treegrid"]')) {
      const r = el.tagName === "TABLE" ? fromTable(el) : fromGrid(el);
      if (r.rows.length > out.rows.length) out = r;
    }
  }
  out.rows = out.rows.filter((r) => r.some((c) => c !== ""));
  return { title: document.title, href: location.href, headers: out.headers, rows: out.rows };
}

// 탭을 url로 이동하고 status complete가 될 때까지 기다린다 (60초 타임아웃).
function navigate(tabId, url) {
  return new Promise((resolve, reject) => {
    let started = false;
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error("페이지 로딩 시간 초과 (60초)"));
    }, LOAD_TIMEOUT_MS);
    const onUpdated = (id, info) => {
      if (id !== tabId) return;
      if (info.status === "loading") started = true;
      if (info.status === "complete" && started) {
        cleanup();
        resolve();
      }
    };
    const onRemoved = (id) => {
      if (id !== tabId) return;
      cleanup();
      reject(new Error("수집 탭이 닫혔습니다"));
    };
    function cleanup() {
      clearTimeout(timer);
      chrome.tabs.onUpdated.removeListener(onUpdated);
      chrome.tabs.onRemoved.removeListener(onRemoved);
    }
    chrome.tabs.onUpdated.addListener(onUpdated);
    chrome.tabs.onRemoved.addListener(onRemoved);
    chrome.tabs.update(tabId, { url }).catch((e) => {
      cleanup();
      reject(e);
    });
  });
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}
