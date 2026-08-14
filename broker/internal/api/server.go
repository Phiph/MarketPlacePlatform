// Package api is the broker's HTTP surface: a catalog of installed Promises
// and, per team, the ability to submit/list/get/delete requests against
// them - either in the team's own flat namespace, or scoped to one of the
// team's project/environment namespaces. See README.md for the full
// endpoint list and example curl commands.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"marketplace-broker/internal/bindingapi"
	"marketplace-broker/internal/catalog"
	"marketplace-broker/internal/k8sclient"
	"marketplace-broker/internal/resourceapi"
	"marketplace-broker/internal/tenant"
)

const maxRequestBody = 1 << 20 // 1MiB - a request's spec should never need more

// Server holds the dependencies every handler needs.
type Server struct {
	admin         *k8sclient.Clients
	dir           *tenant.Directory
	allowedOrigin string
}

// New builds a Server. allowedOrigin is the origin the UI is served from
// (e.g. http://localhost:5173 for `make ui-dev`); requests from other
// origins won't get CORS headers and will be blocked by the browser. Pass
// "" to disable CORS entirely (same-origin deployments don't need it).
func New(clients *k8sclient.Clients, dir *tenant.Directory, allowedOrigin string) *Server {
	return &Server{admin: clients, dir: dir, allowedOrigin: allowedOrigin}
}

// Handler builds the broker's full routing tree: an unauthenticated
// /healthz, and every /api/... route behind the Bearer-token auth
// middleware.
func (s *Server) Handler() http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /promises", s.listPromises)
	apiMux.HandleFunc("GET /promises/{name}", s.getPromise)
	apiMux.HandleFunc("GET /promises/{name}/versions", s.listPromiseVersions)

	// Flat routes: requests land in the caller's own team-<name> namespace.
	// This is the original, still-default request shape - unchanged.
	apiMux.HandleFunc("POST /promises/{name}/requests", s.submitRequest)
	apiMux.HandleFunc("GET /promises/{name}/requests", s.listRequests)
	apiMux.HandleFunc("GET /promises/{name}/requests/{reqName}", s.getRequest)
	apiMux.HandleFunc("PUT /promises/{name}/requests/{reqName}", s.updateRequest)
	apiMux.HandleFunc("DELETE /promises/{name}/requests/{reqName}", s.deleteRequest)
	apiMux.HandleFunc("GET /promises/{name}/requests/{reqName}/version", s.getRequestVersion)
	apiMux.HandleFunc("POST /promises/{name}/requests/{reqName}/version", s.setRequestVersion)

	// Scoped routes: requests land in a project's environment namespace
	// instead. Project/Environment CRUD itself (aside from creation, below)
	// rides these same flat routes above with promise name "project" or
	// "environment" - see promises/project/README.md and
	// promises/environment/README.md.
	apiMux.HandleFunc("POST /projects/{project}/environments/{environment}/promises/{name}/requests", s.submitScopedRequest)
	apiMux.HandleFunc("GET /projects/{project}/environments/{environment}/promises/{name}/requests", s.listScopedRequests)
	apiMux.HandleFunc("GET /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}", s.getScopedRequest)
	apiMux.HandleFunc("PUT /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}", s.updateScopedRequest)
	apiMux.HandleFunc("DELETE /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}", s.deleteScopedRequest)
	apiMux.HandleFunc("GET /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version", s.getScopedRequestVersion)
	apiMux.HandleFunc("POST /projects/{project}/environments/{environment}/promises/{name}/requests/{reqName}/version", s.setScopedRequestVersion)

	// The one dedicated endpoint: environment creation composes its spec
	// server-side rather than forwarding the request body, since
	// spec.team/spec.businessUnit are RBAC-relevant - see
	// promises/environment/README.md, "Why team/businessUnit are
	// broker-owned fields". doSubmitRequest refuses to create an
	// environment via the generic routes above for the same reason;
	// get/list/delete are identity-insensitive and still ride them
	// untouched.
	apiMux.HandleFunc("POST /environments", s.createEnvironment)

	handler := http.Handler(apiMux)
	handler = withAuth(s.dir, handler)
	if s.allowedOrigin != "" {
		handler = cors(s.allowedOrigin, handler)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", healthz)
	root.Handle("/api/", http.StripPrefix("/api", handler))
	return root
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listPromises(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"

	entries, err := catalog.List(r.Context(), s.admin.Dynamic, all)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) getPromise(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// submitRequest submits into the caller's flat team-<name> namespace - the
// default, unscoped request shape.
func (s *Server) submitRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doSubmitRequest(w, r, *entry, team, tenant.Namespace(team))
}

// submitScopedRequest submits into a project's environment namespace
// instead. There's no ownership pre-check on {project}/{environment} here -
// exactly like the flat routes, it's Kubernetes RBAC (via the impersonated
// tenant.Group(team) client doSubmitRequest builds) that actually stops
// team A from touching team B's project/environment namespace, not a
// broker-side string comparison. A namespace that doesn't exist, or
// belongs to another team, simply 403s/404s the same way any other
// cross-team access would.
func (s *Server) submitScopedRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doSubmitRequest(w, r, *entry, team, namespace)
}

