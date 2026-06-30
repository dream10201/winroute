//go:build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// The two halves of the address space. More specific than the default route
// (0.0.0.0/0) so they win over whatever default the company NIC installs,
// yet less specific than the company CIDRs so 10.x still goes to the company.
var defaultSplit = []string{"0.0.0.0/1", "128.0.0.0/1"}

// target is the desired next hop for a destination.
type target struct {
	gateway string
	ifIndex int
}

var logger = log.New(os.Stdout, "", log.LstdFlags)

func logf(format string, a ...any) { logger.Printf(format, a...) }

func main() {
	exe, _ := os.Executable()
	defConfig := filepath.Join(filepath.Dir(exe), "config.json")

	cfgPath := flag.String("config", defConfig, "path to config.json")
	logPath := flag.String("log", "", "log file path (default: stdout)")
	once := flag.Bool("once", false, "apply once and exit (no polling)")
	flag.Parse()

	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		logger.SetOutput(f)
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		logf("FATAL config: %v", err)
		os.Exit(1)
	}
	logf("winroute starting | config=%s poll=%ds forceDefault=%v",
		*cfgPath, cfg.PollSeconds, cfg.ForceDefaultToRouter)

	if !windows.GetCurrentProcessToken().IsElevated() {
		logf("FATAL: not running as administrator — editing the routing table " +
			"requires elevation. Run from an elevated PowerShell, or install as a " +
			"SYSTEM scheduled task with install.ps1.")
		os.Exit(1)
	}

	applied := map[string]target{} // cidr -> currently installed next hop
	dns := newDNSCache(time.Duration(cfg.DNSRefreshSeconds) * time.Second)

	// On shutdown, withdraw everything we installed so we leave the routing
	// table the way we found it.
	cleanup := func() {
		for cidr := range applied {
			if err := deleteRoute(cidr); err != nil {
				logf("cleanup delete %s: %v", cidr, err)
			}
		}
		logf("winroute stopped, routes withdrawn")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	var lastSig string
	reconcileOnce(cfg, applied, dns, &lastSig)
	if *once {
		return
	}

	ticker := time.NewTicker(time.Duration(cfg.PollSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			reconcileOnce(cfg, applied, dns, &lastSig)
		case s := <-sig:
			logf("signal %v received", s)
			cleanup()
			return
		}
	}
}

// reconcileOnce computes the desired routing state from the current network
// and mutates the table so it matches, updating `applied` in place.
func reconcileOnce(cfg Config, applied map[string]target, dns *dnsCache, lastSig *string) {
	ifaces, err := enumerateIfaces()
	if err != nil {
		logf("enumerate interfaces: %v", err)
		return
	}

	company, router := classify(ifaces, cfg)
	desired := buildDesired(cfg, company, router, dns)

	// Add / change.
	for cidr, want := range desired {
		if cur, ok := applied[cidr]; ok && cur == want {
			continue
		}
		if err := applyRoute(cidr, want.gateway, want.ifIndex); err != nil {
			logf("apply %s -> %s if %d: %v", cidr, want.gateway, want.ifIndex, err)
			continue
		}
		logf("route %-18s via %-15s if %d", cidr, want.gateway, want.ifIndex)
		applied[cidr] = want
	}
	// Remove what is no longer desired.
	for cidr := range applied {
		if _, ok := desired[cidr]; ok {
			continue
		}
		if err := deleteRoute(cidr); err != nil {
			logf("withdraw %s: %v", cidr, err)
			continue
		}
		logf("withdraw %s", cidr)
		delete(applied, cidr)
	}

	// Only print the state line when something actually changed, so a steady
	// network stays quiet instead of logging every poll.
	if sig := stateSig(company, router, desired); sig != *lastSig {
		logState(company, router, desired)
		*lastSig = sig
	}
}

