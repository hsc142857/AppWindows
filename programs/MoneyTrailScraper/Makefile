.PHONY: setup test

PY := .venv/bin/python

# 가상환경 + Playwright + Chromium (처음 한 번)
setup:
	python3 -m venv .venv
	$(PY) -m pip install -q --upgrade pip
	$(PY) -m pip install -q -r requirements.txt pytest
	$(PY) -m playwright install chromium

# 단위 테스트 + 가짜 사이트에 헤드리스 Chromium으로 로그인하는 통합 테스트
test:
	@test -x $(PY) || $(MAKE) setup
	$(PY) -m pytest -q tests
