package api

import (
	"net/http"
	"testing"
)

func TestRoutePolicyManifest(t *testing.T) {
	seen := make(map[string]bool, len(routePolicies))
	mux := newPolicyMux()
	for _, policy := range routePolicies {
		key := policy.Method + " " + policy.Path
		if seen[key] {
			t.Fatalf("duplicate policy %q", key)
		}
		seen[key] = true
		if policy.Name == "" {
			t.Errorf("%q has no stable name", key)
		}
		switch policy.Access {
		case routeAdmin, routeMember, routeAdult, routeExempt:
		default:
			t.Errorf("%q has unknown access %q", key, policy.Access)
		}
		if policy.Method == routeMount {
			mux.Handle(policy.Path, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			continue
		}
		mux.HandleFunc(policy.Method+" "+policy.Path, func(http.ResponseWriter, *http.Request) {})
	}
	if len(mux.registrations) != len(routePolicies) {
		t.Fatalf("registered %d policies, want %d", len(mux.registrations), len(routePolicies))
	}
}

func TestRoutePolicyMemberAndExemptionMatching(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/discover", true},
		{http.MethodGet, "/discover/movie/7", true},
		{http.MethodDelete, "/requests/12", true},
		{http.MethodGet, "/adult/sites/4", true},
		{http.MethodPost, "/adult/sites", false},
		{http.MethodPost, "/settings/adult", false},
		{http.MethodGet, "/library/movies", false},
	} {
		if got := memberAllowed(test.method, test.path); got != test.want {
			t.Errorf("memberAllowed(%q, %q) = %v, want %v", test.method, test.path, got, test.want)
		}
	}
	for _, path := range []string{"/auth/login", "/auth/logout", "/calendar.ics", "/images/poster.jpg"} {
		if !authExempt(path) {
			t.Errorf("authExempt(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"/settings/password", "/images", "/image/poster.jpg"} {
		if authExempt(path) {
			t.Errorf("authExempt(%q) = true, want false", path)
		}
	}
}

func TestRoutePolicyAdultSurfaceIsClosed(t *testing.T) {
	for _, policy := range routePolicies {
		if policy.Access != routeAdult || policy.Method == routeMount {
			continue
		}
		if len(policy.Path) < len("/adult/") || policy.Path[:len("/adult/")] != "/adult/" {
			t.Errorf("adult policy %q is outside the gated subtree", policy.Name)
		}
	}
	if policy, ok := policyForRegistration(http.MethodPost, "/settings/adult"); !ok || policy.Access != routeAdmin {
		t.Fatal("POST /settings/adult must remain admin-only outside the adult gate")
	}
}
