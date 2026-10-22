import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))


@pytest.fixture(autouse=True)
def isolated_home(tmp_path, monkeypatch):
    """테스트마다 빈 데이터 폴더를 쓴다 (실제 %LOCALAPPDATA%를 건드리지 않음)."""
    monkeypatch.setenv("MONEYTRAIL_HOME", str(tmp_path / "home"))
    return tmp_path / "home"
