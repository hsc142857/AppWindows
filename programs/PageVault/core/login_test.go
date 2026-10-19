package core

import "testing"

func TestIsLoginRedirect(t *testing.T) {
	cases := []struct {
		name, req, final string
		want             bool
	}{
		{"same", "https://a.com/list", "https://a.com/list", false},
		{"fragment and slash ignored", "https://a.com/list", "https://a.com/list/#top", false},
		{"empty final", "https://a.com/list", "", false},
		{"naver login host", "https://partner.booking.naver.com/bizes/1", "https://nid.naver.com/nidlogin.login?url=x", true},
		{"google accounts", "https://x.com/a", "https://accounts.google.com/ServiceLogin", true},
		{"login path", "https://site.com/logs", "https://site.com/login?next=/logs", true},
		{"signin path", "https://site.com/logs", "https://site.com/user/sign-in", true},
		{"login.php", "https://site.com/logs", "https://site.com/login.php", true},
		{"auth segment", "https://site.com/logs", "https://site.com/auth/callback", true},
		{"nidlogin prefix", "https://site.com/x", "https://other.com/nidlogin.login", true},
		{"other redirect", "https://site.com/logs", "https://site.com/logs/2026", false},
		{"word inside segment", "https://site.com/logs", "https://site.com/blogin", false},
		{"author not auth", "https://site.com/logs", "https://site.com/author", false},
		{"target itself is login page", "https://site.com/login", "https://site.com/login?step=2", false},
	}
	for _, c := range cases {
		if got := IsLoginRedirect(c.req, c.final); got != c.want {
			t.Errorf("%s: IsLoginRedirect(%q, %q) = %v, want %v", c.name, c.req, c.final, got, c.want)
		}
	}
}

func TestValidateTargetURL(t *testing.T) {
	ok := []string{"https://a.com", "http://a.com/x?y=1", "  https://a.com/path  "}
	for _, u := range ok {
		if _, err := ValidateTargetURL(u); err != nil {
			t.Errorf("%q: unexpected error %v", u, err)
		}
	}
	bad := []string{"", "a.com", "ftp://a.com", "javascript:alert(1)", "https://", "file:///C:/x"}
	for _, u := range bad {
		if _, err := ValidateTargetURL(u); err == nil {
			t.Errorf("%q: expected error", u)
		}
	}
}
