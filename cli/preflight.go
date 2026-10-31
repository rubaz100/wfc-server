package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"wwfc/internal/privbind"
)

// preflightPorts checks every required socket before authorization. Hold the
// probes until the check finishes so overlapping configured services are also
// detected. Bind still has to handle races after these probes are closed.
func preflightPorts(ctx context.Context, specs []privbind.Spec) (bool, error) {
	var probes []io.Closer
	defer func() {
		for _, probe := range probes {
			_ = probe.Close()
		}
	}()
	var problems []string
	needsAuthorization := false
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := privbind.ValidateSpec(spec); err != nil {
			return false, err
		}
		var probe io.Closer
		var err error
		if spec.Network == "tcp" {
			probe, err = net.Listen("tcp", spec.Address)
		} else {
			probe, err = net.ListenPacket("udp", spec.Address)
		}
		if err == nil {
			probes = append(probes, probe)
			continue
		}
		detail := err.Error()
		switch {
		case errors.Is(err, syscall.EADDRINUSE):
			detail = identifyPortHolder(ctx, spec.Network, spec.Address)
		case permission(err) && privilegedPort(spec.Address):
			// Permission denied does not prove that a privileged port is free.
			// Try process discovery as well, then let the real binder verify it.
			if holder := findPortHolder(ctx, spec.Network, spec.Address); holder != "" {
				detail = holder
			} else {
				needsAuthorization = true
				continue
			}
		}
		problems = append(problems, fmt.Sprintf("  %s (%s %s): %s", spec.Name, spec.Network, spec.Address, detail))
	}
	if len(problems) != 0 {
		return false, fmt.Errorf("startup port preflight failed:\n%s\n\n"+
			"Free occupied ports or correct the listed bind addresses. "+
			"Development port overrides: --dns-port / --http-port / --https-port", strings.Join(problems, "\n"))
	}
	return needsAuthorization, nil
}

func privilegedPort(address string) bool {
	_, port, err := net.SplitHostPort(address)
	n, numberErr := strconv.Atoi(port)
	return err == nil && numberErr == nil && n > 0 && n < 1024
}

func privilegedPortSummary(specs []privbind.Spec) string {
	var parts []string
	seen := map[string]bool{}
	for _, spec := range specs {
		if !privilegedPort(spec.Address) {
			continue
		}
		_, port, _ := net.SplitHostPort(spec.Address)
		label := spec.Name + " " + port
		if !seen[label] {
			seen[label] = true
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, ", ")
}

func identifyPortHolder(ctx context.Context, network, address string) string {
	if holder := findPortHolder(ctx, network, address); holder != "" {
		return holder
	}
	if network == "tcp" {
		return "in use; no listener identified (connections from a stopped server may still be closing)"
	}
	return "in use by unknown process"
}

// Query by port, then match the local address ourselves: an exact-address lsof
// query can miss wildcard listeners, while a port-only fallback can blame a
// process on a different interface. -a keeps TCP's LISTEN filter conjunctive.
func findPortHolder(ctx context.Context, network, address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	args := []string{"-a", "-n", "-P", "-Fpcn", "-i" + strings.ToUpper(network) + ":" + port}
	if network == "tcp" {
		args = append(args, "-sTCP:LISTEN")
	}
	output, err := exec.CommandContext(ctx, "lsof", args...).Output()
	if err != nil {
		return ""
	}
	return portHolderFromOutput(string(output), address)
}

func portHolderFromOutput(output, address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	var pid, command string
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, command = line[1:], ""
		case 'c':
			command = line[1:]
		case 'n':
			local := strings.SplitN(line[1:], "->", 2)[0]
			boundHost, boundPort, err := net.SplitHostPort(local)
			if err != nil || boundPort != port || pid == "" || command == "" {
				continue
			}
			if host == "0.0.0.0" || boundHost == "*" || boundHost == "0.0.0.0" || boundHost == "::" || boundHost == host {
				return fmt.Sprintf("in use by %s (pid %s)", command, pid)
			}
		}
	}
	return ""
}
