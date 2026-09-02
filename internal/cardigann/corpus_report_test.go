package cardigann

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestClassifyCorpusDirectoryReportsExactInertAndRunnableTotals(t *testing.T) {
	dir := canonicalTempDir(t)
	files := map[string]string{
		"z-unsupported.yml": `id: unsupported
name: Unsupported
links: [https://tracker.example]
login: {path: /login, method: oneurl}
`,
		"a-runnable.yml": `id: runnable
name: Runnable
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	report, err := ClassifyCorpusDirectory("external", dir)
	if err != nil {
		t.Fatalf("ClassifyCorpusDirectory: %v", err)
	}
	if report.Total != 2 || report.Runnable != 1 || report.Inert != 1 {
		t.Fatalf("totals = %+v, want total=2 runnable=1 inert=1", report)
	}
	if got, want := []string{report.Definitions[0].Path, report.Definitions[1].Path}, []string{"a-runnable.yml", "z-unsupported.yml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("definition order = %v, want %v", got, want)
	}
	if report.Definitions[0].State != DefinitionStateRunnableUnverified || report.Definitions[1].State != DefinitionStateUnsupported {
		t.Fatalf("states = %+v", report.Definitions)
	}
	if got := report.CapabilityHistogram[UnsupportedLogin]; got != 1 {
		t.Fatalf("login capability total = %d, want 1", got)
	}
}

func TestExternalV11CorpusReportIsReadOnlyAndExact(t *testing.T) {
	root := os.Getenv("CARAVAN_CARDIGANN_CORPUS")
	if root == "" {
		t.Skip("set CARAVAN_CARDIGANN_CORPUS to run the external corpus report")
	}
	report, err := ClassifyCorpusDirectory("external", root)
	if err != nil {
		t.Fatalf("ClassifyCorpusDirectory: %v", err)
	}
	if report.Total != 542 || report.Runnable+report.Inert != report.Total {
		t.Fatalf("report totals = %+v", report)
	}
	if len(report.Definitions) != report.Total || len(report.CapabilityHistogram) == 0 {
		t.Fatalf("report = %+v", report)
	}
	t.Logf("report total=%d runnable=%d inert=%d capability-codes=%d", report.Total, report.Runnable, report.Inert, len(report.CapabilityHistogram))
	codes := make([]string, 0, len(report.CapabilityHistogram))
	for code := range report.CapabilityHistogram {
		codes = append(codes, string(code))
	}
	sort.Strings(codes)
	for _, code := range codes {
		t.Logf("capability %s=%d", code, report.CapabilityHistogram[CapabilityCode(code)])
	}
	for _, definition := range report.Definitions {
		if definition.State == DefinitionStateRunnableUnverified {
			t.Logf("runnable %s", definition.Ref.String())
		}
	}
	documents, err := readCorpusDocuments(root)
	if err != nil {
		t.Fatalf("read compiler diagnostics corpus: %v", err)
	}
	for _, document := range documents {
		manifest, manifestErr := ParseManifest("external", document.Data)
		if manifestErr != nil || !containsCapability(manifest.Unsupported, "compiler.invalid") {
			continue
		}
		if _, compileErr := ParseDefinition(document.Data); compileErr != nil {
			t.Logf("compiler %s: %v", document.Path, compileErr)
		}
	}
}
