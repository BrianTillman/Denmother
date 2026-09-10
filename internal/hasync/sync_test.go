package hasync

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestSyncEmptyAndPartialRegistries(t *testing.T) {
	for _, failed := range []string{"", "config/device_registry/list", "config/area_registry/list"} {
		t.Run("failed="+failed, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/":
					w.Write([]byte(`{}`))
				case "/api/states":
					w.Write([]byte(`[]`))
				case "/api/websocket":
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					conn.WriteJSON(WSMessage{Type: "auth_required"})
					var auth WSAuthMessage
					if conn.ReadJSON(&auth) != nil {
						return
					}
					conn.WriteJSON(WSMessage{Type: "auth_ok"})
					for {
						var command WSCommandMessage
						if conn.ReadJSON(&command) != nil {
							return
						}
						response := WSMessage{ID: command.ID, Type: "result", Success: true, Result: []byte(`[]`)}
						if command.Type == failed {
							response.Success = false
							response.Error = &WSError{Code: "test_failure", Message: "synthetic fetch failure"}
						}
						conn.WriteJSON(response)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			root := t.TempDir()
			ref := filepath.Join(root, "docs/reference")
			if err := os.MkdirAll(ref, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"entity-list.txt", "device-list.txt", "area-list.txt"} {
				if err := os.WriteFile(filepath.Join(ref, name), []byte("stale-entry\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			syncer := NewSyncer(server.URL, "synthetic-token", filepath.Join(root, "ha-config"))
			syncer.SetQuiet(true)
			result, err := syncer.SyncAll()
			if err != nil {
				t.Fatal(err)
			}
			if (len(result.Warnings) > 0) != (failed != "") {
				t.Fatalf("warnings=%v, failed=%q", result.Warnings, failed)
			}
			for _, dataset := range []struct{ file, command, artifact string }{
				{"entity-list.txt", "states", result.EntityListPath},
				{"device-list.txt", "config/device_registry/list", result.DeviceListPath},
				{"area-list.txt", "config/area_registry/list", result.AreaListPath},
			} {
				contents, err := os.ReadFile(filepath.Join(ref, dataset.file))
				if err != nil {
					t.Fatal(err)
				}
				retained := dataset.command == failed
				if strings.Contains(string(contents), "stale-entry") != retained {
					t.Fatalf("%s stale data retention incorrect: %s", dataset.file, contents)
				}
				if (dataset.artifact == "") != retained {
					t.Fatalf("%s artifact incorrect: %q", dataset.file, dataset.artifact)
				}
			}
		})
	}
}
