"""가짜 사이트에 헤드리스 Chromium으로 로그인해 긁는 통합 테스트."""

import pytest
from fakesite import PASSWORD, USER, FakeSite

from moneytrail import credentials
from moneytrail.profile import parse
from moneytrail.scraper import LoginError, NeedCredentials, Scraper


@pytest.fixture
def site():
    with FakeSite() as s:
        yield s


def test_first_run_logs_in_and_scrapes(site):
    prof = parse(site.profile())
    credentials.put(prof.name, credentials.Credential(USER, PASSWORD))

    res = Scraper(prof).run()

    assert res.logged_in
    assert site.login_posts == 1
    settle, money = res.pages
    assert settle.error == "" and settle.pages_read == 3
    assert settle.headers == ["정산일", "구분", "금액"]
    assert len(settle.rows) == 6
    assert settle.rows[0] == ["2026-10-01", "카드 정산", "120,000"]
    assert money.headers == ["일시", "구분", "금액", "잔액"]
    assert money.rows == [list(r) for r in site.money_rows]


def test_second_run_reuses_session(site):
    prof = parse(site.profile())
    credentials.put(prof.name, credentials.Credential(USER, PASSWORD))
    Scraper(prof).run()

    res = Scraper(prof).run(only="정산내역")

    assert not res.logged_in
    assert site.login_posts == 1  # 저장된 세션 쿠키로 들어감


def test_expired_session_relogs_in(site):
    prof = parse(site.profile())
    credentials.put(prof.name, credentials.Credential(USER, PASSWORD))
    Scraper(prof).run(only="머니내역")
    site.sessions.clear()  # 서버 쪽 세션 만료

    res = Scraper(prof).run(only="머니내역")

    assert res.logged_in and site.login_posts == 2
    assert len(res.pages[0].rows) == 2


def test_wrong_password_reports_site_message(site):
    prof = parse(site.profile())
    credentials.put(prof.name, credentials.Credential(USER, "wrong"))
    with pytest.raises(LoginError, match="아이디 또는 비밀번호가 올바르지 않습니다"):
        Scraper(prof).run()


def test_missing_credentials(site):
    prof = parse(site.profile())
    with pytest.raises(NeedCredentials):
        Scraper(prof).run()


def test_check_login_saves_session(site):
    prof = parse(site.profile())
    Scraper(prof, cred=credentials.Credential(USER, PASSWORD)).check_login()
    assert credentials.load_session(prof.name)["cookies"]
