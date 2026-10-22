"""사이트별 아이디·비밀번호와 로그인 세션(쿠키) 저장.

아이디·비밀번호는 처음 한 번만 입력하면 credentials.json에, 세션은 sessions/<프로필>.json에 저장한다.
둘 다 Windows에서는 DPAPI로 암호화한다. 값은 로그·화면에 그대로 내보내지 않는다.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

from . import paths, secret


@dataclass
class Credential:
    username: str
    password: str

    def masked_username(self) -> str:
        u = self.username
        return u[:2] + "*" * max(len(u) - 2, 2) if u else ""


def _load_all() -> dict:
    return paths.read_json(paths.credentials_path()) or {}


def get(profile: str) -> Credential | None:
    box = _load_all().get(profile)
    if not box:
        return None
    data = json.loads(secret.unseal(box))
    return Credential(username=data["username"], password=data["password"])


def put(profile: str, cred: Credential) -> None:
    if not cred.username or not cred.password:
        raise ValueError("아이디와 비밀번호를 모두 입력하세요")
    paths.ensure_home()
    all_ = _load_all()
    all_[profile] = secret.seal(json.dumps({"username": cred.username, "password": cred.password}))
    paths.write_private(paths.credentials_path(), json.dumps(all_, ensure_ascii=False, indent=2))


def delete(profile: str) -> bool:
    all_ = _load_all()
    if profile not in all_:
        return False
    del all_[profile]
    paths.write_private(paths.credentials_path(), json.dumps(all_, ensure_ascii=False, indent=2))
    return True


def _session_file(profile: str):
    return paths.sessions_dir() / f"{profile}.json"


def load_session(profile: str) -> dict | None:
    """Playwright storage_state(쿠키·localStorage). 없거나 읽을 수 없으면 None (다시 로그인한다)."""
    box = paths.read_json(_session_file(profile))
    if not box:
        return None
    try:
        return json.loads(secret.unseal(box))
    except (ValueError, json.JSONDecodeError):
        return None


def save_session(profile: str, state: dict) -> None:
    paths.ensure_home()
    paths.write_private(_session_file(profile), json.dumps(secret.seal(json.dumps(state))))


def clear_session(profile: str) -> bool:
    f = _session_file(profile)
    if f.exists():
        f.unlink()
        return True
    return False
