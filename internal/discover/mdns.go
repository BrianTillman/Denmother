package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// ServiceTypes for Leviton device discovery. Matter service names follow:
// https://github.com/project-chip/connectedhomeip/blob/master/docs/tips_and_troubleshooting/discovery_from_a_host_computer.md
var ServiceTypes = []string{
	"_hap._tcp",     // HomeKit Accessory Protocol
	"_matter._tcp",  // Matter operational discovery
	"_matterc._udp", // Matter commissioning discovery
}

// MDNSBrowser handles mDNS service discovery
type MDNSBrowser struct {
	timeout       time.Duration
	browseService func(context.Context, string) ([]*DiscoveredDevice, error)
}

// NewMDNSBrowser creates a new mDNS browser with the specified timeout
func NewMDNSBrowser(timeout time.Duration) *MDNSBrowser {
	return &MDNSBrowser{
		timeout: timeout,
	}
}

// BrowseServices discovers services of the specified types on the local network.
// Service types are browsed concurrently within the configured timeout.
func (b *MDNSBrowser) BrowseServices(ctx context.Context, serviceTypes []string) ([]*DiscoveredDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.timeout <= 0 {
		return nil, fmt.Errorf("mDNS timeout must be positive")
	}
	type result struct {
		devices []*DiscoveredDevice
		err     error
	}

	results := make(chan result, len(serviceTypes))
	var wg sync.WaitGroup

	for _, serviceType := range serviceTypes {
		wg.Add(1)
		go func(st string) {
			defer wg.Done()
			browse := b.browseService
			if browse == nil {
				browse = b.browseServiceType
			}
			found, err := browse(ctx, st)
			results <- result{devices: found, err: err}
		}(serviceType)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var devices []*DiscoveredDevice
	var browseErrors []error
	successes := 0
	for r := range results {
		if r.err != nil {
			browseErrors = append(browseErrors, r.err)
		} else {
			successes++
		}
		devices = append(devices, r.devices...)
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(browseErrors) > 0 {
		err := errors.Join(browseErrors...)
		if successes == 0 {
			return nil, fmt.Errorf("mDNS browse failed: %w", err)
		}
		return devices, &PartialBrowseError{Err: err}
	}

	return devices, nil
}

func (b *MDNSBrowser) browseServiceType(ctx context.Context, serviceType string) ([]*DiscoveredDevice, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 100)
	var devices []*DiscoveredDevice

	browseCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	if err := resolver.Browse(browseCtx, serviceType, "local.", entries); err != nil {
		return nil, fmt.Errorf("%s: %w", serviceType, err)
	}
	for {
		select {
		case <-browseCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return devices, nil
		case entry, ok := <-entries:
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if browseCtx.Err() == nil {
					return devices, fmt.Errorf("%s: browse stopped before timeout", serviceType)
				}
				return devices, nil
			}
			device := serviceEntryToDevice(entry, serviceType)
			if device != nil && IsLeviton(device) {
				devices = append(devices, device)
			}
		}
	}

}

// PartialBrowseError reports service types that failed while others completed.
type PartialBrowseError struct{ Err error }

func (e *PartialBrowseError) Error() string { return e.Err.Error() }
func (e *PartialBrowseError) Unwrap() error { return e.Err }

func serviceEntryToDevice(entry *zeroconf.ServiceEntry, serviceType string) *DiscoveredDevice {
	if entry == nil {
		return nil
	}

	var addresses []string
	for _, ip := range entry.AddrIPv4 {
		addresses = append(addresses, ip.String())
	}
	for _, ip := range entry.AddrIPv6 {
		if ip.IsLinkLocalUnicast() {
			addresses = append(addresses, ip.String())
		}
	}

	properties := make(map[string]string)
	for _, txt := range entry.Text {
		key, value, _ := strings.Cut(txt, "=")
		if key != "" {
			properties[key] = value
		}

	}

	hostname := entry.HostName
	protocol := DetermineProtocol(serviceType)

	device := &DiscoveredDevice{
		Name:         entry.Instance,
		Hostname:     hostname,
		Addresses:    addresses,
		Port:         entry.Port,
		Properties:   properties,
		ServiceType:  serviceType,
		Protocol:     protocol,
		Manufacturer: properties["manufacturer"],
	}

	device.MAC = ExtractMAC(properties, hostname)

	return device
}

// GetLocalAddresses returns local network interface addresses (for debugging)
func GetLocalAddresses() ([]string, error) {
	var addresses []string

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					addresses = append(addresses, ipnet.IP.String())
				}
			}
		}
	}

	return addresses, nil
}
