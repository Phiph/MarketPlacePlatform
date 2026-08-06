package tenant

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGroup(t *testing.T) {
	cases := []struct {
		team string
		want string
	}{
		{team: "payments", want: "marketplace:team-payments"},
		{team: "checkout", want: "marketplace:team-checkout"},
		{team: "", want: "marketplace:team-"},
	}

	for _, tc := range cases {
		if got := Group(tc.team); got != tc.want {
			t.Errorf("Group(%q) = %q, want %q", tc.team, got, tc.want)
		}
	}
}

func TestNamespace(t *testing.T) {
	if got, want := Namespace("payments"), "team-payments"; got != want {
		t.Errorf("Namespace(%q) = %q, want %q", "payments", got, want)
	}
}

func TestProjectEnvironmentNamespace(t *testing.T) {
	cases := []struct {
		team, project, environment, want string
	}{
		{"checkout", "checkout-service", "dev", "project-checkout-checkout-service-dev"},
		{"checkout", "checkout-service", "prod", "project-checkout-checkout-service-prod"},
	}

	for _, tc := range cases {
		if got := ProjectEnvironmentNamespace(tc.team, tc.project, tc.environment); got != tc.want {
			t.Errorf("ProjectEnvironmentNamespace(%q, %q, %q) = %q, want %q", tc.team, tc.project, tc.environment, got, tc.want)
		}
	}
}

// Two teams picking an identical project+environment name must never
// collide on the same Namespace - that's the whole reason team is part of
// the string. See the function's doc comment for the incident this guards
// against.
func TestProjectEnvironmentNamespaceCrossTeamUniqueness(t *testing.T) {
	payments := ProjectEnvironmentNamespace("payments", "data-engine", "dev")
	checkout := ProjectEnvironmentNamespace("checkout", "data-engine", "dev")
	if payments == checkout {
		t.Fatalf("two teams with the same project+environment name collided on namespace %q", payments)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "teams.yaml")
	data := `
businessUnits:
  platform-org:
    teams:
      payments: demo-key-payments
      checkout: demo-key-checkout
  other-org:
    teams:
      support: demo-key-support
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("writing teams file: %v", err)
	}

	dir, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		apiKey   string
		wantTeam string
	}{
		{"demo-key-payments", "payments"},
		{"demo-key-checkout", "checkout"},
		{"demo-key-support", "support"},
	}
	for _, tc := range cases {
		team, ok := dir.Resolve(tc.apiKey)
		if !ok {
			t.Errorf("Resolve(%q): not found", tc.apiKey)
			continue
		}
		if team != tc.wantTeam {
			t.Errorf("Resolve(%q) = %q, want %q", tc.apiKey, team, tc.wantTeam)
		}
	}

	if _, ok := dir.Resolve("no-such-key"); ok {
		t.Error("Resolve(unknown key): got ok=true, want false")
	}
}

func TestDirectoryBusinessUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "teams.yaml")
	data := `
businessUnits:
  platform-org:
    teams:
      payments: demo-key-payments
      checkout: demo-key-checkout
  other-org:
    teams:
      support: demo-key-support
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("writing teams file: %v", err)
	}

	dir, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		team   string
		wantBU string
	}{
		{"payments", "platform-org"},
		{"checkout", "platform-org"},
		{"support", "other-org"},
	}
	for _, tc := range cases {
		bu, ok := dir.BusinessUnit(tc.team)
		if !ok {
			t.Errorf("BusinessUnit(%q): not found", tc.team)
			continue
		}
		if bu != tc.wantBU {
			t.Errorf("BusinessUnit(%q) = %q, want %q", tc.team, bu, tc.wantBU)
		}
	}

	if _, ok := dir.BusinessUnit("no-such-team"); ok {
		t.Error("BusinessUnit(unknown team): got ok=true, want false")
	}
}
