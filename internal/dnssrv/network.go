package dnssrv

import (
	"fmt"
	"net"
	"sort"
	"strconv"
)

func ValidateBindAddress(address string, allowUnsafe bool) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid bind address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("bind address %q must be an IPv4 address", address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("invalid bind port in %q", address)
	}
	if !allowUnsafe && (ip.IsUnspecified() || (!ip.IsPrivate() && !ip.IsLoopback())) {
		return "", fmt.Errorf("refusing public or wildcard bind %q; use a private interface address", address)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

func lanClient(address net.Addr) bool {
	var ip net.IP
	switch value := address.(type) {
	case *net.UDPAddr:
		ip = value.IP
	case *net.TCPAddr:
		ip = value.IP
	default:
		host, _, err := net.SplitHostPort(address.String())
		if err != nil {
			return false
		}
		ip = net.ParseIP(host)
	}
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

func DetectLANAddress() (net.IP, error) {
	var routeAddress net.IP
	connection, err := net.Dial("udp", "192.0.2.1:53")
	if err == nil {
		routeAddress = connection.LocalAddr().(*net.UDPAddr).IP
		_ = connection.Close()
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var candidates []net.IP
	for _, address := range addresses {
		ipNetwork, ok := address.(*net.IPNet)
		if !ok || ipNetwork.IP.To4() == nil || !ipNetwork.IP.IsPrivate() {
			continue
		}
		candidates = append(candidates, ipNetwork.IP.To4())
	}
	return SelectLANAddress(candidates, routeAddress)
}

func SelectLANAddress(candidates []net.IP, preferred net.IP) (net.IP, error) {
	unique := map[string]net.IP{}
	for _, candidate := range candidates {
		if candidate == nil || candidate.To4() == nil || !candidate.IsPrivate() {
			continue
		}
		unique[candidate.To4().String()] = candidate.To4()
	}
	if preferred != nil && preferred.To4() != nil && preferred.IsPrivate() {
		if ip, exists := unique[preferred.To4().String()]; exists {
			return ip, nil
		}
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("no private IPv4 LAN address found; use --lan-ip")
	}
	if len(unique) > 1 {
		addresses := make([]string, 0, len(unique))
		for address := range unique {
			addresses = append(addresses, address)
		}
		sort.Strings(addresses)
		return nil, fmt.Errorf("multiple private IPv4 addresses found (%v); use --lan-ip", addresses)
	}
	for _, ip := range unique {
		return ip, nil
	}
	panic("unreachable")
}
