package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

// Rule pins a destination to a specific network.
//   target = "company" -> route via the company (10.x) network
//   target = "router"  -> route via the home / CPE router network
// Dest may be a CIDR ("172.16.0.0/12"), a bare IP ("8.8.8.8"), or a hostname
// ("oa.company.com", resolved via the system DNS at runtime).
type Rule struct {
	Dest   string `json:"dest"`
	Target string `json:"target"`
}

// Config is the on-disk configuration (config.json next to the exe by default).
type Config struct {
	// CIDRs that must always go through the company network when it is up.
	CompanyCIDRs []string `json:"company_cidrs"`

	// CIDR used to recognise which adapter is the company network. The adapter
	// holding an IPv4 address inside this range is treated as "company".
	DetectCIDR string `json:"company_detect_cidr"`

	// Optional substring matches against the adapter friendly name. If set they
	// win over DetectCIDR (useful when the company net is not 10.x, or to be
	// explicit about which physical NIC is which).
	CompanyInterfaceHint string `json:"company_interface_hint"`
	RouterInterfaceHint  string `json:"router_interface_hint"`

	// Extra per-destination overrides.
	Rules []Rule `json:"rules"`

	// When both networks are up, force every non-pinned destination (the
	// default route) to use the router by installing 0.0.0.0/1 + 128.0.0.0/1
	// via the router gateway. These beat any default route the company net
	// installs, while the more-specific company CIDRs still win for 10.x.
	ForceDefaultToRouter bool `json:"force_default_to_router"`

	// How often to re-check the network state, in seconds.
	PollSeconds int `json:"poll_seconds"`

	// How often to re-resolve hostname rules via system DNS, in seconds.
	DNSRefreshSeconds int `json:"dns_refresh_seconds"`

	// DNS servers used to resolve hostname rules, e.g. ["10.16.0.10", "1.1.1.1"].
	// Each may include a port ("1.1.1.1:53"). Empty = use the system resolver.
	DNSServers []string `json:"dns_servers"`
}

func defaultConfig() Config {
	return Config{
		CompanyCIDRs:         []string{"10.0.0.0/8"},
		DetectCIDR:           "10.0.0.0/8",
		ForceDefaultToRouter: true,
		PollSeconds:          5,
		DNSRefreshSeconds:    60,
		Rules:                []Rule{},
	}
}

// loadConfig reads path; if it does not exist a default file is written so the
// user has something to edit. Unknown/empty fields fall back to defaults.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if werr := os.WriteFile(path, out, 0o644); werr != nil {
			return cfg, fmt.Errorf("write default config: %w", werr)
		}
		logf("wrote default config to %s", path)
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.PollSeconds <= 0 {
		cfg.PollSeconds = 5
	}
	if cfg.DNSRefreshSeconds <= 0 {
		cfg.DNSRefreshSeconds = 60
	}
	if cfg.DetectCIDR == "" {
		cfg.DetectCIDR = "10.0.0.0/8"
	}
	if len(cfg.CompanyCIDRs) == 0 {
		cfg.CompanyCIDRs = []string{"10.0.0.0/8"}
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if _, _, err := net.ParseCIDR(c.DetectCIDR); err != nil {
		return fmt.Errorf("invalid company_detect_cidr %q: %w", c.DetectCIDR, err)
	}
	for _, c := range c.CompanyCIDRs {
		if _, err := normalizeCIDR(c); err != nil {
			return err
		}
	}
	for _, s := range c.DNSServers {
		host := s
		if h, _, err := net.SplitHostPort(s); err == nil {
			host = h
		}
		if net.ParseIP(host) == nil {
			return fmt.Errorf("invalid dns_servers entry %q (want an IP, optionally with :port)", s)
		}
	}
	for _, r := range c.Rules {
		d := r.Dest
		if d == "" {
			return fmt.Errorf("rule has empty dest")
		}
		// A rule dest is either an IP/CIDR or a hostname (resolved at runtime).
		if _, err := normalizeCIDR(d); err != nil && !looksLikeHostname(d) {
			return fmt.Errorf("rule %q is neither a valid IP/CIDR nor a hostname", d)
		}
		t := strings.ToLower(r.Target)
		if t != "company" && t != "router" {
			return fmt.Errorf("rule %q has invalid target %q (want company|router)", d, r.Target)
		}
	}
	return nil
}

// normalizeCIDR canonicalises "10.1.2.3/8" -> "10.0.0.0/8" so equal networks
// compare equal regardless of how the user wrote them. A bare IP is treated
// as a /32 host route.
func normalizeCIDR(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
			s += "/32"
		}
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR %q: %w", s, err)
	}
	return ipnet.String(), nil
}
