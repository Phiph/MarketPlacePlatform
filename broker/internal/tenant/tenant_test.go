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
