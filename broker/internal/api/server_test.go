package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"marketplace-broker/internal/catalog"
)

// doSubmitRequest is the single choke point behind both the flat and
// project-scoped create routes. These tests call it directly so they can
// check the environment guard without standing up a fake Kubernetes client
// for the rest of the handler.

func TestDoSubmitRequest_RejectsEnvironmentViaGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPost, "/promises/environment/requests",
		strings.NewReader(`{"name":"prod","spec":{"team":"someone-elses-team","businessUnit":"someone-elses-bu"}}`))
	w := httptest.NewRecorder()

	s.doSubmitRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}

	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, "/environments") {
		t.Errorf("error message %q doesn't point the caller at POST /environments", resp.Error)
	}
}

// The guard must fire before the body is even decoded, so it can't be
// bypassed by sending a body createEnvironment's own validation would
// otherwise reject first.
func TestDoSubmitRequest_RejectsEnvironmentBeforeDecodingBody(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPost, "/promises/environment/requests", strings.NewReader(`not valid json`))
	w := httptest.NewRecorder()

	s.doSubmitRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// Every other Promise must still reach the normal body-validation path
// (evidenced here by the "name" required error, which only fires after the
// environment guard is skipped).
func TestDoSubmitRequest_AllowsOtherPromisesThroughGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "database"}
	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests", strings.NewReader(`{"spec":{}}`))
	w := httptest.NewRecorder()

	s.doSubmitRequest(w, req, entry, "some-team", "team-some-team")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, `"name" is required`) {
		t.Errorf("error message %q, want the missing-name validation error", resp.Error)
	}
}

// doUpdateRequest is the single choke point behind both the flat and
// project-scoped update routes, same shape as doSubmitRequest above.

func TestDoUpdateRequest_RejectsEnvironmentViaGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPut, "/promises/environment/requests/prod",
		strings.NewReader(`{"spec":{"team":"someone-elses-team","businessUnit":"someone-elses-bu"}}`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}

	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, "/environments") {
		t.Errorf("error message %q doesn't point the caller at POST /environments", resp.Error)
	}
}

// The guard must fire before the body is even decoded, so it can't be
// bypassed by sending a body that would otherwise fail validation first.
func TestDoUpdateRequest_RejectsEnvironmentBeforeDecodingBody(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "environment"}
	req := httptest.NewRequest(http.MethodPut, "/promises/environment/requests/prod", strings.NewReader(`not valid json`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "attacker-team", "team-attacker-team")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// Every other Promise must still reach the normal body-validation path
// (evidenced here by the "spec" required error, which only fires after the
// environment guard is skipped).
func TestDoUpdateRequest_AllowsOtherPromisesThroughGenericRoute(t *testing.T) {
	s := &Server{}
	entry := catalog.Entry{Name: "database"}
	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/my-db", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, entry, "some-team", "team-some-team")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	if !strings.Contains(resp.Error, `"spec" is required`) {
		t.Errorf("error message %q, want the missing-spec validation error", resp.Error)
	}
}