func (s *Server) doSubmitRequest(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	// environment is the one Promise whose spec carries broker-owned,
	// RBAC-relevant fields (team/businessUnit - see createEnvironment).
	// Those are only safe because createEnvironment composes them from the
	// authenticated caller instead of the request body; this generic path
	// forwards body.Spec verbatim, so it must not be allowed to create an
	// Environment at all, or a caller could hand-set team/businessUnit to
	// someone else's and have the pipeline label a new namespace with them.
	if entry.Name == "environment" {
		writeError(w, http.StatusForbidden, "environment requests can't be created via this endpoint; use POST /environments")
		return
	}

	var body struct {
		Name string                 `json:"name"`
		Spec map[string]interface{} `json:"spec"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "\"name\" is required")
		return
	}

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	created, err := resourceapi.Submit(r.Context(), client, entry, namespace, body.Name, body.Spec)
	switch {
	case errors.Is(err, resourceapi.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "a request named "+body.Name+" already exists")
	case apierrors.IsInvalid(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusCreated, created.Object)
	}
}

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doListRequests(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) listScopedRequests(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doListRequests(w, r, *entry, team, namespace)
}

func (s *Server) doListRequests(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	items, err := resourceapi.List(r.Context(), client, entry, namespace)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	objects := make([]map[string]interface{}, len(items))
	for i, item := range items {
		objects[i] = item.Object
	}
	writeJSON(w, http.StatusOK, objects)
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doGetRequest(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) getScopedRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doGetRequest(w, r, *entry, team, namespace)
}

func (s *Server) doGetRequest(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	obj, ok, err := resourceapi.Get(r.Context(), client, entry, namespace, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}
	writeJSON(w, http.StatusOK, obj.Object)
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doDeleteRequest(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) deleteScopedRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doDeleteRequest(w, r, *entry, team, namespace)
}

func (s *Server) doDeleteRequest(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	deleted, err := resourceapi.Delete(r.Context(), client, entry, namespace, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPromiseVersions(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

// requestVersionInfo is the shared response shape for doGetRequestVersion
// and doSetRequestVersion.
type requestVersionInfo struct {
	BoundVersion     string `json:"boundVersion"`
	LatestVersion    string `json:"latestVersion"`
	UpgradeAvailable bool   `json:"upgradeAvailable"`
}

func latestVersion(revisions []catalog.Revision) string {
	for _, rev := range revisions {
		if rev.Latest {
			return rev.Version
		}
	}
	return ""
}

func (s *Server) getRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doGetRequestVersion(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) getScopedRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doGetRequestVersion(w, r, *entry, team, namespace)
}

// doGetRequestVersion reports which PromiseRevision reqName is currently
// bound to, and whether a newer one is available. A separate sub-resource
// from doGetRequest deliberately: that handler passes the raw Kubernetes
// object straight through (writeJSON(w, ..., obj.Object)) and three UI
// call sites already depend on that exact shape - see
// docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md,
// "New read endpoints."
func (s *Server) doGetRequestVersion(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	reqName := r.PathValue("reqName")

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	_, ok, err := resourceapi.Get(r.Context(), client, entry, namespace, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	latest := latestVersion(revisions)

	binding, ok, err := bindingapi.Get(r.Context(), client, namespace, entry.Name, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	bound := bindingapi.Version(binding, latest)
	writeJSON(w, http.StatusOK, requestVersionInfo{
		BoundVersion:     bound,
		LatestVersion:    latest,
		UpgradeAvailable: bound != latest,
	})
}

func (s *Server) setRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doSetRequestVersion(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) setScopedRequestVersion(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doSetRequestVersion(w, r, *entry, team, namespace)
}

// doSetRequestVersion moves an existing request's ResourceBinding to a
// different Promise revision - forward (upgrade) or back (rollback), no
// directionality check. Validates the request's current spec against the
// target revision's schema first, so a bad move 400s immediately instead
// of the Resource Configure workflow failing asynchronously later.
func (s *Server) doSetRequestVersion(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Version == "" {
		writeError(w, http.StatusBadRequest, "\"version\" is required")
		return
	}

	reqName := r.PathValue("reqName")

	schemaObj, ok, err := catalog.RevisionSchema(r.Context(), s.admin.Dynamic, entry.Name, body.Version)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such promise version: "+body.Version)
		return
	}

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	existing, ok, err := resourceapi.Get(r.Context(), client, entry, namespace, reqName)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	spec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if problems := catalog.ValidateAgainstSchema(schemaObj, spec); len(problems) > 0 {
		writeError(w, http.StatusBadRequest, "spec is not valid for version "+body.Version+": "+strings.Join(problems, "; "))
		return
	}

	binding, ok, err := bindingapi.SetVersion(r.Context(), client, namespace, entry.Name, reqName, body.Version)
	switch {
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the binding was modified concurrently; reload and try again")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}

	revisions, err := catalog.ListRevisions(r.Context(), s.admin.Dynamic, entry.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	latest := latestVersion(revisions)
	bound := bindingapi.Version(binding, latest)

	writeJSON(w, http.StatusOK, requestVersionInfo{
		BoundVersion:     bound,
		LatestVersion:    latest,
		UpgradeAvailable: bound != latest,
	})
}

func (s *Server) updateRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	s.doUpdateRequest(w, r, *entry, team, tenant.Namespace(team))
}

func (s *Server) updateScopedRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
		return
	}
	team := teamFromContext(r.Context())
	namespace := tenant.ProjectEnvironmentNamespace(team, r.PathValue("project"), r.PathValue("environment"))
	s.doUpdateRequest(w, r, *entry, team, namespace)
}

// doUpdateRequest replaces an existing request's .spec wholesale (see
// resourceapi.Update). Same environment guard as doSubmitRequest, for the
// same reason: spec.team/spec.businessUnit are RBAC-relevant and
// broker-composed for Environment, and spec.project is meaningless to
// change post-creation since the namespace it produced is already fixed.
func (s *Server) doUpdateRequest(w http.ResponseWriter, r *http.Request, entry catalog.Entry, team, namespace string) {
	if entry.Name == "environment" {
		writeError(w, http.StatusForbidden, "environment requests can't be updated via this endpoint; use POST /environments")
		return
	}

	var body struct {
		Spec map[string]interface{} `json:"spec"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Spec == nil {
		writeError(w, http.StatusBadRequest, "\"spec\" is required")
		return
	}

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	updated, ok, err := resourceapi.Update(r.Context(), client, entry, namespace, reqName, body.Spec)
	switch {
	case apierrors.IsInvalid(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the request was modified concurrently; reload and try again")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such request: "+reqName)
		return
	}
	writeJSON(w, http.StatusOK, updated.Object)
}

// createEnvironment is the one place a client-supplied field would be a
// real integrity problem if trusted directly: spec.team/spec.businessUnit
// become an RBAC-relevant label on a real namespace (see
// promises/environment/README.md). So unlike every other route in this
// file, this handler composes the Environment's spec itself from the
// authenticated caller's own identity rather than forwarding a
// client-supplied spec - unauthenticated fields in, never out.
func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "\"name\" is required")
		return
	}
	if body.Project == "" {
		writeError(w, http.StatusBadRequest, "\"project\" is required")
		return
	}

	team := teamFromContext(r.Context())
	namespace := tenant.Namespace(team)

	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	projectEntry, ok, err := catalog.Get(r.Context(), s.admin.Dynamic, "project")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusBadGateway, "the project Promise isn't installed")
		return
	}

	// Cheap, same-namespace, no RBAC implication - just a clearer 404 than
	// whatever error Capsule would eventually surface for a Namespace
	// referencing a nonexistent project.
	if _, ok, err := resourceapi.Get(r.Context(), client, *projectEntry, namespace, body.Project); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "no such project: "+body.Project)
		return
	}

	environmentEntry, ok, err := catalog.Get(r.Context(), s.admin.Dynamic, "environment")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusBadGateway, "the environment Promise isn't installed")
		return
	}

	businessUnit, ok := s.dir.BusinessUnit(team)
	if !ok {
		writeError(w, http.StatusBadGateway, "no business unit on record for team "+team)
		return
	}

	// The whole point of this handler: composed here, not taken from the
	// request body.
	spec := map[string]interface{}{
		"project":      body.Project,
		"team":         team,
		"businessUnit": businessUnit,
	}

	created, err := resourceapi.Submit(r.Context(), client, *environmentEntry, namespace, body.Name, spec)
	switch {
	case errors.Is(err, resourceapi.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "a request named "+body.Name+" already exists")
	case apierrors.IsInvalid(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusCreated, created.Object)
	}
}

// lookupPromise resolves the {name} path value to a catalog.Entry, writing
// the appropriate error response and returning ok=false if it can't. Every
// /promises/{name}... handler starts with this.
func (s *Server) lookupPromise(w http.ResponseWriter, r *http.Request) (*catalog.Entry, bool) {
	name := r.PathValue("name")

	entry, ok, err := catalog.Get(r.Context(), s.admin.Dynamic, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return nil, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such promise: "+name)
		return nil, false
	}
	if !entry.Namespaced() {
		writeError(w, http.StatusBadRequest, "promise "+name+" is not namespace-scoped; the broker only supports namespaced requests")
		return nil, false
	}
	return entry, true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