// stateSig is a stable fingerprint of the current routing decision: which
// adapters are in play and the full destination -> next-hop map.
func stateSig(company, router *Iface, desired map[string]target) string {
	var b strings.Builder
	ifSig := func(i *Iface) {
		if i == nil {
			b.WriteString("none;")
			return
		}
		fmt.Fprintf(&b, "%d/%s;", i.Index, i.Gateway)
	}
	ifSig(company)
	ifSig(router)

	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t := desired[k]
		fmt.Fprintf(&b, "%s>%s/%d;", k, t.gateway, t.ifIndex)
	}
	return b.String()
}

// classify picks which adapter is the company net and which is the router.
func classify(ifaces []Iface, cfg Config) (company, router *Iface) {
	_, detect, _ := net.ParseCIDR(cfg.DetectCIDR)
	cHint := strings.ToLower(cfg.CompanyInterfaceHint)
	rHint := strings.ToLower(cfg.RouterInterfaceHint)

	// First pass: name hints take priority (most explicit).
	for i := range ifaces {
		name := strings.ToLower(ifaces[i].Name)
		if cHint != "" && strings.Contains(name, cHint) && company == nil {
			company = &ifaces[i]
		}
		if rHint != "" && strings.Contains(name, rHint) && router == nil {
			router = &ifaces[i]
		}
	}
	// Second pass: company = adapter with an address inside DetectCIDR.
	if company == nil {
		for i := range ifaces {
			for _, ip := range ifaces[i].IPv4 {
				if detect.Contains(ip) {
					company = &ifaces[i]
					break
				}
			}
			if company != nil {
				break
			}
		}
	}
	// Router = the best remaining adapter (lowest metric) that isn't company.
	if router == nil {
		var cands []*Iface
		for i := range ifaces {
			if company != nil && ifaces[i].Index == company.Index {
				continue
			}
			cands = append(cands, &ifaces[i])
		}
		sort.SliceStable(cands, func(a, b int) bool { return cands[a].Metric < cands[b].Metric })
		if len(cands) > 0 {
			router = cands[0]
		}
	}
	return company, router
}

// buildDesired turns the live interface state + config into the target table.
func buildDesired(cfg Config, company, router *Iface, dns *dnsCache) map[string]target {
	desired := map[string]target{}

	// expandRule turns a rule value (IP / CIDR / hostname) into one or more
	// canonical CIDRs. Hostnames are resolved via the system DNS into /32s.
	expand := func(value string) []string {
		if looksLikeHostname(value) {
			var out []string
			for _, ip := range dns.lookup(value) {
				out = append(out, ip.String()+"/32")
			}
			return out
		}
		if n, err := normalizeCIDR(value); err == nil {
			return []string{n}
		}
		return nil
	}

	if company != nil {
		ct := target{company.Gateway.String(), company.Index}
		for _, c := range cfg.CompanyCIDRs {
			if n, err := normalizeCIDR(c); err == nil {
				desired[n] = ct
			}
		}
		for _, r := range cfg.Rules {
			if strings.EqualFold(r.Target, "company") {
				for _, n := range expand(r.Dest) {
					desired[n] = ct
				}
			}
		}
	}

	if router != nil {
		rt := target{router.Gateway.String(), router.Index}
		for _, r := range cfg.Rules {
			if strings.EqualFold(r.Target, "router") {
				for _, n := range expand(r.Dest) {
					desired[n] = rt
				}
			}
		}
		// Only steal the default route when the company net is also present
		// (otherwise there is nothing to override and the router is already
		// the only default).
		if cfg.ForceDefaultToRouter && company != nil {
			for _, c := range defaultSplit {
				desired[c] = rt
			}
		}
	}
	return desired
}

func logState(company, router *Iface, desired map[string]target) {
	c, r := "none", "none"
	if company != nil {
		c = fmt.Sprintf("%s(if%d gw %s)", company.Name, company.Index, company.Gateway)
	}
	if router != nil {
		r = fmt.Sprintf("%s(if%d gw %s)", router.Name, router.Index, router.Gateway)
	}
	logf("state: company=%s router=%s managed=%d", c, r, len(desired))
}
