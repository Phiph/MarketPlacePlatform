// Package api is the broker's HTTP surface: a catalog of installed Promises
// and, per team, the ability to submit/list/get/delete requests against
// them. See README.md for the full endpoint list and example curl commands.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

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
	apiMux.HandleFunc("POST /promises/{name}/requests", s.submitRequest)
	apiMux.HandleFunc("GET /promises/{name}/requests", s.listRequests)
	apiMux.HandleFunc("GET /promises/{name}/requests/{reqName}", s.getRequest)
	apiMux.HandleFunc("DELETE /promises/{name}/requests/{reqName}", s.deleteRequest)

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

func (s *Server) submitRequest(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.lookupPromise(w, r)
	if !ok {
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

	team := teamFromContext(r.Context())
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	created, err := resourceapi.Submit(r.Context(), client, *entry, tenant.Namespace(team), body.Name, body.Spec)
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
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	items, err := resourceapi.List(r.Context(), client, *entry, tenant.Namespace(team))
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
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	obj, ok, err := resourceapi.Get(r.Context(), client, *entry, tenant.Namespace(team), reqName)
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
	client, err := s.admin.Groups.ForGroup(tenant.Group(team))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	reqName := r.PathValue("reqName")
	deleted, err := resourceapi.Delete(r.Context(), client, *entry, tenant.Namespace(team), reqName)
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
