"""데이터 폴더 위치. 다른 AppWindows 프로그램과 같은 규칙을 따른다."""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

from . import APP_NAME


def home() -> Path:
    """MONEYTRAIL_HOME이 있으면 그 값, Windows는 %LOCALAPPDATA%\\MoneyTrailScraper, 그 밖에는 사용자 설정 폴더 아래."""
    if h := os.environ.get("MONEYTRAIL_HOME"):
        return Path(h)
    if d := os.environ.get("LOCALAPPDATA"):
        return Path(d) / APP_NAME
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Application Support" / APP_NAME
    return Path(os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config") / APP_NAME


def db_path() -> Path:
    return home() / "moneytrail.sqlite"


def api_config_path() -> Path:
    return home() / "api.json"


def credentials_path() -> Path:
    return home() / "credentials.json"


def sessions_dir() -> Path:
    return home() / "sessions"


def profiles_dir() -> Path:
    return home() / "profiles"


def ensure_home() -> None:
    """데이터 폴더를 만든다 (사용자만 접근)."""
    for d in (home(), sessions_dir(), profiles_dir()):
        d.mkdir(parents=True, exist_ok=True)
        if os.name != "nt":
            os.chmod(d, 0o700)


def write_private(path: Path, data: str) -> None:
    """임시 파일에 쓴 뒤 교체한다 (0600)."""
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(data)
    os.replace(tmp, path)


def read_json(path: Path) -> dict | None:
    """파일이 없으면 None."""
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return None
