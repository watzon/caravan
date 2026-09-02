package cardigann

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CapabilityCode identifies one definition behavior Caravan intentionally does
// not execute. Codes are stable machine-readable values, never copied source
// prose, and are sorted in manifests for deterministic cataloging.
type CapabilityCode string

const (
	UnsupportedLogin                    CapabilityCode = "login"
	UnsupportedSearchPathInput          CapabilityCode = "search.paths.inputs"
	UnsupportedSearchPathMethodPost     CapabilityCode = "search.paths.method.post"
	UnsupportedSearchPathResponseXML    CapabilityCode = "unsupported.search.paths.response.xml"
	UnsupportedSearchPathHeaders        CapabilityCode = "search.paths.headers"
	UnsupportedFilesystemInterpolation  CapabilityCode = "interpolation.filesystem"
	UnsupportedEnvironmentInterpolation CapabilityCode = "interpolation.environment"
	UnsupportedScript                   CapabilityCode = "script"
	UnsupportedCookieSetting            CapabilityCode = "login.cookie"
	UnsupportedUserAgentSetting         CapabilityCode = "settings.useragent"
)

// Manifest is inert metadata decoded from a definition. A manifest is never an
// executable definition; Runnable only says the same bytes pass the current
// explicit compiler and have no classified unsupported capabilities.
type Manifest struct {
	Ref         DefinitionRef
	Revision    string
	Digest      string
	Privacy     string
	Unsupported []CapabilityCode
	Runnable    bool
}

// ParseManifest decodes exactly one YAML document and classifies every
// functional construct it recognizes. It never executes templates, includes,
// scripts, filesystem reads, or environment interpolation.
func ParseManifest(source string, data []byte) (Manifest, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Manifest{}, fmt.Errorf("manifest source is empty")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return malformedManifest(source, data, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest must contain exactly one YAML document")
	}
	count := 0
	if err := validateYAMLGraph(&document, 0, &count); err != nil {
		return Manifest{}, err
	}
	root := documentRoot(&document)
	if root == nil || root.Kind != yaml.MappingNode {
		return Manifest{}, fmt.Errorf("manifest root must be a mapping")
	}
	id := scalarValue(mappingValue(root, "id"))
	if id == "" {
		return Manifest{}, fmt.Errorf("manifest requires id")
	}
	ref, err := ParseDefinitionRef(source + ":" + id)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest identity: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	revision := firstScalar(mappingValue(root, "revision"), mappingValue(root, "version"))
	if revision == "" {
		revision = digest
	}
	manifest := Manifest{
		Ref:      ref,
		Digest:   digest,
		Revision: revision,
		Privacy:  strings.ToLower(scalarValue(mappingValue(root, "type"))),
	}
	codes := classifyManifest(root)
	if _, err := ParseDefinition(data); err != nil && len(codes) == 0 {
		codes = append(codes, CapabilityCode("compiler.invalid"))
	}
	manifest.Unsupported = sortedCodes(codes)
	manifest.Runnable = len(manifest.Unsupported) == 0
	return manifest, nil
}

func malformedManifest(source string, data []byte, decodeErr error) (Manifest, error) {
	var id, privacy, revision string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		for _, target := range []struct {
			key  string
			into *string
		}{{"id:", &id}, {"type:", &privacy}, {"revision:", &revision}, {"version:", &revision}} {
			if strings.HasPrefix(line, target.key) && *target.into == "" {
				*target.into = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, target.key)), "\"'")
			}
		}
	}
	if id == "" {
		return Manifest{}, fmt.Errorf("decode manifest: %w", decodeErr)
	}
	ref, err := ParseDefinitionRef(source + ":" + id)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest identity: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if revision == "" {
		revision = digest
	}
	return Manifest{Ref: ref, Revision: revision, Digest: digest, Privacy: strings.ToLower(privacy), Unsupported: []CapabilityCode{"syntax.invalid"}}, nil
}

