"""Playwright로 백그라운드(헤드리스) 브라우저를 띄워 로그인하고 프로필의 표를 긁어 온다.

흐름: 저장된 세션으로 페이지 열기 → 로그인 화면이면 저장된 아이디·비밀번호로 로그인 → 다시 열기
→ 표 행 추출(다음 페이지 버튼이 있으면 maxPages까지) → 세션 저장.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from urllib.parse import urlsplit

from playwright.sync_api import Error as PlaywrightError
from playwright.sync_api import Page as PWPage
from playwright.sync_api import TimeoutError as PWTimeout
from playwright.sync_api import sync_playwright

from . import credentials
from .profile import Page, Profile

NAV_TIMEOUT_MS = 30_000
LOGIN_TIMEOUT_MS = 30_000
MANUAL_LOGIN_TIMEOUT_MS = 180_000  # --show: 사용자가 추가 인증(2단계·캡차)을 직접 끝낼 시간

_WS = re.compile(r"\s+")

# 행 추출 (브라우저 안에서 실행)
_EXTRACT_JS = """
(a) => {
  const text = (el) => (el ? (el.innerText || el.textContent || '') : '').trim();
  const rows = Array.from(document.querySelectorAll(a.rows));
  const keys = Object.keys(a.columns);
  let headers = keys;
  if (!keys.length) {
    const table = rows.length ? rows[0].closest('table') : null;
    headers = table ? Array.from(table.querySelectorAll('thead th')).map(text) : [];
  }
  const out = rows.map((r) => keys.length
    ? keys.map((k) => text(a.columns[k] ? r.querySelector(a.columns[k]) : r))
    : Array.from(r.querySelectorAll('td, th')).map(text));
  return { headers, rows: out };
}
"""

# 다음 페이지로 넘어갔는지 확인할 서명 (행 내용)
_SIGNATURE_JS = "(sel) => Array.from(document.querySelectorAll(sel)).map(r => r.innerText).join('\\u001e')"


class LoginError(RuntimeError):
    """아이디·비밀번호가 틀렸거나 추가 인증이 필요해 로그인하지 못했다."""


class NeedCredentials(RuntimeError):
    """저장된 아이디·비밀번호가 없다."""


@dataclass
class PageResult:
    page: Page
    url: str = ""
    headers: list[str] = field(default_factory=list)
    rows: list[list[str]] = field(default_factory=list)
    pages_read: int = 0
    error: str = ""


@dataclass
class ScrapeResult:
    logged_in: bool = False  # 이번 실행에서 새로 로그인했는가 (False면 저장된 세션 재사용)
    pages: list[PageResult] = field(default_factory=list)


def clean(s: str) -> str:
    return _WS.sub(" ", s).strip()


def _same_page(a: str, b: str) -> bool:
    """호스트·경로가 같으면 같은 화면 (쿼리·# 무시, 끝 슬래시 무시)."""
    pa, pb = urlsplit(a), urlsplit(b)
    return pa.netloc.lower() == pb.netloc.lower() and pa.path.rstrip("/") == pb.path.rstrip("/")


class Scraper:
    def __init__(self, prof: Profile, *, show: bool = False, cred: credentials.Credential | None = None):
        self.prof = prof
        self.show = show
        self._cred = cred

    # ---- 로그인 ----

    def _credential(self) -> credentials.Credential:
        if self._cred is None:
            self._cred = credentials.get(self.prof.name)
        if self._cred is None:
            raise NeedCredentials(f"'{self.prof.name}' 아이디·비밀번호가 없습니다. 먼저 `moneytrail login set {self.prof.name}`")
        return self._cred

    def _on_login_page(self, page: PWPage) -> bool:
        lg = self.prof.login
        if _same_page(page.url, lg.url):
            return True
        try:
            return page.locator(lg.password).first.is_visible()
        except PlaywrightError:
            return False

    def _login(self, page: PWPage) -> None:
        lg = self.prof.login
        cred = self._credential()
        if not _same_page(page.url, lg.url):
            page.goto(lg.url, wait_until="domcontentloaded", timeout=NAV_TIMEOUT_MS)
        page.locator(lg.username).first.wait_for(state="visible", timeout=LOGIN_TIMEOUT_MS)
        page.locator(lg.username).first.fill(cred.username)
        page.locator(lg.password).first.fill(cred.password)
        if lg.submit:
            page.locator(lg.submit).first.click()
        else:
            page.locator(lg.password).first.press("Enter")

        timeout = MANUAL_LOGIN_TIMEOUT_MS if self.show else LOGIN_TIMEOUT_MS
        try:
            if lg.success:
                page.locator(lg.success).first.wait_for(state="visible", timeout=timeout)
            else:
                page.wait_for_url(lambda u: not _same_page(u, lg.url), timeout=timeout)
        except PWTimeout:
            msg = ""
            if lg.error:
                try:
                    loc = page.locator(lg.error).first
                    if loc.is_visible():
                        msg = clean(loc.inner_text())
                except PlaywrightError:
                    pass
            hint = "" if self.show else " 추가 인증(2단계·캡차)이 필요하면 --show로 창을 띄워 직접 끝내세요."
            raise LoginError(f"로그인 실패{': ' + msg if msg else ''}.{hint}") from None

    # ---- 추출 ----

    def _open(self, page: PWPage, spec: Page) -> None:
        """대상 페이지를 열고 표(또는 로그인 화면)가 나타날 때까지 기다린다."""
        page.goto(spec.url, wait_until="domcontentloaded", timeout=NAV_TIMEOUT_MS)
        target = spec.wait_for or spec.rows
        try:
            page.locator(f"{target}, {self.prof.login.password}").first.wait_for(state="attached", timeout=NAV_TIMEOUT_MS)
        except PWTimeout:
            pass  # 행이 하나도 없는 표일 수 있다. 아래에서 빈 결과로 처리한다.

    def _extract(self, page: PWPage, spec: Page) -> tuple[list[str], list[list[str]]]:
        data = page.evaluate(_EXTRACT_JS, {"rows": spec.rows, "columns": spec.columns})
        headers = [clean(h) for h in data["headers"]]
        rows = [[clean(c) for c in r] for r in data["rows"]]
        return headers, [r for r in rows if any(r)]

    def _next(self, page: PWPage, spec: Page) -> bool:
        """다음 페이지 버튼을 눌러 행이 바뀌면 True."""
        btn = page.locator(spec.next).first
        try:
            if not btn.is_visible() or not btn.is_enabled():
                return False
            if (btn.get_attribute("aria-disabled") or "").lower() == "true":
                return False
            before = page.evaluate(_SIGNATURE_JS, spec.rows)
            btn.click()
            page.wait_for_function(
                f"(old) => ({_SIGNATURE_JS})({spec.rows!r}) !== old",
                arg=before,
                timeout=NAV_TIMEOUT_MS,
            )
            return True
        except (PWTimeout, PlaywrightError):
            return False

    def _scrape_page(self, page: PWPage, spec: Page, result: ScrapeResult) -> PageResult:
        pr = PageResult(page=spec)
        self._open(page, spec)
        if self._on_login_page(page):
            self._login(page)
            result.logged_in = True
            self._open(page, spec)
            if self._on_login_page(page):
                raise LoginError("로그인 후에도 로그인 화면으로 돌아갑니다. 프로필의 login 설정을 확인하세요")
        pr.url = page.url
        while True:
            headers, rows = self._extract(page, spec)
            if headers and not pr.headers:
                pr.headers = headers
            pr.rows.extend(rows)
            pr.pages_read += 1
            if not spec.next or pr.pages_read >= spec.max_pages or not self._next(page, spec):
                break
        return pr

    def run(self, only: str | None = None) -> ScrapeResult:
        specs = [p for p in self.prof.pages if only is None or p.name == only]
        if not specs:
            raise ValueError(f"페이지가 없습니다: {only}")
        result = ScrapeResult()
        with sync_playwright() as pw:
            browser = pw.chromium.launch(headless=not self.show)
            try:
                ctx = browser.new_context(storage_state=credentials.load_session(self.prof.name), locale="ko-KR")
                page = ctx.new_page()
                for spec in specs:
                    try:
                        result.pages.append(self._scrape_page(page, spec, result))
                    except (LoginError, NeedCredentials):
                        raise
                    except PlaywrightError as e:
                        result.pages.append(PageResult(page=spec, url=page.url, error=clean(str(e).splitlines()[0])))
                credentials.save_session(self.prof.name, ctx.storage_state())
            finally:
                browser.close()
        return result

    def check_login(self) -> None:
        """세션을 버리고 아이디·비밀번호로 로그인만 해 본다. 성공하면 새 세션을 저장한다."""
        with sync_playwright() as pw:
            browser = pw.chromium.launch(headless=not self.show)
            try:
                ctx = browser.new_context(locale="ko-KR")
                page = ctx.new_page()
                page.goto(self.prof.login.url, wait_until="domcontentloaded", timeout=NAV_TIMEOUT_MS)
                self._login(page)
                credentials.save_session(self.prof.name, ctx.storage_state())
            finally:
                browser.close()
