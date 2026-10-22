package registry

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Network kinds reported by clients for their P2P listeners.
const (
	NetworkLAN = "lan"
	NetworkVPN = "vpn"
)

// maxEndpointPriority bounds the client supplied priority so a single
// endpoint cannot dominate the candidate list indefinitely.
const maxEndpointPriority = 1000

var defaultPriority = map[string]uint16{
	NetworkLAN: 100,
	NetworkVPN: 50,
}

// Endpoint is one P2P candidate address of a device.
type Endpoint struct {
	Network  string `json:"network"`
	IP       string `json:"ip"`
	Port     uint16 `json:"port"`
	Priority uint16 `json:"priority"`
}

// NormalizeEndpoints validates, de-duplicates and orders a candidate pool.
// The result is deterministic so endpoint comparison and presence broadcasts
// remain stable.
func NormalizeEndpoints(endpoints []Endpoint, max int) ([]Endpoint, error) {
	if max <= 0 {
		return nil, errors.New("endpoint limit must be positive")
	}
	if len(endpoints) > max {
		return nil, fmt.Errorf("at most %d endpoints are allowed", max)
	}

	normalized := make([]Endpoint, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for index, endpoint := range endpoints {
		network := strings.ToLower(strings.TrimSpace(endpoint.Network))
		if network != NetworkLAN && network != NetworkVPN {
			return nil, fmt.Errorf("endpoint %d: network must be %q or %q", index, NetworkLAN, NetworkVPN)
		}

		addr, err := netip.ParseAddr(strings.TrimSpace(endpoint.IP))
		if err != nil || addr.Zone() != "" {
			return nil, fmt.Errorf("endpoint %d: invalid IP address", index)
		}
		addr = addr.Unmap()
		if err := validateAddress(addr); err != nil {
			return nil, fmt.Errorf("endpoint %d: %w", index, err)
		}
		if endpoint.Port == 0 {
			return nil, fmt.Errorf("endpoint %d: port must be between 1 and 65535", index)
		}
		if endpoint.Priority > maxEndpointPriority {
			return nil, fmt.Errorf("endpoint %d: priority must be at most %d", index, maxEndpointPriority)
		}

		priority := endpoint.Priority
		if priority == 0 {
			priority = defaultPriority[network]
		}
		key := network + "|" + addr.String() + "|" + strconv.Itoa(int(endpoint.Port))
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, Endpoint{
			Network:  network,
			IP:       addr.String(),
			Port:     endpoint.Port,
			Priority: priority,
		})
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Priority != normalized[j].Priority {
			return normalized[i].Priority > normalized[j].Priority
		}
		if normalized[i].Network != normalized[j].Network {
			return normalized[i].Network < normalized[j].Network
		}
		if normalized[i].IP != normalized[j].IP {
			return normalized[i].IP < normalized[j].IP
		}
		return normalized[i].Port < normalized[j].Port
	})
	return normalized, nil
}

func validateAddress(addr netip.Addr) error {
	switch {
	case !addr.IsValid():
		return errors.New("invalid IP address")
	case addr.IsUnspecified():
		return errors.New("IP address must not be unspecified")
	case addr.IsLoopback():
		return errors.New("IP address must not be loopback")
	case addr.IsMulticast():
		return errors.New("IP address must not be multicast")
	case addr.IsLinkLocalUnicast():
		return errors.New("IP address must not be link-local")
	default:
		return nil
	}
}
