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

func TestDefinitionPackRoutePoliciesRemainExactAndAdminOnly(t *testing.T) {
	want := map[string]string{
		"definition-packs.list":     http.MethodGet + " /definition-packs",
		"definition-packs.preview":  http.MethodPost + " /definition-packs/preview",
		"definition-packs.install":  http.MethodPost + " /definition-packs/install",
		"definition-packs.activate": http.MethodPost + " /definition-packs/activate",
		"definition-packs.rollback": http.MethodPost + " /definition-packs/rollback",
	}
	seen := make(map[string]bool, len(want))
	for _, policy := range routePolicies {
		exact, ok := want[policy.Name]
		if !ok {
			continue
		}
		seen[policy.Name] = true
		if got := policy.Method + " " + policy.Path; got != exact || policy.Access != routeAdmin || policy.Member {
			t.Fatalf("pack policy %q = %+v, want exact %q admin-only", policy.Name, policy, exact)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("definition pack policies found = %v, want all %v", seen, want)
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
		{http.MethodGet, "/libraries/4/access", false},
		{http.MethodPut, "/libraries/4/access", false},
		{http.MethodGet, "/library/movies", false},
		{http.MethodPost, "/library/episodes/7/search", false},
	} {
		if got := memberAllowed(test.method, test.path); got != test.want {
			t.Errorf("memberAllowed(%q, %q) = %v, want %v", test.method, test.path, got, test.want)
		}
	}
	episodeSearch, ok := policyForRegistration(http.MethodPost, "/library/episodes/{id}/search")
	if !ok || episodeSearch.Access != routeAdmin || episodeSearch.Member {
		t.Fatalf("episode search policy = %+v, %v; want registered admin-only policy", episodeSearch, ok)
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
	// The access routes are the module's replacement doors and they live OUTSIDE
	// the gated subtree, deliberately: an adult library's roster has to be
	// editable, and restriction is not an adult idea any more. They are
	// admin-only instead, by the ordinary rule.
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		policy, ok := policyForRegistration(method, "/libraries/{id}/access")
		if !ok || policy.Access != routeAdmin || policy.Member {
			t.Fatalf("%s /libraries/{id}/access policy = %+v, %v; want admin-only",
				method, policy, ok)
		}
	}
	// And nothing answers at the retired module switch, on any mux.
	if _, ok := policyForRegistration(http.MethodPost, "/settings/adult"); ok {
		t.Fatal("POST /settings/adult still has a policy row; the module switch is gone")
	}
	// Stash-box instance CRUD is admin metadata, not an adult-surface route:
	// Settings → Metadata has to reach it before the first adult library exists.
	for _, path := range []string{
		"/adult/stashbox-instances",
		"/adult/stashbox-instances/{id}",
		"/adult/stashbox-instances/test",
		"/adult/stashbox-instances/{id}/test",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			policy, ok := policyForRegistration(method, path)
			if !ok {
				continue
			}
			if policy.Access != routeAdmin || policy.Member {
				t.Errorf("%s %s policy = %+v; want admin-only", method, path, policy)
			}
		}
	}
}
