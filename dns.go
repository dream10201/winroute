package main

import (
	"context"
	"net"
	"regexp"
	"sync"
	"time"
)

// hostnameRe is a loose check for "this looks like a domain, not a CIDR/IP".
var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)

// looksLikeHostname reports whether s should be resolved via DNS rather than
// parsed as an IP/CIDR.
func looksLikeHostname(s string) bool {
	if _, err := normalizeCIDR(s); err == nil {
		return false
	}
	return hostnameRe.MatchString(s)
}

// dnsCache resolves hostnames via the system resolver and caches the result so
// we re-resolve at most once per refresh interval. On lookup failure it keeps
// the last good answer so a transient DNS hiccup doesn't drop routes.
type dnsCache struct {
	refresh time.Duration

	mu      sync.Mutex
	entries map[string]*dnsEntry
}

type dnsEntry struct {
	ips     []net.IP // last good IPv4 answer
	resolved time.Time
	hasGood bool
}

func newDNSCache(refresh time.Duration) *dnsCache {
	if refresh <= 0 {
		refresh = 60 * time.Second
	}
	return &dnsCache{refresh: refresh, entries: make(map[string]*dnsEntry)}
}

// lookup returns the IPv4 addresses for host, re-resolving if the cached answer
// is older than the refresh interval. The returned slice may be empty.
func (c *dnsCache) lookup(host string) []net.IP {
	c.mu.Lock()
	e := c.entries[host]
	if e == nil {
		e = &dnsEntry{}
		c.entries[host] = e
	}
	fresh := e.hasGood && time.Since(e.resolved) < c.refresh
	c.mu.Unlock()

	if fresh {
		return e.ips
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		// Keep the previous answer (if any) and try again next interval.
		if e.hasGood {
			logf("dns %s lookup failed (%v) — keeping %d cached IP(s)", host, err, len(e.ips))
			return e.ips
		}
		logf("dns %s lookup failed: %v", host, err)
		return nil
	}

	var ips []net.IP
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			ips = append(ips, v4)
		}
	}
	e.ips = ips
	e.resolved = time.Now()
	e.hasGood = true
	return ips
}
