package discover

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

func discoveryServer(t *testing.T, registryAvailable bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("missing authorization")
			}
			_ = json.NewEncoder(w).Encode([]hasync.EntityState{{EntityID: "light.kitchen", Attributes: map[string]interface{}{"friendly_name": "Kitchen"}}})
			return
		}
		if !registryAvailable {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"type": "auth_required"})
		var auth hasync.WSAuthMessage
		if conn.ReadJSON(&auth) != nil {
			return
		}
		if auth.AccessToken != "secret" {
			t.Error("missing websocket authorization")
		}
		_ = conn.WriteJSON(map[string]any{"type": "auth_ok"})
		for {
			var command hasync.WSCommandMessage
			if conn.ReadJSON(&command) != nil {
				return
			}
			var result any
			switch command.Type {
			case "config/device_registry/list":
				result = []registryDevice{{ID: "device1", Manufacturer: "Leviton", Name: "Kitchen", Connections: [][]string{{"mac", "AA:BB:CC:DD:EE:FF"}}}}
			case "config/entity_registry/list":
				result = []hasync.EntityRegistryEntry{{EntityID: "light.kitchen", DeviceID: "device1"}}
			default:
				t.Errorf("unexpected command %s", command.Type)
				return
			}
			_ = conn.WriteJSON(map[string]any{"id": command.ID, "type": "result", "success": true, "result": result})
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRunUsesRegistryForRenamedEntity(t *testing.T) {
	server := discoveryServer(t, true)
	path := filepath.Join(t.TempDir(), "references", "discovery.json")
	d := NewDiscoverer(Options{HAUrl: server.URL, HAToken: "secret", Timeout: time.Second, OutputPath: path})
	d.browse = func(context.Context, []string) ([]*DiscoveredDevice, error) {
		return []*DiscoveredDevice{{Name: "Kitchen", MAC: "aabbccddeeff", Protocol: "matter"}}, nil
	}
	report, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.RegistryAvailable || len(report.NewDevices) != 0 || len(report.Matches) != 1 || report.Matches[0].Method != "registry" || report.Matches[0].HAEntities[0] != "light.kitchen" {
		t.Fatalf("bad registry report: %+v", report)
	}
	if len(report.InconsistentNames) != 0 || len(report.MatterLightsToExclude) != 1 {
		t.Fatalf("bad comparison: %+v", report)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved LevitonReport
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Matches) != 1 {
		t.Fatal("saved report missing matches")
	}
}

func TestRunKeepsRegistryAndBrowseFailuresExplicit(t *testing.T) {
	server := discoveryServer(t, false)
	d := NewDiscoverer(Options{HAUrl: server.URL, HAToken: "secret", Timeout: time.Second, OutputPath: filepath.Join(t.TempDir(), "report.json")})
	d.browse = func(context.Context, []string) ([]*DiscoveredDevice, error) {
		return nil, &PartialBrowseError{Err: errors.New("matter unavailable")}
	}
	report, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.RegistryAvailable || len(report.Warnings) != 2 {
		t.Fatalf("missing partial warnings: %+v", report)
	}
}

func TestRunCancellationInterruptsRESTAndDoesNotSave(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "report.json")
	d := NewDiscoverer(Options{HAUrl: server.URL, Timeout: time.Second, OutputPath: path})
	d.browse = func(context.Context, []string) ([]*DiscoveredDevice, error) {
		t.Error("must not browse after cancellation")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := d.Run(ctx); result <- err }()
	<-entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not propagate")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected report: %v", err)
	}
}

func TestRegistryIdentityNamespacesAndMultipleEntities(t *testing.T) {
	entries := []hasync.EntityRegistryEntry{{EntityID: "light.room", DeviceID: "one", Platform: "homekit_controller", UniqueID: "AA:BB:CC:DD:EE:FF_1_2"}, {EntityID: "sensor.room", DeviceID: "one"}}
	identities := registryIdentities([]registryDevice{{ID: "one", Manufacturer: "Leviton", Identifiers: [][]string{{"homekit_controller", "AA:BB:CC:DD:EE:FF"}}, SerialNumber: "SERIAL42"}}, entries, nil)
	for _, device := range []*DiscoveredDevice{
		{Protocol: "homekit", Properties: map[string]string{"id": "aa:bb:cc:dd:ee:ff"}},
		{Protocol: "matter", Properties: map[string]string{"sn": "SERIAL42"}},
	} {
		comparison := CompareWithRegistry(nil, identities, []*DiscoveredDevice{device})
		if len(comparison.Matches) != 1 || len(comparison.Matches[0].HAEntities) != 2 {
			t.Fatalf("not matched: %+v", comparison)
		}
	}
	comparison := CompareWithRegistry(nil, identities, []*DiscoveredDevice{{Protocol: "matter", MAC: "aabbccddeeff"}})
	if len(comparison.NewDevices) != 1 {
		t.Fatal("HomeKit accessory ID must not match unrelated network MAC")
	}
}

