package cardigann

import (
	"slices"
	"strings"
	"testing"
)

func TestParseManifestClassifiesWithoutMakingUnsupportedDefinitionRunnable(t *testing.T) {
	manifest, err := ParseManifest("community", []byte(`
id: fixture
name: Fixture
type: private
links: [https://tracker.example]
login: {path: /login, method: oneurl}
search:
  paths:
    - path: /search
      method: post
      inputs: {q: "{{ .Keywords }}"}
      response: {type: xml}
  rows: {selector: item}
  fields:
    title: {selector: title}
    download: {selector: link}
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Ref.String() != "community:fixture" || manifest.Privacy != "private" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if manifest.Digest == "" || manifest.Revision != manifest.Digest {
		t.Fatalf("manifest digest/revision = %q / %q, want unversioned content pinned to its digest", manifest.Digest, manifest.Revision)
	}
	if manifest.Runnable {
		t.Fatal("unsupported manifest was marked runnable")
	}
	want := []CapabilityCode{
		UnsupportedLogin,
	}
	if !slices.Equal(manifest.Unsupported, want) {
		t.Fatalf("unsupported = %v, want %v", manifest.Unsupported, want)
	}
}

func TestParseManifestAcceptsCookieUserAgentAndFlareSolverrSettings(t *testing.T) {
	manifest, err := ParseManifest("managed", []byte(`
id: guarded-settings
name: Guarded Settings
links: [https://tracker.example]
settings:
  - {name: cookie, type: text}
  - {name: useragent, type: text}
  - {name: info_flaresolverr, type: info_flaresolverr}
caps: {modes: {search: [q]}}
search: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if !manifest.Runnable || len(manifest.Unsupported) != 0 {
		t.Fatalf("guarded settings manifest = %+v, want runnable", manifest)
	}
}

func TestParseManifestRejectsMultipleDocuments(t *testing.T) {
	_, err := ParseManifest("community", []byte("id: one\n---\nid: two\n"))
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("ParseManifest error = %v", err)
	}
}
