package util

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func hostRequest(host string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = host
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestParseHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		headers map[string]string
		want    RequestHost
		baseURL string
	}{
		{
			name: "subdomain is the label next to a two-part public suffix, with port",
			host: "acme.office.example.co.uk:7001",
			want: RequestHost{Scheme: "http", Subdomain: "office", Domain: "example.co.uk", TLD: "co.uk",
				Hostname: "acme.office.example.co.uk", Port: "7001"},
			baseURL: "http://example.co.uk:7001",
		},
		{
			name:    "registrable domain alone has no subdomain",
			host:    "example.com",
			want:    RequestHost{Scheme: "http", Domain: "example.com", TLD: "com", Hostname: "example.com"},
			baseURL: "http://example.com",
		},
		{
			name:    "IPv6 loopback with port",
			host:    "[::1]:7001",
			want:    RequestHost{Scheme: "http", Domain: "::1", Hostname: "::1", Port: "7001"},
			baseURL: "http://[::1]:7001",
		},
		{
			name:    "IPv4 loopback",
			host:    "127.0.0.1",
			want:    RequestHost{Scheme: "http", Domain: "127.0.0.1", Hostname: "127.0.0.1"},
			baseURL: "http://127.0.0.1",
		},
		{
			name:    "bare localhost with the Vite port",
			host:    "localhost:5173",
			want:    RequestHost{Scheme: "http", Domain: "localhost", Hostname: "localhost", Port: "5173"},
			baseURL: "http://localhost:5173",
		},
		{
			name: "tenant on the local dev domain",
			host: "acme.ctoup.localhost",
			want: RequestHost{Scheme: "http", Subdomain: "acme", Domain: "ctoup.localhost", TLD: "localhost",
				Hostname: "acme.ctoup.localhost"},
			baseURL: "http://ctoup.localhost",
		},
		{
			name: "deep host keeps the label next to the domain",
			host: "bo.corpa.cto.com",
			want: RequestHost{Scheme: "http", Subdomain: "corpa", Domain: "cto.com", TLD: "com",
				Hostname: "bo.corpa.cto.com"},
			baseURL: "http://cto.com",
		},
		{
			name: "a private public suffix is never the domain",
			host: "acme.github.io",
			want: RequestHost{Scheme: "http", Domain: "acme.github.io", TLD: "github.io",
				Hostname: "acme.github.io"},
			baseURL: "http://acme.github.io",
		},
		{
			name: "case and trailing dot are normalised",
			host: "Acme.Example.COM.",
			want: RequestHost{Scheme: "http", Subdomain: "acme", Domain: "example.com", TLD: "com",
				Hostname: "acme.example.com"},
			baseURL: "http://example.com",
		},
		{
			name:    "first hop of X-Forwarded-Proto sets the scheme",
			host:    "acme.example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https, http"},
			want: RequestHost{Scheme: "https", Subdomain: "acme", Domain: "example.com", TLD: "com",
				Hostname: "acme.example.com"},
			baseURL: "https://example.com",
		},
		{
			name:    "Origin wins over Host",
			host:    "api.example.com",
			headers: map[string]string{"Origin": "https://acme.example.com:8443", "X-Forwarded-Proto": "http"},
			want: RequestHost{Scheme: "https", Subdomain: "acme", Domain: "example.com", TLD: "com",
				Hostname: "acme.example.com", Port: "8443"},
			baseURL: "https://example.com:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHost(hostRequest(tt.host, tt.headers))
			if err != nil {
				t.Fatalf("ParseHost: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseHost = %+v, want %+v", got, tt.want)
			}
			if b := got.BaseURL(); b != tt.baseURL {
				t.Errorf("BaseURL = %q, want %q", b, tt.baseURL)
			}
			if strings.Contains(got.BaseURL(), "://.") {
				t.Errorf("BaseURL %q has an empty label", got.BaseURL())
			}
		})
	}
}

func TestParseHostRefusesWhatNamesNoSite(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		headers map[string]string
	}{
		{"opaque Origin", "acme.example.com", map[string]string{"Origin": "null"}},
		{"Origin without scheme", "acme.example.com", map[string]string{"Origin": "acme.example.com"}},
		{"non-numeric port", "acme.example.com:abc", nil},
		{"unclosed IPv6 bracket", "[::1:7001", nil},
		{"no host at all", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := hostRequest(tt.host, tt.headers)
			r.URL.Host = ""
			if _, err := ParseHost(r); err == nil {
				t.Fatal("ParseHost accepted a request naming no usable site")
			}
		})
	}
	if _, err := ParseHost(nil); !errors.Is(err, errNoRequest) {
		t.Errorf("ParseHost(nil) = %v, want errNoRequest", err)
	}
}

func TestGinWrappersAgreeWithParseHost(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = hostRequest("acme.office.example.co.uk:7001", map[string]string{"X-Forwarded-Proto": "https"})

	if u, err := GetHost(c); err != nil || u.String() != "https://acme.office.example.co.uk:7001" {
		t.Errorf("GetHost = %v, %v", u, err)
	}
	if s, _ := GetSubdomain(c); s != "office" {
		t.Errorf("GetSubdomain = %q", s)
	}
	if d, _ := GetDomain(c); d != "example.co.uk" {
		t.Errorf("GetDomain = %q", d)
	}
	if d, _ := GetBaseDomainWithPort(c); d != "example.co.uk:7001" {
		t.Errorf("GetBaseDomainWithPort = %q", d)
	}
	if b, _ := ExtractBaseURL(c); b != "https://example.co.uk:7001" {
		t.Errorf("ExtractBaseURL = %q", b)
	}
	info, err := GetDomainInfo(c)
	if err != nil {
		t.Fatalf("GetDomainInfo: %v", err)
	}
	want := DomainInfo{Subdomain: "office", Domain: "example.co.uk", TLD: "co.uk",
		FullHost: "acme.office.example.co.uk", Port: "7001", BaseURL: "https://office.example.co.uk:7001"}
	if *info != want {
		t.Errorf("GetDomainInfo = %+v, want %+v", *info, want)
	}

	for host, base := range map[string]string{
		"acme.example.com:8080": "http://acme.example.com:8080",
		"bo.corpa.cto.com":      "http://corpa.cto.com",
		"example.com":           "http://example.com",
		"localhost:5173":        "http://localhost:5173",
	} {
		c.Request = hostRequest(host, nil)
		if info, _ := GetDomainInfo(c); info.BaseURL != base {
			t.Errorf("DomainInfo.BaseURL for %s = %q, want %q", host, info.BaseURL, base)
		}
	}

	c.Request = hostRequest("[::1]:7001", nil)
	if d, _ := GetBaseDomainWithPort(c); d != "[::1]:7001" {
		t.Errorf("GetBaseDomainWithPort on IPv6 = %q", d)
	}
	if info, _ := GetDomainInfo(c); info.BaseURL != "http://[::1]:7001" {
		t.Errorf("DomainInfo.BaseURL on IPv6 = %q", info.BaseURL)
	}
}

func TestIsAdminSubdomain(t *testing.T) {
	for sub, want := range map[string]bool{"": true, "www": true, "admin": true, "acme": false, "auth": false} {
		if got := IsAdminSubdomain(sub); got != want {
			t.Errorf("IsAdminSubdomain(%q) = %v, want %v", sub, got, want)
		}
	}
}
