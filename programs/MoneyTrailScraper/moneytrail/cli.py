"""moneytrail CLI.

  moneytrail profile add <파일.json> | list | show <이름>
  moneytrail login set <프로필> | test <프로필> [--show] | clear <프로필>
  moneytrail run <프로필> [--page 이름] [--show] [--no-sync]
  moneytrail entries [<프로필>] [--page 이름] [-n 20]
  moneytrail export <프로필> <파일.csv> [--page 이름]
  moneytrail api set <https://…> [토큰] | show | test | clear
  moneytrail sync [--retry-rejected]
  moneytrail status
"""

from __future__ import annotations

import argparse
import csv
import getpass
import sys
from pathlib import Path

from . import VERSION, api, credentials, paths, secret
from .profile import ProfileError, install, list_installed, load
from .scraper import LoginError, NeedCredentials, Scraper
from .store import Store


def _store() -> Store:
    paths.ensure_home()
    return Store(paths.db_path())


def _ask_credential(profile_name: str, title: str) -> credentials.Credential:
    if not sys.stdin.isatty():
        raise SystemExit("아이디·비밀번호는 터미널에서 직접 입력해야 합니다")
    print(f"[{title}] 로그인 정보를 입력하세요. 이 PC에 {'DPAPI로 암호화해' if secret.AVAILABLE else '(개발용: 평문, 파일 권한 0600)'} 저장합니다.")
    user = input("아이디: ").strip()
    pw = getpass.getpass("비밀번호: ")
    cred = credentials.Credential(user, pw)
    credentials.put(profile_name, cred)
    return cred


# ---- profile ----

def cmd_profile(a) -> int:
    if a.action == "add":
        p = install(Path(a.target))
        print(f"프로필 등록: {p.name} ({p.title}) → {p.source}")
    elif a.action == "list":
        items = list_installed()
        if not items:
            print(f"등록된 프로필이 없습니다 ({paths.profiles_dir()})")
        for p in items:
            if isinstance(p, ProfileError):
                print(f"  ! {p}")
            else:
                cred = "로그인 정보 있음" if credentials.get(p.name) else "로그인 정보 없음"
                print(f"  {p.name:<20} {p.title}  페이지 {len(p.pages)}개, {cred}")
    elif a.action == "show":
        if not a.target:
            raise SystemExit("프로필 이름을 주세요")
        p = load(a.target)
        print(f"{p.name} ({p.title})\n  로그인: {p.login.url}")
        for pg in p.pages:
            cols = ", ".join(pg.columns) if pg.columns else "(표 머리글 그대로)"
            print(f"  - {pg.name}: {pg.url}\n      행 {pg.rows}  열 {cols}  최대 {pg.max_pages}쪽")
    return 0


# ---- login ----

def cmd_login(a) -> int:
    prof = load(a.profile)
    if a.action == "set":
        _ask_credential(prof.name, prof.title)
        credentials.clear_session(prof.name)
        print("저장했습니다. 확인하려면: moneytrail login test", prof.name)
    elif a.action == "clear":
        removed = credentials.delete(prof.name)
        credentials.clear_session(prof.name)
        print("지웠습니다" if removed else "저장된 로그인 정보가 없습니다")
    elif a.action == "test":
        cred = credentials.get(prof.name) or _ask_credential(prof.name, prof.title)
        Scraper(prof, show=a.show, cred=cred).check_login()
        print(f"로그인 성공 ({cred.masked_username()}). 세션을 저장했습니다.")
    return 0


# ---- run ----

def cmd_run(a) -> int:
    prof = load(a.profile)
    cred = credentials.get(prof.name)
    if cred is None:
        cred = _ask_credential(prof.name, prof.title)  # 최초 실행: 한 번만 입력
    st = _store()
    run_id = st.start_run(prof.name)
    seen = new = 0
    try:
        result = Scraper(prof, show=a.show, cred=cred).run(only=a.page)
    except (LoginError, NeedCredentials) as e:
        st.finish_run(run_id, "login_failed", 0, 0, str(e))
        print(f"✗ {e}", file=sys.stderr)
        return 2
    except Exception as e:
        st.finish_run(run_id, "error", 0, 0, str(e))
        raise
    failed = 0
    if result.logged_in:
        print("로그인했습니다 (세션 저장).")
    for pr in result.pages:
        if pr.error:
            failed += 1
            print(f"  ✗ {pr.page.name}: {pr.error}")
            continue
        s, n = st.save_rows(prof.name, pr.page.name, pr.url, pr.headers, pr.rows)
        seen += s
        new += n
        print(f"  ✓ {pr.page.name}: {s}행 ({pr.pages_read}쪽), 새 행 {n}")
    st.finish_run(run_id, "partial" if failed else "ok", seen, new, f"{failed}개 페이지 실패" if failed else "")
    if not a.no_sync and (cfg := api.load_config()):
        _do_sync(st, cfg)
    st.close()
    return 1 if failed else 0


# ---- entries / export ----

def cmd_entries(a) -> int:
    st = _store()
    rows = st.entries(a.profile, a.page, a.n)
    if not rows:
        print("저장된 행이 없습니다")
    for e in reversed(rows):
        mark = "↑" if e.synced_at else ("!" if e.sync_error else " ")
        print(f"{mark} {e.first_seen_at[:19]} [{e.profile}/{e.page_name}] " + " | ".join(e.cells))
    return 0


def _write_csv(out: Path, rows: list) -> None:
    with out.open("w", encoding="utf-8-sig", newline="") as f:  # 엑셀에서 한글이 깨지지 않도록 BOM
        w = csv.writer(f)
        width = max(len(e.cells) for e in rows)
        headers = next((e.headers for e in reversed(rows) if e.headers), [])
        headers = headers + [f"열{i + 1}" for i in range(len(headers), width)]
        w.writerow(["처음 본 시각(UTC)", *headers])
        for e in rows:
            w.writerow([e.first_seen_at, *e.cells])
    print(f"{len(rows)}행 → {out}")


