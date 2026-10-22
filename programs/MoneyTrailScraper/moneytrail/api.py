"""Web API 업로드 (docs/web-api.md 공통 배치 계약). DB에 직접 연결하지 않는다.

api.json 형식은 shared/apiclient(Go)와 같다: {"baseURL", "token_dpapi"(Windows) | "token"(그 밖)}.
"""

from __future__ import annotations

import json
import socket
import urllib.error
import urllib.request
from dataclasses import dataclass
from urllib.parse import urlsplit

from . import APP_NAME, VERSION, paths, secret
from .store import Store

BATCH_PATH = "/api/v1/moneytrail/entries:batch"
HEALTH_PATH = "/api/v1/health"
BATCH_SIZE = 100
TIMEOUT = 30
_DEV_HOSTS = {"localhost", "127.0.0.1", "::1"}


class APIError(RuntimeError):
    def __init__(self, status: int, message: str):
        super().__init__(f"API 오류 {status}: {message}" if status else message)
        self.status = status

    @property
    def retryable(self) -> bool:
        return self.status in (0, 429) or self.status >= 500


@dataclass
class Config:
    base_url: str
    token: str

    def validate(self) -> None:
        u = urlsplit(self.base_url)
        if not u.netloc:
            raise ValueError("API 주소 형식이 아닙니다 (예: https://api.example.com)")
        host = (u.hostname or "").lower()
        if u.scheme != "https" and not (u.scheme == "http" and (host in _DEV_HOSTS or host.endswith(".localhost"))):
            raise ValueError("API 주소는 https만 됩니다 (개발용 localhost만 http 허용)")
        if not self.token:
            raise ValueError("토큰이 비어 있습니다")

    def masked_token(self) -> str:
        return self.token[:4] + "…" if len(self.token) > 4 else "****"


def load_config() -> Config | None:
    f = paths.read_json(paths.api_config_path())
    if not f:
        return None
    token = f.get("token", "")
    if f.get("token_dpapi"):
        token = secret.unseal({"dpapi": f["token_dpapi"]})
    return Config(base_url=f.get("baseURL", ""), token=token)


def save_config(c: Config) -> None:
    c.base_url = c.base_url.rstrip("/")
    c.validate()
    paths.ensure_home()
    f: dict = {"baseURL": c.base_url}
    if secret.AVAILABLE:
        f["token_dpapi"] = secret.protect(c.token)
    else:
        f["token"] = c.token
    paths.write_private(paths.api_config_path(), json.dumps(f, indent=2))


def clear_config() -> None:
    p = paths.api_config_path()
    if p.exists():
        p.unlink()


def _request(c: Config, method: str, path: str, body: dict | None = None) -> dict:
    data = json.dumps(body, ensure_ascii=False).encode("utf-8") if body is not None else None
    req = urllib.request.Request(c.base_url + path, data=data, method=method)
    req.add_header("Authorization", f"Bearer {c.token}")
    req.add_header("Accept", "application/json")
    req.add_header("User-Agent", f"AppWindows-{APP_NAME}/{VERSION}")
    if data is not None:
        req.add_header("Content-Type", "application/json; charset=utf-8")
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as e:
        msg = e.reason
        try:
            msg = json.loads(e.read()).get("error") or msg
        except (ValueError, AttributeError):
            pass
        raise APIError(e.code, str(msg)) from None
    except (urllib.error.URLError, socket.timeout, ConnectionError) as e:
        raise APIError(0, f"접속 실패: {getattr(e, 'reason', e)}") from None
    return json.loads(raw) if raw else {}


def health(c: Config) -> dict:
    return _request(c, "GET", HEALTH_PATH)


def entry_item(e) -> dict:
    return {
        "uid": e.uid,
        "profile": e.profile,
        "pageName": e.page_name,
        "pageURL": e.page_url,
        "headers": e.headers,
        "cells": e.cells,
        "rowHash": e.row_hash,
        "occurrence": e.occurrence,
        "firstSeenAt": e.first_seen_at,
    }


@dataclass
class SyncResult:
    sent: int = 0
    rejected: int = 0
    remaining: int = 0


def sync(store: Store, c: Config) -> SyncResult:
    """미업로드 행을 100건씩 보낸다. 429·5xx·접속 실패면 APIError로 멈추고 남은 행은 다음에 보낸다."""
    res = SyncResult()
    host = socket.gethostname()
    while True:
        batch = store.unsynced(BATCH_SIZE)
        if not batch:
            break
        out = _request(c, "POST", BATCH_PATH, {"sourceHost": host, "items": [entry_item(e) for e in batch]})
        sent = {e.uid for e in batch}
        accepted = [u for u in out.get("accepted") or [] if u in sent]
        rejected = {r["uid"]: r.get("error", "") for r in out.get("rejected") or [] if isinstance(r, dict) and r.get("uid") in sent}
        store.mark_synced(accepted)
        store.mark_rejected(rejected)
        res.sent += len(accepted)
        res.rejected += len(rejected)
        if len(accepted) + len(rejected) < len(batch):
            # 응답에서 빠진 행은 다음 실행에 다시 보낸다 (같은 배치를 무한히 보내지 않도록 여기서 멈춘다).
            break
    res.remaining = store.counts()["pending"]
    return res
