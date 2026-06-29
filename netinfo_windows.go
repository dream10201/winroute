//go:build windows

package main

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Iface is a connected IPv4 adapter with a usable gateway.
type Iface struct {
	Index   int
	Name    string
	IPv4    []net.IP
	Gateway net.IP
	Metric  uint32
}

// enumerateIfaces returns all "up" adapters that have at least one IPv4
// address and an IPv4 default gateway.
func enumerateIfaces() ([]Iface, error) {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS |
		windows.GAA_FLAG_SKIP_ANYCAST |
		windows.GAA_FLAG_SKIP_MULTICAST |
		windows.GAA_FLAG_SKIP_DNS_SERVER

	size := uint32(15000)
	var buf []byte
	for {
		buf = make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0, aa, &size)
		if err == nil {
			break
		}
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue // size now holds the required length; loop and retry
		}
		return nil, err
	}

	var out []Iface
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp {
			continue
		}

		var gw net.IP
		for g := aa.FirstGatewayAddress; g != nil; g = g.Next {
			if ip := g.Address.IP(); ip != nil && ip.To4() != nil {
				gw = ip.To4()
				break
			}
		}
		if gw == nil {
			continue // no IPv4 gateway -> not a routable upstream
		}

		var ips []net.IP
		for u := aa.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip != nil && ip.To4() != nil {
				ips = append(ips, ip.To4())
			}
		}
		if len(ips) == 0 {
			continue
		}

		out = append(out, Iface{
			Index:   int(aa.IfIndex),
			Name:    windows.UTF16PtrToString(aa.FriendlyName),
			IPv4:    ips,
			Gateway: gw,
			Metric:  aa.Ipv4Metric,
		})
	}
	return out, nil
}
