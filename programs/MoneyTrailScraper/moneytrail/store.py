"""SQLite 저장소. 표의 행 하나가 entries 한 행이다.

같은 행은 다시 긁어도 새로 쌓지 않고 last_seen_at만 갱신한다. 한 번 올린 행은 다시 보내지 않는다.
uid는 프로필·페이지·행 내용·같은 내용 안의 순번(occurrence)으로 정해지므로 다른 PC에서 긁어도 같다.
"""

from __future__ import annotations

import hashlib
import json
import sqlite3
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS entries (
  id            INTEGER PRIMARY KEY,
  uid           TEXT NOT NULL UNIQUE,
  profile       TEXT NOT NULL,
  page_name     TEXT NOT NULL,
  page_url      TEXT NOT NULL,
  headers       TEXT NOT NULL,
  cells         TEXT NOT NULL,
  row_hash      TEXT NOT NULL,
  occurrence    INTEGER NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  synced_at     TEXT,
  sync_error    TEXT
);
CREATE INDEX IF NOT EXISTS entries_profile ON entries(profile, page_name, id);
CREATE INDEX IF NOT EXISTS entries_unsynced ON entries(id) WHERE synced_at IS NULL AND sync_error IS NULL;
CREATE TABLE IF NOT EXISTS runs (
  id          INTEGER PRIMARY KEY,
  profile     TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  finished_at TEXT,
  status      TEXT NOT NULL,
  rows_seen   INTEGER NOT NULL DEFAULT 0,
  rows_new    INTEGER NOT NULL DEFAULT 0,
  error       TEXT
);
"""


def now() -> str:
    """고정 폭 ISO8601 UTC (Go 프로그램들과 같은 2006-01-02T15:04:05.000000Z 형식)."""
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def row_hash(cells: list[str]) -> str:
    return hashlib.sha256("\x1f".join(cells).encode("utf-8")).hexdigest()


def make_uid(profile: str, page_name: str, rhash: str, occurrence: int) -> str:
    return hashlib.sha256(f"{profile}\x1f{page_name}\x1f{rhash}\x1f{occurrence}".encode("utf-8")).hexdigest()[:32]


@dataclass
class Entry:
    id: int
    uid: str
    profile: str
    page_name: str
    page_url: str
    headers: list[str]
    cells: list[str]
    row_hash: str
    occurrence: int
    first_seen_at: str
    last_seen_at: str
    synced_at: str | None
    sync_error: str | None


class Store:
    def __init__(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        self.db = sqlite3.connect(str(path), timeout=5)
        self.db.row_factory = sqlite3.Row
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.executescript(SCHEMA)

    def close(self) -> None:
        self.db.close()

    # ---- 수집 ----

    def save_rows(self, profile: str, page_name: str, page_url: str, headers: list[str], rows: list[list[str]]) -> tuple[int, int]:
        """(본 행 수, 새 행 수). 같은 내용의 행이 한 번에 여러 개면 occurrence 1, 2, …로 구분한다."""
        ts = now()
        counts: Counter[str] = Counter()
        new = 0
        hdr = json.dumps(headers, ensure_ascii=False)
        with self.db:
            for cells in rows:
                h = row_hash(cells)
                counts[h] += 1
                occ = counts[h]
                uid = make_uid(profile, page_name, h, occ)
                if self.db.execute("UPDATE entries SET last_seen_at=? WHERE uid=?", (ts, uid)).rowcount:
                    continue
                self.db.execute(
                    "INSERT INTO entries(uid, profile, page_name, page_url, headers, cells, row_hash, occurrence, first_seen_at, last_seen_at)"
                    " VALUES (?,?,?,?,?,?,?,?,?,?)",
                    (uid, profile, page_name, page_url, hdr, json.dumps(cells, ensure_ascii=False), h, occ, ts, ts),
                )
                new += 1
        return len(rows), new

    def start_run(self, profile: str) -> int:
        with self.db:
            return self.db.execute("INSERT INTO runs(profile, started_at, status) VALUES (?,?,'running')", (profile, now())).lastrowid

    def finish_run(self, run_id: int, status: str, rows_seen: int, rows_new: int, error: str = "") -> None:
        with self.db:
            self.db.execute(
                "UPDATE runs SET finished_at=?, status=?, rows_seen=?, rows_new=?, error=? WHERE id=?",
                (now(), status, rows_seen, rows_new, error or None, run_id),
            )

    def last_runs(self, limit: int = 10) -> list[sqlite3.Row]:
        return self.db.execute("SELECT * FROM runs ORDER BY id DESC LIMIT ?", (limit,)).fetchall()

    # ---- 조회 ----

    @staticmethod
    def _entry(r: sqlite3.Row) -> Entry:
        return Entry(
            id=r["id"], uid=r["uid"], profile=r["profile"], page_name=r["page_name"], page_url=r["page_url"],
            headers=json.loads(r["headers"]), cells=json.loads(r["cells"]), row_hash=r["row_hash"],
            occurrence=r["occurrence"], first_seen_at=r["first_seen_at"], last_seen_at=r["last_seen_at"],
            synced_at=r["synced_at"], sync_error=r["sync_error"],
        )

    def entries(self, profile: str | None = None, page_name: str | None = None, limit: int | None = None) -> list[Entry]:
        q, args = "SELECT * FROM entries WHERE 1=1", []
        if profile:
            q += " AND profile=?"
            args.append(profile)
        if page_name:
            q += " AND page_name=?"
            args.append(page_name)
        q += " ORDER BY id DESC"
        if limit:
            q += " LIMIT ?"
            args.append(limit)
        return [self._entry(r) for r in self.db.execute(q, args)]

    def counts(self) -> dict[str, int]:
        r = self.db.execute(
            "SELECT COUNT(*) total, SUM(synced_at IS NULL AND sync_error IS NULL) pending, SUM(sync_error IS NOT NULL) rejected FROM entries"
        ).fetchone()
        return {"total": r["total"] or 0, "pending": r["pending"] or 0, "rejected": r["rejected"] or 0}

    # ---- 업로드 ----

    def unsynced(self, limit: int) -> list[Entry]:
        rows = self.db.execute(
            "SELECT * FROM entries WHERE synced_at IS NULL AND sync_error IS NULL ORDER BY id LIMIT ?", (limit,)
        ).fetchall()
        return [self._entry(r) for r in rows]

    def mark_synced(self, uids: list[str]) -> None:
        ts = now()
        with self.db:
            self.db.executemany("UPDATE entries SET synced_at=? WHERE uid=?", [(ts, u) for u in uids])

    def mark_rejected(self, rejected: dict[str, str]) -> None:
        with self.db:
            self.db.executemany("UPDATE entries SET sync_error=? WHERE uid=?", [(e or "rejected", u) for u, e in rejected.items()])

    def retry_rejected(self) -> int:
        with self.db:
            return self.db.execute("UPDATE entries SET sync_error=NULL WHERE sync_error IS NOT NULL").rowcount
