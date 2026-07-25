package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP は直接接続元が信頼済みプロキシの場合に限り、X-Forwarded-Forを評価する。
// 右端から信頼済みプロキシを除外し、最初の未信頼アドレスをクライアントIPとして返す。
func ClientIP(r *http.Request, trustedProxyCIDRs []netip.Prefix) string {
	peer, ok := parseRemoteAddr(r.RemoteAddr)
	if !ok {
		return ""
	}
	if !isTrustedProxy(peer, trustedProxyCIDRs) {
		return peer.String()
	}

	forwarded := parseXForwardedFor(r.Header.Get("X-Forwarded-For"))
	if len(forwarded) == 0 {
		return peer.String()
	}

	candidate := peer
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate = forwarded[i]
		if !isTrustedProxy(candidate, trustedProxyCIDRs) {
			return candidate.String()
		}
	}
	return candidate.String()
}

// ClientIPHash はログ・セッション記録用にIPアドレスをSHA-256で不可逆化する。
func ClientIPHash(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}

// ClientIPHashFromRequest は信頼プロキシ設定を適用して接続元IPのハッシュを返す。
func ClientIPHashFromRequest(r *http.Request, trustedProxyCIDRs []netip.Prefix) string {
	return ClientIPHash(ClientIP(r, trustedProxyCIDRs))
}

func parseRemoteAddr(remoteAddr string) (netip.Addr, bool) {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteAddr = host
	}
	remoteAddr = strings.Trim(remoteAddr, "[]")
	addr, err := netip.ParseAddr(remoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func parseXForwardedFor(value string) []netip.Addr {
	parts := strings.Split(value, ",")
	addresses := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		addresses = append(addresses, addr.Unmap())
	}
	return addresses
}

func isTrustedProxy(addr netip.Addr, trustedProxyCIDRs []netip.Prefix) bool {
	for _, prefix := range trustedProxyCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
