package cmd

import (
	"errors"
	"reflect"
	"testing"
)

func TestPortableOnboardingCompletesBrowserSetup(t *testing.T) {
	steps := []devOnboardingStep{{Step: "user", Done: true}, {Step: "core_config"}, {Step: "analytics"}, {Step: "integration"}}
	var calls []string
	err := finishPortableOnboarding(steps, "synthetic-token", "http://localhost:8123/", func(path, token string, payload, out any) error {
		calls = append(calls, path)
		if token != "synthetic-token" {
			t.Fatal("onboarding call did not authenticate")
		}
		if path == "/api/onboarding/integration" {
			values := payload.(map[string]string)
			if values["client_id"] != "http://localhost:8123/" || values["redirect_uri"] != values["client_id"] {
				t.Fatal("integration onboarding lost client identity")
			}
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(calls, []string{"/api/onboarding/core_config", "/api/onboarding/analytics", "/api/onboarding/integration"}) {
		t.Fatalf("onboarding calls = %v, error = %v", calls, err)
	}
	for i := range steps {
		steps[i].Done = true
	}
	if err := finishPortableOnboarding(steps, "synthetic-token", "http://localhost:8123/", func(string, string, any, any) error {
		t.Fatal("repeated completed onboarding step")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := finishPortableOnboarding([]devOnboardingStep{{Step: "analytics"}}, "synthetic-token", "http://localhost:8123/", func(string, string, any, any) error {
		return errors.New("fixture failure")
	}); err == nil {
		t.Fatal("onboarding failure returned success")
	}
}