func documentRoot(document *yaml.Node) *yaml.Node {
	if document != nil && document.Kind == yaml.DocumentNode && len(document.Content) == 1 {
		return document.Content[0]
	}
	return nil
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func firstScalar(nodes ...*yaml.Node) string {
	for _, node := range nodes {
		if value := scalarValue(node); value != "" {
			return value
		}
	}
	return ""
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func classifyManifest(root *yaml.Node) []CapabilityCode {
	codes := make([]CapabilityCode, 0)
	allowedRoot := set("id", "name", "description", "language", "type", "encoding", "links", "legacylinks", "replaces", "testlinktorrent", "requestDelay", "followredirect", "settings", "caps", "search", "revision", "version")
	forEachMapping(root, func(key string, value *yaml.Node) {
		switch key {
		case "login":
			codes = append(codes, classifyLogin(value)...)
		case "download":
			codes = append(codes, classifyDownload(value)...)
		case "script", "scripts":
			codes = append(codes, UnsupportedScript)
		case "include", "includes", "file", "files":
			codes = append(codes, UnsupportedFilesystemInterpolation)
		case "env", "environment":
			codes = append(codes, UnsupportedEnvironmentInterpolation)
		default:
			if !allowedRoot[key] {
				codes = append(codes, CapabilityCode("unknown.root."+key))
			}
		}
		if key == "caps" {
			codes = append(codes, classifyCaps(value)...)
		}
		if key == "search" {
			codes = append(codes, classifySearch(value)...)
		}
	})
	return codes
}

func classifyLogin(node *yaml.Node) []CapabilityCode {
	if node == nil || node.Kind != yaml.MappingNode {
		return []CapabilityCode{UnsupportedLogin}
	}
	method := strings.ToLower(scalarValue(mappingValue(node, "method")))
	if method == "" {
		method = "form"
	}
	if method != "cookie" && method != "get" && method != "post" && method != "form" {
		return []CapabilityCode{UnsupportedLogin}
	}
	allowed := set("path", "method", "form", "submitpath", "inputs", "selectorinputs", "selectors", "cookies", "headers", "test", "error", "captcha")
	unsupported := false
	forEachMapping(node, func(key string, _ *yaml.Node) {
		if !allowed[key] {
			unsupported = true
		}
	})
	if unsupported {
		return []CapabilityCode{UnsupportedLogin}
	}
	return nil
}

func classifyDownload(node *yaml.Node) []CapabilityCode {
	unsupported := CapabilityCode("unknown.root.download")
	if node == nil || node.Kind != yaml.MappingNode {
		return []CapabilityCode{unsupported}
	}
	before := mappingValue(node, "before")
	methodNode := mappingValue(node, "method")
	headers := mappingValue(node, "headers")
	selectors := mappingValue(node, "selectors")
	infoHash := mappingValue(node, "infohash")
	if selectors != nil && infoHash != nil || before == nil && methodNode == nil && headers == nil && selectors == nil && infoHash == nil {
		return []CapabilityCode{unsupported}
	}
	valid := true
	forEachMapping(node, func(key string, _ *yaml.Node) {
		if key != "before" && key != "method" && key != "headers" && key != "selectors" && key != "infohash" {
			valid = false
		}
	})
	method := strings.ToLower(scalarValue(methodNode))
	if method != "" && method != "get" && method != "post" {
		valid = false
	}
	if headers != nil && headers.Kind != yaml.MappingNode {
		valid = false
	}
	if before != nil {
		if before.Kind != yaml.MappingNode || len(unknownMappingCodes(before, "download.before", set("path", "pathselector", "method", "inputs"))) != 0 {
			valid = false
		} else {
			path := mappingValue(before, "path")
			pathSelector := mappingValue(before, "pathselector")
			if (path == nil) == (pathSelector == nil) {
				valid = false
			}
			method := strings.ToLower(scalarValue(mappingValue(before, "method")))
			if method != "" && method != "get" && method != "post" {
				valid = false
			}
			if inputs := mappingValue(before, "inputs"); inputs != nil && inputs.Kind != yaml.MappingNode {
				valid = false
			}
			if pathSelector != nil {
				if pathSelector.Kind != yaml.MappingNode || mappingValue(pathSelector, "selector") == nil || len(unknownMappingCodes(pathSelector, "download.before.pathselector", set("selector", "attribute", "filters"))) != 0 {
					valid = false
				}
			}
		}
	}
	if selectors != nil {
		if selectors.Kind != yaml.SequenceNode || len(selectors.Content) == 0 {
			valid = false
		}
		for _, selector := range selectors.Content {
			if len(unknownMappingCodes(selector, "download.selectors", set("selector", "attribute", "filters", "usebeforeresponse"))) != 0 {
				valid = false
			}
		}
	}
	if infoHash != nil {
		if len(unknownMappingCodes(infoHash, "download.infohash", set("hash", "title", "usebeforeresponse"))) != 0 || mappingValue(infoHash, "hash") == nil {
			valid = false
		}
		for _, name := range []string{"hash", "title"} {
			field := mappingValue(infoHash, name)
			if field != nil && len(unknownMappingCodes(field, "download.infohash."+name, set("selector", "attribute", "filters", "text"))) != 0 {
				valid = false
			}
		}
	}
	if !valid {
		return []CapabilityCode{unsupported}
	}
	return nil
}

func classifyCaps(node *yaml.Node) []CapabilityCode {
	allowed := set("categorymappings", "categories", "modes", "allowrawsearch", "allowtvsearchimdb")
	return unknownMappingCodes(node, "caps", allowed)
}

func classifySearch(node *yaml.Node) []CapabilityCode {
	allowed := set("path", "paths", "inputs", "headers", "allowEmptyInputs", "rows", "fields", "keywordsfilters", "error")
	codes := unknownMappingCodes(node, "search", allowed)
	paths := mappingValue(node, "paths")
	if paths == nil || paths.Kind != yaml.SequenceNode {
		return codes
	}
	for _, path := range paths.Content {
		codes = append(codes, classifySearchPath(path)...)
	}
	return codes
}

func classifySearchPath(node *yaml.Node) []CapabilityCode {
	allowed := set("path", "method", "categories", "followredirect", "response", "inputs", "headers")
	codes := unknownMappingCodes(node, "search.paths", allowed)
	if method := strings.ToLower(scalarValue(mappingValue(node, "method"))); method != "" && method != "get" && !strings.Contains(method, "{{") {
		if method != "post" {
			codes = append(codes, CapabilityCode("unsupported.search.paths.method."+method))
		}
	}
	response := mappingValue(node, "response")
	if response != nil {
		codes = append(codes, unknownMappingCodes(response, "search.paths.response", set("type", "noResultsMessage"))...)
		if responseType := strings.ToLower(scalarValue(mappingValue(response, "type"))); responseType != "" && responseType != "html" && responseType != "json" && responseType != "xml" {
			codes = append(codes, CapabilityCode("unsupported.search.paths.response."+responseType))
		}
	}
	return codes
}

func unknownMappingCodes(node *yaml.Node, prefix string, allowed map[string]bool) []CapabilityCode {
	codes := make([]CapabilityCode, 0)
	forEachMapping(node, func(key string, _ *yaml.Node) {
		if !allowed[key] {
			codes = append(codes, CapabilityCode("unknown."+prefix+"."+key))
		}
	})
	return codes
}

func forEachMapping(node *yaml.Node, fn func(string, *yaml.Node)) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		fn(node.Content[i].Value, node.Content[i+1])
	}
}

func set(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedCodes(codes []CapabilityCode) []CapabilityCode {
	seen := make(map[CapabilityCode]bool, len(codes))
	out := make([]CapabilityCode, 0, len(codes))
	for _, code := range codes {
		if code != "" {
			seen[code] = true
		}
	}
	for code := range seen {
		out = append(out, code)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
