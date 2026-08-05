package api

import (
	"net/http"
	"strings"
)

type routeAccess string

const (
	routeAdmin  routeAccess = "admin"
	routeMember routeAccess = "member"
	routeAdult  routeAccess = "adult"
	routeExempt routeAccess = "exempt"
	routeMount              = "MOUNT"
)

type routePolicy struct {
	Name   string
	Method string
	Path   string
	Access routeAccess
	Member bool
}

type routeRegistration struct {
	Method string
	Path   string
	Policy routePolicy
}

// routePolicies is the security inventory. HTTP handlers stay in api.go; this
// table records the access decision independently so a new registration cannot
// silently acquire the wrong policy.
var routePolicies = []routePolicy{
	{"settings.get", "GET", "/settings", routeAdmin, false}, {"settings.put", "PUT", "/settings", routeAdmin, false}, {"settings.apikey", "POST", "/settings/apikey", routeAdmin, false}, {"settings.metadata-test", "POST", "/settings/metadata/test", routeAdmin, false},
	{"system.status", "GET", "/system/status", routeAdmin, false}, {"system.shutdown", "POST", "/system/shutdown", routeAdmin, false}, {"system.verify", "POST", "/system/verify", routeAdmin, false}, {"storage.repoint", "POST", "/system/storage-root/repoint", routeAdmin, false}, {"storage.migrate", "POST", "/system/storage-root/migrate", routeAdmin, false}, {"storage.migration", "GET", "/system/storage-root/migration", routeAdmin, false},
	{"auth.login", "POST", "/auth/login", routeExempt, true}, {"auth.logout", "POST", "/auth/logout", routeExempt, true}, {"auth.me", "GET", "/auth/me", routeMember, true}, {"settings.password", "POST", "/settings/password", routeMember, true},
	{"users.list", "GET", "/users", routeAdmin, false}, {"users.create", "POST", "/users", routeAdmin, false}, {"users.delete", "DELETE", "/users/{id}", routeAdmin, false}, {"users.password", "POST", "/users/{id}/password", routeAdmin, false},
	{"quality.list", "GET", "/quality-profiles", routeAdmin, false}, {"quality.create", "POST", "/quality-profiles", routeAdmin, false}, {"quality.update", "PUT", "/quality-profiles/{id}", routeAdmin, false}, {"quality.delete", "DELETE", "/quality-profiles/{id}", routeAdmin, false}, {"wanted.list", "GET", "/wanted", routeAdmin, false}, {"wanted.search", "POST", "/wanted/search", routeAdmin, false},
	{"tv-profiles.list", "GET", "/tv-profiles", routeAdmin, false}, {"convert.list", "GET", "/convert", routeAdmin, false}, {"convert.create", "POST", "/convert", routeAdmin, false}, {"convert.cancel", "POST", "/convert/{id}/cancel", routeAdmin, false}, {"convert.retry", "POST", "/convert/{id}/retry", routeAdmin, false}, {"dlna.status", "GET", "/dlna", routeAdmin, false},
	{"handoff.jellyfin.get", "GET", "/handoff/jellyfin", routeAdmin, false}, {"handoff.jellyfin.put", "POST", "/handoff/jellyfin", routeAdmin, false}, {"handoff.jellyfin.test", "POST", "/handoff/jellyfin/test", routeAdmin, false}, {"calendar.list", "GET", "/calendar", routeAdmin, false}, {"calendar.ics", "GET", "/calendar.ics", routeExempt, false}, {"jobs.list", "GET", "/jobs", routeAdmin, false}, {"tasks.list", "GET", "/system/tasks", routeAdmin, false}, {"tasks.run", "POST", "/system/tasks/{kind}/run", routeAdmin, false},
	{"movies.list", "GET", "/library/movies", routeAdmin, false}, {"movies.create", "POST", "/library/movies", routeAdmin, false}, {"movies.get", "GET", "/library/movies/{id}", routeAdmin, false}, {"movies.patch", "PATCH", "/library/movies/{id}", routeAdmin, false}, {"movies.delete", "DELETE", "/library/movies/{id}", routeAdmin, false}, {"series.list", "GET", "/library/series", routeAdmin, false}, {"series.create", "POST", "/library/series", routeAdmin, false}, {"series.get", "GET", "/library/series/{id}", routeAdmin, false}, {"series.patch", "PATCH", "/library/series/{id}", routeAdmin, false}, {"series.delete", "DELETE", "/library/series/{id}", routeAdmin, false}, {"seasons.patch", "PATCH", "/library/series/{id}/seasons/{season}", routeAdmin, false}, {"episodes.patch", "PATCH", "/library/episodes/{id}", routeAdmin, false}, {"library.rescan", "POST", "/library/rescan", routeAdmin, false}, {"movie.releases", "GET", "/library/movies/{id}/releases", routeAdmin, false}, {"movie.grab", "POST", "/library/movies/{id}/grab", routeAdmin, false}, {"series.releases", "GET", "/library/series/{id}/releases", routeAdmin, false}, {"series.grab", "POST", "/library/series/{id}/grab", routeAdmin, false}, {"movie.search", "POST", "/library/movies/{id}/search", routeAdmin, false}, {"series.search", "POST", "/library/series/{id}/search", routeAdmin, false}, {"search", "GET", "/search", routeAdmin, false},
	{"discover.home", "GET", "/discover", routeMember, true}, {"discover.browse", "GET", "/discover/browse", routeMember, true}, {"discover.movies", "GET", "/discover/movies", routeMember, true}, {"discover.series", "GET", "/discover/series", routeMember, true}, {"discover.people", "GET", "/discover/people", routeMember, true}, {"discover.companies", "GET", "/discover/companies", routeMember, true}, {"discover.keywords", "GET", "/discover/keywords", routeMember, true}, {"discover.genres", "GET", "/discover/genres", routeMember, true}, {"discover.title", "GET", "/discover/{type}/{id}", routeMember, true}, {"requests.list", "GET", "/requests", routeMember, true}, {"requests.create", "POST", "/requests", routeMember, true}, {"requests.approve", "POST", "/requests/{id}/approve", routeAdmin, false}, {"requests.dismiss", "DELETE", "/requests/{id}", routeMember, true}, {"libraries.list", "GET", "/libraries", routeAdmin, false}, {"libraries.patch", "PATCH", "/libraries/{id}", routeAdmin, false}, {"libraries.indexer", "PUT", "/libraries/{id}/indexers/{indexerID}", routeAdmin, false},
	{"adult.mount", routeMount, "/adult/", routeAdult, false}, {"adult.sites.list", "GET", "/adult/sites", routeAdult, true}, {"adult.sites.create", "POST", "/adult/sites", routeAdult, false}, {"adult.sites.get", "GET", "/adult/sites/{id}", routeAdult, true}, {"adult.search", "GET", "/adult/search", routeAdult, true}, {"adult.discover", "GET", "/adult/discover", routeAdult, true}, {"adult.performers", "GET", "/adult/performers", routeAdult, true}, {"adult.tags", "GET", "/adult/tags", routeAdult, true}, {"adult.users.list", "GET", "/adult/users", routeAdult, false}, {"adult.users.access", "PUT", "/adult/users/{id}/access", routeAdult, false}, {"adult.stash.get", "GET", "/adult/stash", routeAdult, false}, {"adult.stash.put", "POST", "/adult/stash", routeAdult, false}, {"adult.stash.test", "POST", "/adult/stash/test", routeAdult, false}, {"settings.adult", "POST", "/settings/adult", routeAdmin, false},
	{"indexers.list", "GET", "/indexers", routeAdmin, false}, {"indexers.create", "POST", "/indexers", routeAdmin, false}, {"indexers.update", "PUT", "/indexers/{id}", routeAdmin, false}, {"indexers.delete", "DELETE", "/indexers/{id}", routeAdmin, false}, {"indexers.test", "POST", "/indexers/{id}/test", routeAdmin, false}, {"indexers.categories", "POST", "/indexers/categories", routeAdmin, false}, {"download-clients.list", "GET", "/download-clients", routeAdmin, false}, {"download-clients.create", "POST", "/download-clients", routeAdmin, false}, {"download-clients.types", "GET", "/download-clients/types", routeAdmin, false}, {"download-clients.test-config", "POST", "/download-clients/test", routeAdmin, false}, {"download-clients.update", "PUT", "/download-clients/{id}", routeAdmin, false}, {"download-clients.delete", "DELETE", "/download-clients/{id}", routeAdmin, false}, {"download-clients.test", "POST", "/download-clients/{id}/test", routeAdmin, false},
	{"usenet-servers.list", "GET", "/usenet-servers", routeAdmin, false}, {"usenet-servers.create", "POST", "/usenet-servers", routeAdmin, false}, {"usenet-servers.test-config", "POST", "/usenet-servers/test", routeAdmin, false}, {"usenet-servers.get", "GET", "/usenet-servers/{id}", routeAdmin, false}, {"usenet-servers.update", "PUT", "/usenet-servers/{id}", routeAdmin, false}, {"usenet-servers.delete", "DELETE", "/usenet-servers/{id}", routeAdmin, false}, {"usenet-servers.test", "POST", "/usenet-servers/{id}/test", routeAdmin, false}, {"downloads.list", "GET", "/downloads", routeAdmin, false}, {"downloads.pause", "POST", "/downloads/{id}/pause", routeAdmin, false}, {"downloads.resume", "POST", "/downloads/{id}/resume", routeAdmin, false}, {"downloads.retry", "POST", "/downloads/{id}/retry", routeAdmin, false}, {"downloads.delete", "DELETE", "/downloads/{id}", routeAdmin, false}, {"downloads.insight", "GET", "/downloads/{id}/insight", routeAdmin, false}, {"downloads.limits", "PUT", "/downloads/{id}/limits", routeAdmin, false},
	{"images", "GET", "/images/{path...}", routeExempt, false}, {"import.list", "GET", "/import/queue", routeAdmin, false}, {"import.match", "POST", "/import/queue/{id}/match", routeAdmin, false}, {"import.delete", "DELETE", "/import/queue/{id}", routeAdmin, false}, {"events.list", "GET", "/events", routeAdmin, false},
	{"api.mount", routeMount, "/api/v1/", routeAdmin, false}, {"dlna.mount", routeMount, "/dlna/", routeExempt, false},
}

