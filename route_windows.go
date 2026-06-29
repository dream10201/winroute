//go:build windows

package main

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"syscall"
)

// route shells out to the Windows ROUTE command. It is run with a hidden
// window so the background service never flashes a console.
func route(args ...string) (string, error) {
	cmd := exec.Command("route", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("route %v: %w: %s", args, err, string(out))
	}
	return string(out), nil
}

// destMask splits a canonical CIDR into the dotted dest + mask that ROUTE wants.
func destMask(cidr string) (dest, mask string, err error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}
	return ipnet.IP.String(), net.IP(ipnet.Mask).String(), nil
}

// applyRoute makes the table contain exactly: cidr -> gateway via ifIndex.
// We delete-then-add (scoped by mask) so it is idempotent whether or not a
// stale entry already exists. Routes are non-persistent on purpose: a reboot
// clears them and the service re-installs the correct ones.
func applyRoute(cidr, gateway string, ifIndex int) error {
	dest, mask, err := destMask(cidr)
	if err != nil {
		return err
	}
	_, _ = route("delete", dest, "mask", mask) // ignore "not found"
	_, err = route("add", dest, "mask", mask, gateway, "if", strconv.Itoa(ifIndex))
	return err
}

func deleteRoute(cidr string) error {
	dest, mask, err := destMask(cidr)
	if err != nil {
		return err
	}
	_, err = route("delete", dest, "mask", mask)
	return err
}
