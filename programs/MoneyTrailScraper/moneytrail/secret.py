"""비밀값(아이디·비밀번호·세션·API 토큰) 보호.

Windows는 DPAPI(현재 사용자 범위)로 암호화하고, 그 밖의 OS(개발용)는 평문 그대로 둔다
(그 경우 파일 권한 0600으로만 보호된다 — AVAILABLE이 False). shared/secret(Go)과 같은 규칙이다.
"""

from __future__ import annotations

import base64
import sys

AVAILABLE = sys.platform == "win32"

if AVAILABLE:
    import ctypes
    from ctypes import wintypes

    class _Blob(ctypes.Structure):
        _fields_ = [("cbData", wintypes.DWORD), ("pbData", ctypes.POINTER(ctypes.c_char))]

    _crypt32 = ctypes.windll.crypt32
    _kernel32 = ctypes.windll.kernel32
    _UI_FORBIDDEN = 0x1

    def _call(fn, data: bytes) -> bytes:
        buf = ctypes.create_string_buffer(data, len(data))
        inp = _Blob(len(data), ctypes.cast(buf, ctypes.POINTER(ctypes.c_char)))
        out = _Blob()
        if not fn(ctypes.byref(inp), None, None, None, None, _UI_FORBIDDEN, ctypes.byref(out)):
            raise OSError(ctypes.FormatError())
        try:
            return ctypes.string_at(out.pbData, out.cbData)
        finally:
            _kernel32.LocalFree(out.pbData)


def protect(plain: str) -> str:
    """DPAPI로 암호화해 base64로 돌려준다. 같은 Windows 사용자만 복호화할 수 있다."""
    if not plain:
        raise ValueError("빈 값은 암호화할 수 없습니다")
    if not AVAILABLE:
        return plain
    return base64.b64encode(_call(_crypt32.CryptProtectData, plain.encode("utf-8"))).decode("ascii")


def unprotect(enc: str) -> str:
    if not AVAILABLE:
        return enc
    try:
        raw = base64.b64decode(enc, validate=True)
    except ValueError as e:
        raise ValueError("암호문 형식 오류") from e
    return _call(_crypt32.CryptUnprotectData, raw).decode("utf-8")


def seal(obj_json: str) -> dict:
    """파일에 저장할 형태로 감싼다: Windows는 {"dpapi": …}, 그 밖에는 {"plain": …}."""
    return {"dpapi": protect(obj_json)} if AVAILABLE else {"plain": obj_json}


def unseal(box: dict) -> str:
    if "dpapi" in box:
        if not AVAILABLE:
            raise ValueError("Windows DPAPI로 암호화된 값입니다. 저장한 Windows 사용자로 실행하세요")
        try:
            return unprotect(box["dpapi"])
        except OSError as e:
            raise ValueError(f"저장된 값을 복호화할 수 없습니다 (다른 사용자·PC에서 저장했나요?): {e}") from e
    return box.get("plain", "")
