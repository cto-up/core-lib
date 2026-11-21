package config

import (
	"fmt"
	"net"
	"strings"
)

// DefaultTrustedProxies are loopback and the private ranges. The API publishes
// on 127.0.0.1 only, so its peers are nginx on the host (seen through the
// Docker bridge gateway), our own containers, or, in a split prod, web1 over
// the WireGuard mesh. The tailnet (100.64.0.0/10) is deliberately absent.
var DefaultTrustedProxies = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7",
}

// TrustedProxies parses Network.TrustedProxies. An empty value gives
// DefaultTrustedProxies; an entry that is neither an address nor a CIDR range
// is an error, so a typo stops the process instead of silently trusting
// something else.
func TrustedProxies() ([]string, error) {
	return ParseTrustedProxies(Current().Network.TrustedProxies)
}

// ParseTrustedProxies is TrustedProxies for a given value.
func ParseTrustedProxies(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), DefaultTrustedProxies...), nil
	}
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err != nil && net.ParseIP(entry) == nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is neither an address nor a CIDR range", entry)
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %q names no address", raw)
	}
	return out, nil
}
