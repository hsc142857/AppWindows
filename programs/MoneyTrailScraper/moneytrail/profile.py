"""사이트 프로필: 로그인 방법과 긁어올 페이지·표를 JSON으로 정한다. 사이트별 코드는 만들지 않는다."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlsplit

from . import paths

NAME_RE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,39}$")


class ProfileError(ValueError):
    pass


@dataclass
class Login:
    url: str
    username: str  # 아이디 입력칸 선택자
    password: str  # 비밀번호 입력칸 선택자
    submit: str = ""  # 로그인 버튼 선택자 (없으면 비밀번호 칸에서 Enter)
    success: str = ""  # 로그인 성공 후 나타나는 요소 선택자 (없으면 로그인 주소를 벗어나면 성공)
    error: str = ""  # 로그인 실패 메시지 선택자 (있으면 실패 사유로 보여 준다)


@dataclass
class Page:
    name: str
    url: str
    rows: str  # 표의 행 선택자 (예: "table.list tbody tr")
    columns: dict[str, str] = field(default_factory=dict)  # 열 이름 → 행 안의 선택자. 비우면 thead 머리글 + 모든 칸
    wait_for: str = ""  # 이 요소가 보일 때까지 기다린다 (비우면 rows)
    next: str = ""  # 다음 페이지 버튼 선택자
    max_pages: int = 1


@dataclass
class Profile:
    name: str
    title: str
    login: Login
    pages: list[Page]
    source: Path | None = None


def _req(d: dict, key: str, where: str) -> str:
    v = d.get(key)
    if not isinstance(v, str) or not v.strip():
        raise ProfileError(f"{where}.{key}가 비어 있습니다")
    return v.strip()


def _http_url(u: str, where: str) -> str:
    parts = urlsplit(u)
    if parts.scheme not in ("http", "https") or not parts.netloc:
        raise ProfileError(f"{where}: http(s) 주소가 아닙니다: {u}")
    return u


def parse(data: dict, source: Path | None = None) -> Profile:
    name = _req(data, "name", "profile")
    if not NAME_RE.match(name):
        raise ProfileError("name은 영문 소문자·숫자·-·_ 40자 이내여야 합니다")
    lg = data.get("login")
    if not isinstance(lg, dict):
        raise ProfileError("login이 없습니다")
    login = Login(
        url=_http_url(_req(lg, "url", "login"), "login.url"),
        username=_req(lg, "username", "login"),
        password=_req(lg, "password", "login"),
        submit=lg.get("submit", "") or "",
        success=lg.get("success", "") or "",
        error=lg.get("error", "") or "",
    )
    raw_pages = data.get("pages")
    if not isinstance(raw_pages, list) or not raw_pages:
        raise ProfileError("pages가 비어 있습니다")
    pages: list[Page] = []
    seen: set[str] = set()
    for i, p in enumerate(raw_pages):
        where = f"pages[{i}]"
        if not isinstance(p, dict):
            raise ProfileError(f"{where}가 객체가 아닙니다")
        pname = _req(p, "name", where)
        if pname in seen:
            raise ProfileError(f"페이지 이름이 겹칩니다: {pname}")
        seen.add(pname)
        cols = p.get("columns") or {}
        if not isinstance(cols, dict) or not all(isinstance(k, str) and isinstance(v, str) for k, v in cols.items()):
            raise ProfileError(f"{where}.columns는 {{열 이름: 선택자}} 형식이어야 합니다")
        max_pages = p.get("maxPages", 1)
        if not isinstance(max_pages, int) or isinstance(max_pages, bool) or not 1 <= max_pages <= 200:
            raise ProfileError(f"{where}.maxPages는 1~200입니다")
        pages.append(
            Page(
                name=pname,
                url=_http_url(_req(p, "url", where), f"{where}.url"),
                rows=_req(p, "rows", where),
                columns=cols,
                wait_for=p.get("waitFor", "") or "",
                next=p.get("next", "") or "",
                max_pages=max_pages,
            )
        )
    return Profile(name=name, title=data.get("title") or name, login=login, pages=pages, source=source)


def load(name_or_path: str) -> Profile:
    """이름이면 데이터 폴더의 profiles/<이름>.json, .json으로 끝나면 그 파일을 읽는다."""
    path = Path(name_or_path) if name_or_path.endswith(".json") else paths.profiles_dir() / f"{name_or_path}.json"
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        raise ProfileError(f"프로필이 없습니다: {path}") from None
    except json.JSONDecodeError as e:
        raise ProfileError(f"{path.name} JSON 오류: {e}") from None
    return parse(data, path)


def list_installed() -> list[Profile | ProfileError]:
    out: list[Profile | ProfileError] = []
    d = paths.profiles_dir()
    if d.is_dir():
        for f in sorted(d.glob("*.json")):
            try:
                out.append(load(str(f)))
            except ProfileError as e:
                out.append(ProfileError(f"{f.name}: {e}"))
    return out


def install(src: Path) -> Profile:
    """프로필 파일을 검증해 데이터 폴더에 <name>.json으로 복사한다."""
    prof = load(str(src))
    paths.ensure_home()
    dst = paths.profiles_dir() / f"{prof.name}.json"
    dst.write_text(src.read_text(encoding="utf-8"), encoding="utf-8")
    prof.source = dst
    return prof