func TestLegacyMACFallbackRejectsAmbiguousSuffixes(t *testing.T) {
	device := &DiscoveredDevice{MAC: "AA:BB:CC:DD:EE:FF"}
	result := CompareWithHA([]string{"light.levds_kitchen_ddeeff"}, []*DiscoveredDevice{device})
	if len(result.Matches) != 1 || result.Matches[0].Method != "legacy_mac_suffix" {
		t.Fatalf("missing fallback: %+v", result)
	}
	result = CompareWithHA([]string{"light.levds_kitchen_ddeeff", "light.levds_other_ddeeff"}, []*DiscoveredDevice{device})
	if len(result.Matches) != 0 || len(result.NewDevices) != 1 {
		t.Fatal("ambiguous suffix must not match")
	}
	result = CompareWithHA([]string{"light.levds_room_room"}, []*DiscoveredDevice{{MAC: "room"}})
	if len(result.Matches) != 0 {
		t.Fatal("nonhex name must not match")
	}
}

func TestServiceTXTAndManufacturerFiltering(t *testing.T) {
	entry := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Lamp"}, HostName: "lamp.local.", Text: []string{"manufacturer=Leviton", "id=AA:BB:CC:DD:EE:FF", "flag"}, AddrIPv4: []net.IP{net.ParseIP("192.0.2.1")}}
	device := serviceEntryToDevice(entry, "_hap._tcp")
	if len(device.Properties) != 3 || device.Properties["manufacturer"] != "Leviton" || !IsLeviton(device) {
		t.Fatalf("bad TXT parsing: %+v", device)
	}
	if IsLeviton(&DiscoveredDevice{Protocol: "matter", Properties: map[string]string{"s#type": "0x0104"}}) {
		t.Fatal("generic light type is not a manufacturer identity")
	}
}

func TestMDNSPartialWithoutDevicesAndCompleteFailure(t *testing.T) {
	browser := NewMDNSBrowser(time.Second)
	browser.browseService = func(ctx context.Context, service string) ([]*DiscoveredDevice, error) {
		if service == "working" {
			return nil, nil
		}
		return nil, errors.New("resolver unavailable")
	}
	_, err := browser.BrowseServices(context.Background(), []string{"working", "failed"})
	var partial *PartialBrowseError
	if !errors.As(err, &partial) {
		t.Fatalf("zero devices must still retain partial coverage: %v", err)
	}
	_, err = browser.BrowseServices(context.Background(), []string{"failed"})
	if err == nil || errors.As(err, &partial) {
		t.Fatalf("total failure must not be partial: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	browser.browseService = func(context.Context, string) ([]*DiscoveredDevice, error) {
		t.Fatal("canceled browse must not start")
		return nil, nil
	}
	_, err = browser.BrowseServices(ctx, ServiceTypes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("bad cancellation: %v", err)
	}
}

func TestMatterVendorAndStandardServices(t *testing.T) {
	for _, properties := range []map[string]string{{"VP": "4251+4098"}, {"vid": "0x109B"}, {"vendorid": "4251"}} {
		if !IsLeviton(&DiscoveredDevice{Protocol: "matter", Properties: properties}) {
			t.Fatalf("missed Leviton vendor: %v", properties)
		}
	}
	if IsLeviton(&DiscoveredDevice{Protocol: "matter", Properties: map[string]string{"vid": "0x1133"}}) {
		t.Fatal("wrong vendor must not match")
	}
	if !containsString(ServiceTypes, "_matter._tcp") || !containsString(ServiceTypes, "_matterc._udp") || containsString(ServiceTypes, "_matter._udp") {
		t.Fatalf("incorrect Matter services: %v", ServiceTypes)
	}
}
