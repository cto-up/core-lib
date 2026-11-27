package util

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/publicsuffix"
)

// RequestHost is the site a request was made from, split along the public
// suffix list: "acme.example.co.uk:7001" is Subdomain "acme", Domain
// "example.co.uk", TLD "co.uk", Port "7001".
type RequestHost struct {
	Scheme    string // "http" or "https" (or the Origin's scheme)
	Subdomain string // label just in front of Domain; "" when there is none
	Domain    string // registrable domain, or the whole name for an IP or a single label
	TLD       string // public suffix; "" for an IP or a single label
	Hostname  string // lower-cased, without port or trailing dot
	Port      string // "" when the request named none
}

// DomainInfo is RequestHost in the shape older callers read: FullHost is the
// host name and BaseURL the tenant's site.
type DomainInfo struct {
	Subdomain string
	Domain    string
	TLD       string
	FullHost  string
	Port      string // "" when the request named none, else digits such as "7001"
	BaseURL   string
}

var (
	errNoHost      = errors.New("invalid hostname in URL")
	errNoRequest   = errors.New("no HTTP request on the context")
	errInvalidPort = errors.New("invalid port in request host")
)

// ParseHost reads the request's site once. The Origin header wins when
// present (scheme included); otherwise the Host header is used, with the
// scheme taken from X-Forwarded-Proto and defaulting to http.
func ParseHost(r *http.Request) (RequestHost, error) {
	if r == nil {
		return RequestHost{}, errNoRequest
	}
	scheme, authority, err := siteOf(r)
	if err != nil {
		return RequestHost{}, err
	}
	hostname, port, err := splitAuthority(authority)
	if err != nil {
		return RequestHost{}, err
	}
	h := RequestHost{Scheme: scheme, Hostname: hostname, Port: port}
	var label string
	h.Subdomain, label, h.TLD = extractDomainParts(hostname)
	h.Domain = label
	if h.TLD != "" {
		h.Domain = label + "." + h.TLD
	}
	return h, nil
}

// BaseURL is scheme://domain[:port], without the subdomain.
func (h RequestHost) BaseURL() string {
	return h.Scheme + "://" + withPort(h.Domain, h.Port)
}

// tenantURL is scheme://[subdomain.]domain[:port]: the tenant's site, without
// any label further left.
func (h RequestHost) tenantURL() string {
	host := h.Domain
	if h.Subdomain != "" {
		host = h.Subdomain + "." + h.Domain
	}
	return h.Scheme + "://" + withPort(host, h.Port)
}

func siteOf(r *http.Request) (scheme, authority string, err error) {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, perr := url.Parse(origin)
		if perr != nil || u.Host == "" || u.Scheme == "" {
			return "", "", errNoHost
		}
		return strings.ToLower(u.Scheme), u.Host, nil
	}
	authority = r.Host
	if authority == "" && r.URL != nil {
		authority = r.URL.Host
	}
	if authority == "" {
		return "", "", errNoHost
	}
	return forwardedScheme(r.Header.Get("X-Forwarded-Proto")), authority, nil
}

// forwardedScheme takes the first hop of X-Forwarded-Proto ("https, http"
// from a proxy chain) and ignores anything that is not http or https.
func forwardedScheme(header string) string {
	first, _, _ := strings.Cut(header, ",")
	if s := strings.ToLower(strings.TrimSpace(first)); s == "https" {
		return s
	}
	return "http"
}

// splitAuthority separates "host[:port]" and "[v6][:port]", lower-casing the
// name and dropping a trailing dot.
func splitAuthority(authority string) (hostname, port string, err error) {
	host := authority
	switch {
	case strings.HasPrefix(authority, "["):
		end := strings.Index(authority, "]")
		if end < 0 {
			return "", "", errNoHost
		}
		host = authority[1:end]
		rest := authority[end+1:]
		if rest != "" {
			if rest[0] != ':' {
				return "", "", errNoHost
			}
			port = rest[1:]
		}
	case strings.Count(authority, ":") == 1:
		host, port, _ = strings.Cut(authority, ":")
	}
	for _, ch := range port {
		if ch < '0' || ch > '9' {
			return "", "", fmt.Errorf("%w: %q", errInvalidPort, authority)
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return "", "", errNoHost
	}
	return host, port, nil
}

// extractDomainParts splits a host name around its public suffix:
// "a.b.example.co.uk" gives subdomain "b", label "example", tld "co.uk".
// An IP address, a single label or a bare public suffix is all label.
func extractDomainParts(hostname string) (subdomain, label, tld string) {
	if net.ParseIP(hostname) != nil || !strings.Contains(hostname, ".") {
		return "", hostname, ""
	}
	suffix, _ := publicsuffix.PublicSuffix(hostname)
	if suffix == hostname {
		return "", hostname, ""
	}
	labels := strings.Split(strings.TrimSuffix(hostname, "."+suffix), ".")
	label = labels[len(labels)-1]
	if len(labels) > 1 {
		subdomain = labels[len(labels)-2]
	}
	return subdomain, label, suffix
}

func withPort(host, port string) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" {
		return host
	}
	return host + ":" + port
}

func parseGinHost(c *gin.Context) (RequestHost, error) {
	if c == nil {
		return RequestHost{}, errNoRequest
	}
	return ParseHost(c.Request)
}

// GetHost returns the request's site as a URL holding only scheme and host[:port].
func GetHost(c *gin.Context) (*url.URL, error) {
	h, err := parseGinHost(c)
	if err != nil {
		return nil, err
	}
	return &url.URL{Scheme: h.Scheme, Host: withPort(h.Hostname, h.Port)}, nil
}

// GetDomainInfo returns the parsed request host; BaseURL is the tenant's site,
// scheme://[subdomain.]domain[:port].
func GetDomainInfo(c *gin.Context) (*DomainInfo, error) {
	h, err := parseGinHost(c)
	if err != nil {
		return nil, err
	}
	return &DomainInfo{
		Subdomain: h.Subdomain,
		Domain:    h.Domain,
		TLD:       h.TLD,
		FullHost:  h.Hostname,
		Port:      h.Port,
		BaseURL:   h.tenantURL(),
	}, nil
}

// GetSubdomain returns the tenant label of the request host ("" on the root site).
func GetSubdomain(c *gin.Context) (string, error) {
	h, err := parseGinHost(c)
	return h.Subdomain, err
}

// GetDomain returns the registrable domain of the request host, suffix included.
func GetDomain(c *gin.Context) (string, error) {
	h, err := parseGinHost(c)
	return h.Domain, err
}

// GetBaseDomainWithPort returns domain[:port], without the subdomain.
func GetBaseDomainWithPort(c *gin.Context) (string, error) {
	h, err := parseGinHost(c)
	if err != nil {
		return "", err
	}
	return withPort(h.Domain, h.Port), nil
}

// ExtractBaseURL is RequestHost.BaseURL for a gin request: the root site,
// subdomain dropped, scheme from Origin or X-Forwarded-Proto — for example
// "https://ctoup.com" or "http://localhost:3000".
func ExtractBaseURL(c *gin.Context) (string, error) {
	h, err := parseGinHost(c)
	if err != nil {
		return "", fmt.Errorf("could not determine request host: %w", err)
	}
	return h.BaseURL(), nil
}

// IsAdminSubdomain is true for the root site's labels — none, "www" and
// "admin" — which name no tenant. Every caller asks this function rather than
// keeping its own list.
func IsAdminSubdomain(subdomain string) bool {
	switch subdomain {
	case "", "www", "admin":
		return true
	}
	return false
}
