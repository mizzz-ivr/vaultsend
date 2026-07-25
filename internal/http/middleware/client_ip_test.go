package middleware

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.20")

	if got := ClientIP(req, nil); got != "203.0.113.10" {
		t.Fatalf("want direct peer got=%q", got)
	}
}

func TestClientIPUsesFirstUntrustedAddressFromRight(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "192.0.2.200, 198.51.100.20, 10.0.0.4")
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	if got := ClientIP(req, trusted); got != "198.51.100.20" {
		t.Fatalf("want nearest untrusted client got=%q", got)
	}
}

func TestClientIPSupportsIPv6TrustedProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[2001:db8:1::10]:443"
	req.Header.Set("X-Forwarded-For", "2001:db8:2::20")
	trusted := []netip.Prefix{netip.MustParsePrefix("2001:db8:1::/64")}

	if got := ClientIP(req, trusted); got != "2001:db8:2::20" {
		t.Fatalf("want IPv6 client got=%q", got)
	}
}

func TestNormalizedRateLimitEndpointDoesNotExposeAccessToken(t *testing.T) {
	got := normalizedRateLimitEndpoint("GET", "/v1/access/sensitive-secret-token")
	if got != "GET /v1/access/{token}" {
		t.Fatalf("unexpected endpoint=%q", got)
	}
	if got == "GET /v1/access/sensitive-secret-token" {
		t.Fatal("access token must not remain in normalized endpoint")
	}
}

func TestRateLimiterFailsClosedAtEntryLimit(t *testing.T) {
	limiter := NewInMemoryRateLimiter(1)
	if !limiter.allow("client-1", 10, 60_000_000_000) {
		t.Fatal("first key should be allowed")
	}
	if limiter.allow("client-2", 10, 60_000_000_000) {
		t.Fatal("new key must be rejected when limiter storage is full")
	}
}
