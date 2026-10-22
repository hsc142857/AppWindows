"""테스트용 가짜 금융 사이트 (로컬 HTTP).

- /login: 아이디·비밀번호 폼 → 맞으면 sid 쿠키 발급 후 next로 이동, 틀리면 오류 문구
- /settlements?page=N: 정산 내역 표 3쪽 (다음 버튼), 로그인 필요
- /money: 머니 내역 표를 JS로 늦게 그린다 (waitFor 확인용), 로그인 필요
"""

from __future__ import annotations

import html
import secrets
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, quote, urlsplit

USER = "tester"
PASSWORD = "test-pass-123"

SETTLEMENTS = [
    [("2026-10-01", "카드 정산", "120,000"), ("2026-10-02", "카드 정산", "80,500"), ("2026-10-02", "카드 정산", "80,500")],
    [("2026-10-03", "간편결제 정산", "45,000"), ("2026-10-04", "수수료", "-3,200")],
    [("2026-10-05", "카드 정산", "99,000")],
]
MONEY = [("2026-10-10 09:00", "충전", "50,000", "50,000"), ("2026-10-11 13:20", "사용", "-12,000", "38,000")]

_PAGE = "<!doctype html><html lang=ko><head><meta charset=utf-8><title>{title}</title></head><body>{body}</body></html>"


class FakeSite:
    def __init__(self):
        self.sessions: set[str] = set()
        self.login_posts = 0
        self.money_rows = list(MONEY)
        site = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def _send(self, status: int, body: str = "", headers: dict | None = None):
                data = body.encode("utf-8")
                self.send_response(status)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Content-Length", str(len(data)))
                for k, v in (headers or {}).items():
                    self.send_header(k, v)
                self.end_headers()
                self.wfile.write(data)

            def _authed(self) -> bool:
                for part in (self.headers.get("Cookie") or "").split(";"):
                    k, _, v = part.strip().partition("=")
                    if k == "sid" and v in site.sessions:
                        return True
                return False

            def _login_form(self, nxt: str, error: str = ""):
                err = f'<p class="login-error">{html.escape(error)}</p>' if error else ""
                body = (
                    f'<form method="post" action="/login?next={quote(nxt)}">{err}'
                    '<input id="uid" name="uid"><input id="upw" name="upw" type="password">'
                    '<button id="btn-login" type="submit">로그인</button></form>'
                )
                self._send(200, _PAGE.format(title="로그인", body=body))

            def do_GET(self):
                u = urlsplit(self.path)
                q = parse_qs(u.query)
                if u.path == "/login":
                    return self._login_form(q.get("next", ["/"])[0])
                if not self._authed():
                    return self._send(302, headers={"Location": "/login?next=" + quote(self.path)})
                if u.path == "/":
                    return self._send(200, _PAGE.format(title="홈", body='<div id="welcome">환영합니다</div>'))
                if u.path == "/settlements":
                    n = max(1, min(int(q.get("page", ["1"])[0]), len(SETTLEMENTS)))
                    rows = "".join(f"<tr><td>{d}</td><td>{k}</td><td>{amt}</td></tr>" for d, k, amt in SETTLEMENTS[n - 1])
                    nxt = f'<a class="next" href="/settlements?page={n + 1}">다음</a>' if n < len(SETTLEMENTS) else ""
                    body = (
                        '<table class="list"><thead><tr><th>정산일</th><th>구분</th><th>금액</th></tr></thead>'
                        f"<tbody>{rows}</tbody></table>{nxt}"
                    )
                    return self._send(200, _PAGE.format(title="정산 내역", body=body))
                if u.path == "/money":
                    items = ",".join("[" + ",".join(f'"{c}"' for c in r) + "]" for r in site.money_rows)
                    body = (
                        '<div id="app">불러오는 중…</div><script>setTimeout(() => {'
                        f"const rows = [{items}];"
                        "document.getElementById('app').innerHTML = '<ul class=\"history\">' + rows.map(r =>"
                        " '<li class=\"item\"><span class=\"at\">' + r[0] + '</span><b class=\"kind\">' + r[1] +"
                        " '</b><em class=\"amount\">' + r[2] + '</em><i class=\"balance\">' + r[3] + '</i></li>').join('') + '</ul>';"
                        "}, 400);</script>"
                    )
                    return self._send(200, _PAGE.format(title="머니 내역", body=body))
                self._send(404, "not found")

            def do_POST(self):
                u = urlsplit(self.path)
                if u.path != "/login":
                    return self._send(404)
                site.login_posts += 1
                n = int(self.headers.get("Content-Length") or 0)
                form = parse_qs(self.rfile.read(n).decode("utf-8"))
                nxt = parse_qs(u.query).get("next", ["/"])[0]
                if form.get("uid", [""])[0] == USER and form.get("upw", [""])[0] == PASSWORD:
                    sid = secrets.token_hex(8)
                    site.sessions.add(sid)
                    return self._send(302, headers={"Location": nxt, "Set-Cookie": f"sid={sid}; Path=/; HttpOnly"})
                self._login_form(nxt, "아이디 또는 비밀번호가 올바르지 않습니다")

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        self._t = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def __enter__(self):
        self._t.start()
        return self

    def __exit__(self, *exc):
        self.httpd.shutdown()
        self.httpd.server_close()

    def profile(self) -> dict:
        return {
            "name": "fakepay",
            "title": "테스트 페이",
            "login": {
                "url": f"{self.url}/login",
                "username": "#uid",
                "password": "#upw",
                "submit": "#btn-login",
                "error": ".login-error",
            },
            "pages": [
                {"name": "정산내역", "url": f"{self.url}/settlements", "rows": "table.list tbody tr", "next": "a.next", "maxPages": 10},
                {
                    "name": "머니내역",
                    "url": f"{self.url}/money",
                    "rows": "ul.history li.item",
                    "waitFor": "ul.history",
                    "columns": {"일시": ".at", "구분": ".kind", "금액": ".amount", "잔액": ".balance"},
                },
            ],
        }
