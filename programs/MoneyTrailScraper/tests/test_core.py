"""프로필 검증, 자격 증명 저장, SQLite 중복 방지, Web API 업로드."""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from moneytrail import api, credentials, paths
from moneytrail.profile import ProfileError, parse
from moneytrail.store import Store

BASE = {
    "name": "demo",
    "login": {"url": "https://example.com/login", "username": "#id", "password": "#pw"},
    "pages": [{"name": "정산", "url": "https://example.com/s", "rows": "tr"}],
}


def test_profile_validation():
    assert parse(BASE).title == "demo"
    with pytest.raises(ProfileError):
        parse({**BASE, "name": "Bad Name"})
    with pytest.raises(ProfileError):
        parse({**BASE, "login": {**BASE["login"], "url": "ftp://x"}})
    with pytest.raises(ProfileError):
        parse({**BASE, "pages": [BASE["pages"][0], BASE["pages"][0]]})
    with pytest.raises(ProfileError):
        parse({**BASE, "pages": [{**BASE["pages"][0], "maxPages": 0}]})


def test_credentials_roundtrip_private_file():
    credentials.put("demo", credentials.Credential("myid", "s3cret"))
    got = credentials.get("demo")
    assert got.username == "myid" and got.password == "s3cret"
    assert got.masked_username() == "my**"
    assert (paths.credentials_path().stat().st_mode & 0o777) == 0o600
    assert credentials.delete("demo") and credentials.get("demo") is None


def test_session_roundtrip_and_corrupt_file():
    credentials.save_session("demo", {"cookies": [{"name": "sid"}], "origins": []})
    assert credentials.load_session("demo")["cookies"][0]["name"] == "sid"
    (paths.sessions_dir() / "demo.json").write_text("{}")
    assert not credentials.load_session("demo")


def test_store_dedupes_and_counts_occurrence(tmp_path):
    st = Store(tmp_path / "t.sqlite")
    rows = [["a", "1"], ["a", "1"], ["b", "2"]]
    assert st.save_rows("p", "정산", "u", ["x", "y"], rows) == (3, 3)  # 같은 행 두 개는 occurrence 1, 2
    assert st.save_rows("p", "정산", "u", ["x", "y"], rows) == (3, 0)
    assert st.save_rows("p", "정산", "u", ["x", "y"], [["c", "3"]]) == (1, 1)
    assert st.save_rows("p", "머니", "u", ["x", "y"], [["c", "3"]]) == (1, 1)  # 페이지가 다르면 다른 행
    assert st.counts() == {"total": 5, "pending": 5, "rejected": 0}


class _FakeAPI:
    def __init__(self, reject=()):
        self.batches = []
        site = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_POST(self):
                assert self.headers["Authorization"] == "Bearer tok-test"
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                site.batches.append(body)
                uids = [i["uid"] for i in body["items"]]
                out = {
                    "accepted": [u for u in uids if u not in reject],
                    "rejected": [{"uid": u, "error": "bad"} for u in uids if u in reject],
                }
                data = json.dumps(out).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def test_sync_batches_and_rejects(tmp_path):
    st = Store(tmp_path / "t.sqlite")
    st.save_rows("p", "정산", "u", ["k"], [[str(i)] for i in range(150)])
    bad = st.entries(limit=1)[0].uid
    srv = _FakeAPI(reject={bad})
    try:
        r = api.sync(st, api.Config(srv.url, "tok-test"))
    finally:
        srv.close()
    assert [len(b["items"]) for b in srv.batches] == [100, 50]
    assert srv.batches[0]["items"][0].keys() >= {"uid", "profile", "pageName", "cells", "rowHash", "firstSeenAt"}
    assert (r.sent, r.rejected, r.remaining) == (149, 1, 0)
    assert st.counts()["rejected"] == 1
    assert st.retry_rejected() == 1 and st.counts()["pending"] == 1


def test_api_config_validation_and_storage():
    with pytest.raises(ValueError):
        api.save_config(api.Config("http://example.com", "t"))
    api.save_config(api.Config("http://localhost:3000/", "tok-test"))
    c = api.load_config()
    assert c.base_url == "http://localhost:3000" and c.token == "tok-test"
    assert c.masked_token() == "tok-…"
