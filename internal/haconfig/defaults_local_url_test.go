package haconfig

import "testing"

func TestIsLocalURLRejectsUserinfoSpoof(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"http://localhost:8123", true},
		{"http://127.0.0.1:8123", true},
		{"http://homeassistant:8123", true},
		{"http://hass:8123", true},
		{"https://localhost:8123", true},
		{"http://localhost:@prod.example/", false},
		{"http://localhost@prod.example/", false},
		{"http://user:pass@localhost:8123", false},
		{"http://prod.example/", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isLocalURL(tc.raw); got != tc.want {
			t.Fatalf("isLocalURL(%q)=%v want %v", tc.raw, got, tc.want)
		}
	}
}
