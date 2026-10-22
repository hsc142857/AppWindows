package core

import (
	"net/url"
	"strings"
)

var loginHosts = map[string]bool{
	"nid.naver.com":             true,
	"accounts.google.com":       true,
	"accounts.kakao.com":        true,
	"logins.daum.net":           true,
	"login.microsoftonline.com": true,
	"login.live.com":            true,
	"appleid.apple.com":         true,
}

var loginWords = []string{"login", "signin", "sign-in", "sign_in", "logon", "auth"}

// IsLoginRedirect는 요청 URL이 로그인 페이지로 리다이렉트되었는지 판정한다.
// 두 URL이 같거나(#·끝 슬래시 무시) 대상 URL 자체가 로그인 페이지면 false.
func IsLoginRedirect(requestedURL, finalURL string) bool {
	if finalURL == "" || sameURL(requestedURL, finalURL) {
		return false
	}
	if looksLikeLogin(requestedURL) {
		return false
	}
	return looksLikeLogin(finalURL)
}

func looksLikeLogin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if loginHosts[strings.ToLower(u.Hostname())] {
		return true
	}
	for _, seg := range strings.Split(strings.ToLower(u.Path), "/") {
		if isLoginSegment(seg) {
			return true
		}
	}
	return false
}

func isLoginSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if strings.HasPrefix(seg, "nidlogin") {
		return true
	}
	for _, w := range loginWords {
		if seg == w || strings.HasPrefix(seg, w+".") {
			return true
		}
	}
	return false
}

func sameURL(a, b string) bool {
	return normalizeURL(a) == normalizeURL(b)
}

func normalizeURL(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// ValidateTargetURL은 대상 URL이 http(s) 절대 URL인지 검사하고 공백을 정리해 돌려준다.
func ValidateTargetURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errorf("http(s) URL만 등록할 수 있습니다: %q", raw)
	}
	return u.String(), nil
}