func policyForRegistration(method, path string) (routePolicy, bool) {
	for _, policy := range routePolicies {
		if policy.Method == method && policy.Path == path {
			return policy, true
		}
	}
	return routePolicy{}, false
}

func policyForRequest(method, path string) (routePolicy, bool) {
	if policy, ok := policyForRegistration(method, path); ok {
		return policy, true
	}
	for _, policy := range routePolicies {
		if policy.Method == method && routePathMatches(policy.Path, path) {
			return policy, true
		}
	}
	return routePolicy{}, false
}

func routePathMatches(pattern, path string) bool {
	patternSegments, pathParts := pathSegments(pattern), pathSegments(path)
	if len(patternSegments) != len(pathParts) {
		return len(patternSegments) == 2 && strings.HasSuffix(patternSegments[1], "...") && len(pathParts) >= 2 && patternSegments[0] == pathParts[0]
	}
	for i, segment := range patternSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}
		if segment != pathParts[i] {
			return false
		}
	}
	return true
}

type policyMux struct {
	mux           *http.ServeMux
	registrations []routeRegistration
}

func newPolicyMux() *policyMux { return &policyMux{mux: http.NewServeMux()} }

func (m *policyMux) HandleFunc(pattern string, handler http.HandlerFunc) {
	method, path := splitRoutePattern(pattern)
	policy, ok := policyForRegistration(method, path)
	if !ok {
		panic("api: route has no declarative policy: " + pattern)
	}
	m.mux.HandleFunc(pattern, handler)
	m.registrations = append(m.registrations, routeRegistration{Method: method, Path: path, Policy: policy})
}

func (m *policyMux) Handle(pattern string, handler http.Handler) {
	policy, ok := policyForRegistration(routeMount, pattern)
	if !ok {
		panic("api: mount has no declarative policy: " + pattern)
	}
	m.mux.Handle(pattern, handler)
	m.registrations = append(m.registrations, routeRegistration{Method: routeMount, Path: pattern, Policy: policy})
}

func (m *policyMux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

func splitRoutePattern(pattern string) (string, string) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return routeMount, pattern
	}
	return method, path
}