def cmd_export(a) -> int:
    """페이지마다 열 구성이 다르므로 페이지 하나당 파일 하나 (여러 페이지면 파일명_페이지.csv)."""
    st = _store()
    rows = list(reversed(st.entries(a.profile, a.page)))
    if not rows:
        raise SystemExit("내보낼 행이 없습니다")
    by_page: dict[str, list] = {}
    for e in rows:
        by_page.setdefault(e.page_name, []).append(e)
    out = Path(a.file)
    if len(by_page) == 1:
        _write_csv(out, rows)
    else:
        for name, group in by_page.items():
            _write_csv(out.with_name(f"{out.stem}_{name}{out.suffix or '.csv'}"), group)
    return 0


# ---- api / sync ----

def _do_sync(st: Store, cfg: api.Config) -> bool:
    try:
        r = api.sync(st, cfg)
    except api.APIError as e:
        hint = " (다음 실행에 다시 보냅니다)" if e.retryable else " (API 설정을 확인하세요)"
        print(f"업로드 실패: {e}{hint}", file=sys.stderr)
        return False
    print(f"업로드: {r.sent}건 보냄, 거부 {r.rejected}건, 남음 {r.remaining}건")
    return True


def cmd_api(a) -> int:
    if a.action == "set":
        if not a.url:
            raise SystemExit("API 주소를 주세요")
        token = a.token or getpass.getpass("토큰: ").strip()
        api.save_config(api.Config(a.url, token))
        print("저장했습니다")
    elif a.action == "show":
        c = api.load_config()
        print(f"{c.base_url}  토큰 {c.masked_token()}" if c else "API 설정이 없습니다")
    elif a.action == "test":
        c = api.load_config()
        if not c:
            raise SystemExit("API 설정이 없습니다")
        print("접속 성공:", api.health(c))
    elif a.action == "clear":
        api.clear_config()
        print("지웠습니다")
    return 0


def cmd_sync(a) -> int:
    c = api.load_config()
    if not c:
        raise SystemExit("API 설정이 없습니다: moneytrail api set <주소>")
    st = _store()
    if a.retry_rejected:
        print(f"거부된 {st.retry_rejected()}건을 다시 보냅니다")
    return 0 if _do_sync(st, c) else 1


def cmd_status(a) -> int:
    st = _store()
    c = st.counts()
    cfg = api.load_config()
    print(f"MoneyTrailScraper {VERSION}  데이터: {paths.home()}")
    print(f"비밀값 보호: {'DPAPI' if secret.AVAILABLE else '없음 (개발용, 파일 권한 0600)'}")
    print(f"행: 전체 {c['total']}, 업로드 대기 {c['pending']}, 거부 {c['rejected']}")
    print(f"Web API: {cfg.base_url if cfg else '설정 안 됨'}")
    runs = st.last_runs(5)
    if runs:
        print("최근 실행:")
        for r in runs:
            err = f"  {r['error']}" if r["error"] else ""
            print(f"  {r['started_at'][:19]} {r['profile']:<16} {r['status']:<12} 본 행 {r['rows_seen']}, 새 행 {r['rows_new']}{err}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog="moneytrail", description="로그인해서 정산·머니 내역을 긁어 오는 스크래퍼")
    ap.add_argument("--version", action="version", version=VERSION)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("profile", help="사이트 프로필 관리")
    p.add_argument("action", choices=["add", "list", "show"])
    p.add_argument("target", nargs="?")
    p.set_defaults(fn=cmd_profile)

    p = sub.add_parser("login", help="아이디·비밀번호 저장·확인")
    p.add_argument("action", choices=["set", "test", "clear"])
    p.add_argument("profile")
    p.add_argument("--show", action="store_true", help="브라우저 창을 띄운다 (2단계 인증 등을 직접 처리)")
    p.set_defaults(fn=cmd_login)

    p = sub.add_parser("run", help="로그인해서 내역을 긁어 온다")
    p.add_argument("profile")
    p.add_argument("--page", help="이 페이지만")
    p.add_argument("--show", action="store_true", help="브라우저 창을 띄운다")
    p.add_argument("--no-sync", action="store_true", help="Web API 업로드를 건너뛴다")
    p.set_defaults(fn=cmd_run)

    p = sub.add_parser("entries", help="저장된 행 보기")
    p.add_argument("profile", nargs="?")
    p.add_argument("--page")
    p.add_argument("-n", type=int, default=20)
    p.set_defaults(fn=cmd_entries)

    p = sub.add_parser("export", help="CSV로 내보내기")
    p.add_argument("profile")
    p.add_argument("file")
    p.add_argument("--page")
    p.set_defaults(fn=cmd_export)

    p = sub.add_parser("api", help="Web API 설정")
    p.add_argument("action", choices=["set", "show", "test", "clear"])
    p.add_argument("url", nargs="?")
    p.add_argument("token", nargs="?")
    p.set_defaults(fn=cmd_api)

    p = sub.add_parser("sync", help="미업로드 행을 Web API로 보낸다")
    p.add_argument("--retry-rejected", action="store_true")
    p.set_defaults(fn=cmd_sync)

    p = sub.add_parser("status", help="상태")
    p.set_defaults(fn=cmd_status)
    return ap


def main(argv: list[str] | None = None) -> int:
    a = build_parser().parse_args(argv)
    try:
        return a.fn(a)
    except (ProfileError, ValueError, api.APIError) as e:
        print(f"오류: {e}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
