// PageVault background service worker (MV3)
// 알람마다 Native Messaging 호스트(com.pagevault.host)를 실행해 설정을 받고,
// 최소화된 창에서 대상 URL을 하나씩 열어 스냅샷을 추출해 저장한 뒤 동기화를 요청한다.

const HOST = "com.pagevault.host";
const ALARM = "pagevault-collect";
const MIN_INTERVAL_SECONDS = 30;
const LOAD_TIMEOUT_MS = 60_000;
const ROUND_STALE_MS = 30 * 60_000; // 이보다 오래된 실행 중 표시는 비정상 종료로 본다
const MAX_HTML_CHARS = 30_000_000; // Chrome→호스트 64MiB 한도 안에 들도록 (UTF-8 최대 2바이트 가정)

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
  const sec = Math.max(MIN_INTERVAL_SECONDS, Number(intervalSeconds) || 3600);
  const { alarmSeconds } = await chrome.storage.local.get("alarmSeconds");
  const existing = await chrome.alarms.get(ALARM);
  if (existing && alarmSeconds === sec) return;
  await chrome.alarms.clear(ALARM);
  await chrome.alarms.create(ALARM, { periodInMinutes: sec / 60, delayInMinutes: sec / 60 });
  await chrome.storage.local.set({ alarmSeconds: sec });
}

// 설치·시작 시 호스트에 주기를 물어 알람을 맞춘다. 호스트가 없으면 1시간 기본값.
async function setupAlarm() {
  let port;
  try {
    port = new HostPort();
    const cfg = await port.request("config");
    await ensureAlarm(cfg.intervalSeconds);
  } catch (e) {
    await ensureAlarm(3600);
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
  const result = { at: startedAt, trigger, total: 0, saved: 0, changed: 0, loginRequired: [], errors: [] };
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
          if (r.changed) result.changed++;
          if (r.status === "login_required") result.loginRequired.push(t.name || t.url);
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
    text: "",
    html: "",
    error: "",
  };
  try {
    await navigate(tabId, target.url);
    if (loadDelaySeconds > 0) await sleep(loadDelaySeconds * 1000);
    const [injection] = await chrome.scripting.executeScript({
      target: { tabId },
      func: () => ({
        title: document.title,
        href: location.href,
        text: document.body ? document.body.innerText : "",
        html: document.documentElement ? document.documentElement.outerHTML : "",
      }),
    });
    const r = injection?.result;
    if (!r) throw new Error("페이지 내용을 읽지 못했습니다");
    capture.finalURL = r.href;
    capture.title = r.title;
    capture.text = r.text;
    capture.html = r.html.length > MAX_HTML_CHARS ? "" : r.html;
  } catch (e) {
    capture.error = e.message || String(e);
    try {
      const tab = await chrome.tabs.get(tabId);
      capture.finalURL = tab.url || "";
    } catch (_) {}
  }
  return capture;
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
