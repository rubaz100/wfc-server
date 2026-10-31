package common

import (
	"fmt"
	"net"
)

// WithLANAddress returns an independent network configuration for a detected
// or explicitly selected LAN interface. Only bindings that followed the old
// LAN address move with it; custom interfaces and loopback listeners are kept.
// Certificate paths, keys, ports and all non-network settings are preserved.
func WithLANAddress(current Config, address string) (Config, error) {
	updated, _, err := planLANAddress(current, address)
	return updated, err
}

// RebindLANAddress persists server-owned interface fields without exposing
// them through the admin settings allowlist. It updates the configuration cache
// only after the targeted XML rewrite succeeds, preserving comments and secrets.
// Callers must confirm that address belongs to this host before invoking it.
func RebindLANAddress(address string) (Config, error) {
	if _, err := LoadConfig(); err != nil {
		return Config{}, err
	}
	cmutex.Lock()
	defer cmutex.Unlock()

	updated, fields, err := planLANAddress(config, address)
	if err != nil {
		return Config{}, err
	}
	if len(fields) != 0 {
		if err := writeConfigFields(ConfigPath(), fields); err != nil {
			return Config{}, fmt.Errorf("save LAN interface configuration: %w", err)
		}
	}
	config = updated
	return updated, nil
}

func planLANAddress(current Config, address string) (Config, map[string]string, error) {
	address, err := ValidatePrivateIPv4(address)
	if err != nil {
		return Config{}, nil, err
	}
	oldLAN := current.LANAddress
	if oldLAN == "" {
		oldLAN = current.DefaultAddress
	}
	// Work on fresh address pointers rather than modifying aliases in the
	// cached Config or another caller's snapshot while previewing an override.
	updated := current
	fields := make(map[string]string)
	set := func(tag string, field *string, value string) {
		if *field != value {
			*field = value
			fields[tag] = value
		}
	}
	followsLAN := func(host string) bool {
		if host == "" || host == oldLAN {
			return true
		}
		oldIP, hostIP := net.ParseIP(oldLAN), net.ParseIP(host)
		return oldIP != nil && hostIP != nil && oldIP.Equal(hostIP)
	}
	moveHost := func(host string) string {
		if followsLAN(host) {
			return address
		}
		return host
	}
	value := func(pointer *string, fallback string) string {
		if pointer == nil || *pointer == "" {
			return fallback
		}
		return *pointer
	}
	movePointer := func(tag string, previous *string, fallback string) *string {
		before := value(previous, fallback)
		after := moveHost(before)
		if after != before {
			fields[tag] = after
		}
		return &after
	}
	moveEndpoint := func(tag string, field *string) {
		host, port, err := net.SplitHostPort(*field)
		if err == nil && followsLAN(host) {
			set(tag, field, net.JoinHostPort(address, port))
		}
	}

	set("lanAddress", &updated.LANAddress, address)
	set("address", &updated.DefaultAddress, moveHost(current.DefaultAddress))
	updated.GameSpyAddress = movePointer("gsAddress", current.GameSpyAddress, current.DefaultAddress)
	updated.NASAddress = movePointer("nasAddress", current.NASAddress, current.DefaultAddress)
	updated.NASAddressHTTPS = movePointer("nasAddressHttps", current.NASAddressHTTPS, value(current.NASAddress, current.DefaultAddress))
	if updated.DNSAddress == "" {
		set("dnsAddress", &updated.DNSAddress, net.JoinHostPort(address, "53"))
	} else {
		moveEndpoint("dnsAddress", &updated.DNSAddress)
	}
	// Explicit LAN-facing ancillary listeners follow the selected interface.
	// Defaults such as the admin/RPC loopback endpoints stay on loopback.
	moveEndpoint("adminAddress", &updated.AdminAddress)
	moveEndpoint("frontendAddress", &updated.FrontendAddress)
	moveEndpoint("frontendBackendAddress", &updated.FrontendBackendAddress)
	moveEndpoint("backendAddress", &updated.BackendAddress)
	moveEndpoint("backendFrontendAddress", &updated.BackendFrontendAddress)
	moveEndpoint("payloadServerAddress", &updated.PayloadServerAddress)
	return updated, fields, nil
}
