// Package cardigann turns declarative tracker definitions into local search
// clients. It is intentionally independent from the Torznab transport: callers
// can use the same engine directly inside Caravan or expose it through a feed.
package cardigann

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"text/template"

	"github.com/watzon/caravan/internal/core"
	"golang.org/x/net/html/charset"
	"gopkg.in/yaml.v3"
)

// Definition is the supported subset of the Cardigann v11 definition format.
// Unsupported fields are rejected by ParseDefinition so a definition cannot be
// advertised when Caravan would silently ignore part of its behavior.
type Definition struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	Description     string            `yaml:"description"`
	Language        string            `yaml:"language"`
	Type            string            `yaml:"type"`
	Encoding        string            `yaml:"encoding"`
	Links           []string          `yaml:"links"`
	LegacyLinks     []string          `yaml:"legacylinks"`
	Replaces        []string          `yaml:"replaces"`
	TestLinkTorrent bool              `yaml:"testlinktorrent"`
	RequestDelay    float64           `yaml:"requestDelay"`
	FollowRedirect  *bool             `yaml:"followredirect"`
	Settings        []settingBlock    `yaml:"settings"`
	Caps            capabilitiesBlock `yaml:"caps"`
	Login           *loginBlock       `yaml:"login"`
	Download        *downloadBlock    `yaml:"download"`
	Search          searchBlock       `yaml:"search"`

	// approvedOrigins is populated only by a verified signed-pack provider.
	// It restricts configured bases, templates, and redirects beyond links.
	approvedOrigins []string
	sourceRevision  string
	sourceDigest    string
}

type settingBlock struct {
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"`
	Label   string         `yaml:"label"`
	Default any            `yaml:"default"`
	Options settingOptions `yaml:"options"`
}

// SettingOption is one value exposed by a Cardigann select field. Definition
// order is preserved so the UI does not rely on Go map iteration.
type SettingOption struct {
	Value string
	Label string
}

type settingOptions []SettingOption

func (options *settingOptions) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Kind == 0 {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("setting options must be a mapping")
	}
	if len(node.Content)/2 > 512 {
		return fmt.Errorf("setting has too many options")
	}
	out := make([]SettingOption, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
			return fmt.Errorf("setting option values must be scalar")
		}
		out = append(out, SettingOption{Value: strings.TrimSpace(key.Value), Label: strings.TrimSpace(value.Value)})
	}
	*options = out
	return nil
}

// SettingSchema is the non-secret UI contract for one definition setting.
// Configured values remain write-only.
type SettingSchema struct {
	Name     string
	Label    string
	Type     string
	Default  string
	Options  []SettingOption
	Secret   bool
	Editable bool
}

type capabilitiesBlock struct {
	CategoryMappings  []categoryMapping   `yaml:"categorymappings"`
	Categories        map[string]string   `yaml:"categories"`
	Modes             map[string][]string `yaml:"modes"`
	AllowRawSearch    bool                `yaml:"allowrawsearch"`
	AllowTVSearchIMDb bool                `yaml:"allowtvsearchimdb"`
}

type categoryMapping struct {
	ID      any    `yaml:"id"`
	Cat     string `yaml:"cat"`
	Desc    string `yaml:"desc"`
	Default bool   `yaml:"default"`
}

type searchBlock struct {
	Path             string                    `yaml:"path"`
	Paths            []pathBlock               `yaml:"paths"`
	Inputs           map[string]string         `yaml:"inputs"`
	Headers          map[string]headerTemplate `yaml:"headers"`
	AllowEmptyInputs bool                      `yaml:"allowEmptyInputs"`
	KeywordFilters   []filterBlock             `yaml:"keywordsfilters"`
	Error            []loginErrorBlock         `yaml:"error"`
	Rows             rowsBlock                 `yaml:"rows"`
	Fields           map[string]fieldBlock     `yaml:"fields"`
}

type loginBlock struct {
	Path           string                    `yaml:"path"`
	Method         string                    `yaml:"method"`
	Captcha        *loginCaptchaBlock        `yaml:"captcha"`
	Form           string                    `yaml:"form"`
	SubmitPath     string                    `yaml:"submitpath"`
	Inputs         map[string]string         `yaml:"inputs"`
	SelectorInputs map[string]fieldBlock     `yaml:"selectorinputs"`
	Selectors      bool                      `yaml:"selectors"`
	Cookies        []string                  `yaml:"cookies"`
	Headers        map[string]headerTemplate `yaml:"headers"`
	Test           loginTestBlock            `yaml:"test"`
	Error          []loginErrorBlock         `yaml:"error"`
}

// loginCaptchaBlock names the captcha a form login may show. Caravan cannot
// solve one; the engine detects it and points the user at cookie login.
type loginCaptchaBlock struct {
	Type     string `yaml:"type"`
	Selector string `yaml:"selector"`
	Input    string `yaml:"input"`
}

type loginTestBlock struct {
	Path     string `yaml:"path"`
	Selector string `yaml:"selector"`
}

type loginErrorBlock struct {
	Selector string     `yaml:"selector"`
	Message  fieldBlock `yaml:"message"`
}

type downloadBlock struct {
	Before    *downloadBeforeBlock      `yaml:"before"`
	Method    string                    `yaml:"method"`
	Headers   map[string]headerTemplate `yaml:"headers"`
	Selectors []fieldBlock              `yaml:"selectors"`
	InfoHash  *downloadInfoHashBlock    `yaml:"infohash"`
}

type downloadBeforeBlock struct {
	Path         string            `yaml:"path"`
	PathSelector fieldBlock        `yaml:"pathselector"`
	Method       string            `yaml:"method"`
	Inputs       map[string]string `yaml:"inputs"`
}

type downloadInfoHashBlock struct {
	UseBeforeResponse bool       `yaml:"usebeforeresponse"`
	Hash              fieldBlock `yaml:"hash"`
	Title             fieldBlock `yaml:"title"`
}

type pathBlock struct {
	Path           string                    `yaml:"path"`
	Method         string                    `yaml:"method"`
	Categories     []string                  `yaml:"categories"`
	FollowRedirect *bool                     `yaml:"followredirect"`
	Inputs         map[string]string         `yaml:"inputs"`
	Headers        map[string]headerTemplate `yaml:"headers"`
	Response       responseBlock             `yaml:"response"`
}

type headerTemplate string

func (value *headerTemplate) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return fmt.Errorf("header template is missing")
	}
	switch node.Kind {
	case yaml.ScalarNode:
		*value = headerTemplate(node.Value)
		return nil
	case yaml.SequenceNode:
		if len(node.Content) != 1 || node.Content[0].Kind != yaml.ScalarNode {
			return fmt.Errorf("header template list must contain exactly one scalar")
		}
		*value = headerTemplate(node.Content[0].Value)
		return nil
	default:
		return fmt.Errorf("header template must be a scalar or single-value list")
	}
}

type responseBlock struct {
	Type             string `yaml:"type"`
	NoResultsMessage string `yaml:"noResultsMessage"`
}

type rowsBlock struct {
	Selector                        string        `yaml:"selector"`
	Attribute                       string        `yaml:"attribute"`
	Multiple                        bool          `yaml:"multiple"`
	MissingAttributeEqualsNoResults bool          `yaml:"missingAttributeEqualsNoResults"`
	After                           int           `yaml:"after"`
	Count                           countBlock    `yaml:"count"`
	Filters                         []filterBlock `yaml:"filters"`
	DateHeaders                     fieldBlock    `yaml:"dateheaders"`
}

type countBlock struct {
	Selector string `yaml:"selector"`
}

type fieldBlock struct {
	Selector          string        `yaml:"selector"`
	Attribute         string        `yaml:"attribute"`
	Remove            string        `yaml:"remove"`
	Text              string        `yaml:"text"`
	Default           string        `yaml:"default"`
	Optional          bool          `yaml:"optional"`
	Case              caseBlock     `yaml:"case"`
	Filters           []filterBlock `yaml:"filters"`
	UseBeforeResponse bool          `yaml:"usebeforeresponse"`
	deferredFilters   []filterBlock
}

type filterBlock struct {
	Name string `yaml:"name"`
	Args any    `yaml:"args"`
}

// Capabilities is the local, protocol-neutral view used to render t=caps and
// populate Caravan's category picker.
type Capabilities struct {
	Categories []core.IndexerCategory
	Modes      map[string]bool
}

// ParseDefinition decodes and validates the minimum contract needed to run a
// public search definition.
func ParseDefinition(src []byte) (*Definition, error) {
	if err := validateYAMLSource(src); err != nil {
		return nil, fmt.Errorf("parse definition: %w", err)
	}
	var def Definition
	decoder := yaml.NewDecoder(bytes.NewReader(src))
	decoder.KnownFields(true)
	if err := decoder.Decode(&def); err != nil {
		return nil, fmt.Errorf("decode definition: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("definition must contain exactly one YAML document")
	}
	def.ID = strings.TrimSpace(def.ID)
	def.Name = strings.TrimSpace(def.Name)
	def.Encoding = strings.TrimSpace(def.Encoding)
	if def.Encoding == "" {
		def.Encoding = "UTF-8"
	}
	if _, canonical := charset.Lookup(def.Encoding); canonical == "" {
		return nil, fmt.Errorf("definition %s has unsupported encoding %q", def.ID, def.Encoding)
	} else {
		def.Encoding = canonical
	}
	if def.ID == "" || def.Name == "" {
		return nil, fmt.Errorf("definition requires id and name")
	}
	if len(def.Links) == 0 {
		return nil, fmt.Errorf("definition %s has no links", def.ID)
	}
	if len(def.Links) > 64 || len(def.LegacyLinks) > 64 || len(def.Replaces) > 64 {
		return nil, fmt.Errorf("definition %s has excessive metadata entries", def.ID)
	}
	if def.RequestDelay < 0 || def.RequestDelay > 60 {
		return nil, fmt.Errorf("definition %s request delay must be between 0 and 60 seconds", def.ID)
	}
	if err := validateLoginBlock(def.Login); err != nil {
		return nil, fmt.Errorf("definition %s login: %w", def.ID, err)
	}
	def.Settings = withSessionCookieSetting(def.Login, def.Settings)
	if err := validateDownloadBlock(def.Download); err != nil {
		return nil, fmt.Errorf("definition %s download: %w", def.ID, err)
	}
	for _, raw := range append(append([]string(nil), def.Links...), def.LegacyLinks...) {
		parsed, parseErr := url.Parse(strings.TrimSpace(raw))
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, fmt.Errorf("definition %s has an invalid tracker link", def.ID)
		}
	}
	if strings.TrimSpace(def.Search.Path) != "" {
		if len(def.Search.Paths) != 0 {
			return nil, fmt.Errorf("definition %s declares both search path and paths", def.ID)
		}
		def.Search.Paths = []pathBlock{{Path: def.Search.Path}}
	}
	if len(def.Search.Paths) == 0 || strings.TrimSpace(def.Search.Rows.Selector) == "" {
		return nil, fmt.Errorf("definition %s has no runnable search", def.ID)
	}
	if _, ok := def.Search.Fields["title"]; !ok {
		return nil, fmt.Errorf("definition %s has no title field", def.ID)
	}
	_, hasDownload := def.Search.Fields["download"]
	_, hasMagnet := def.Search.Fields["magnet"]
	_, hasInfoHash := def.Search.Fields["infohash"]
	if !hasDownload && !hasMagnet && !hasInfoHash {
		return nil, fmt.Errorf("definition %s has neither a download, magnet, nor infohash field", def.ID)
	}
	if err := validateSupportedDefinition(&def); err != nil {
		return nil, fmt.Errorf("definition %s: %w", def.ID, err)
	}
	return &def, nil
}

func validateLoginBlock(login *loginBlock) error {
	if login == nil {
		return nil
	}
	method := loginMethod(login)
	switch method {
	case "cookie":
		if strings.TrimSpace(login.Inputs["cookie"]) == "" {
			return fmt.Errorf("cookie method requires a cookie input")
		}
	case "get", "post":
		if strings.TrimSpace(login.Path) == "" {
			return fmt.Errorf("%s method requires a path", method)
		}
	case "form":
		if strings.TrimSpace(login.Path) == "" {
			return fmt.Errorf("form method requires a path")
		}
	default:
		return fmt.Errorf("unsupported method %q", login.Method)
	}
	if len(login.Inputs) > 128 || len(login.SelectorInputs) > 32 || len(login.Cookies) > 32 || len(login.Headers) > 64 || len(login.Error) > 32 {
		return fmt.Errorf("exceeds bounded login metadata")
	}
	if (len(login.SelectorInputs) > 0 || login.Selectors || login.SubmitPath != "" || login.Captcha != nil) && method != "form" {
		return fmt.Errorf("selector inputs, selector keys, captcha, and submit path require form method")
	}
	if login.Captcha != nil {
		captchaType := strings.ToLower(strings.TrimSpace(login.Captcha.Type))
		if captchaType != "" && captchaType != "image" && captchaType != "text" {
			return fmt.Errorf("unsupported captcha type %q", login.Captcha.Type)
		}
		if strings.TrimSpace(login.Captcha.Selector) == "" {
			return fmt.Errorf("captcha requires a selector")
		}
	}
	for name, source := range login.Inputs {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("input has an invalid name")
		}
		if _, err := template.New("login-input").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source)); err != nil {
			return fmt.Errorf("input %q template: %w", name, err)
		}
	}
	if login.Path != "" {
		if _, err := template.New("login-path").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(login.Path)); err != nil {
			return fmt.Errorf("path template: %w", err)
		}
	}
	if login.SubmitPath != "" {
		if _, err := template.New("login-submit-path").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(login.SubmitPath)); err != nil {
			return fmt.Errorf("submit path template: %w", err)
		}
	}
	for name, field := range login.SelectorInputs {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(field.Selector) == "" {
			return fmt.Errorf("selector input is invalid")
		}
		if err := validateFilters(field.Filters); err != nil {
			return fmt.Errorf("selector input %q filters: %w", name, err)
		}
	}
	for _, cookie := range login.Cookies {
		if strings.TrimSpace(cookie) == "" || len(cookie) > maxConfiguredSettingBytes || strings.ContainsAny(cookie, "\r\n") {
			return fmt.Errorf("login cookie is invalid")
		}
	}
	for name, source := range login.Headers {
		canonical := strings.ToLower(strings.TrimSpace(name))
		if name != strings.TrimSpace(name) || !supportedDefinitionHeader(canonical) {
			return fmt.Errorf("unsupported header %q", name)
		}
		if _, err := template.New("login-header").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(string(source))); err != nil {
			return fmt.Errorf("header %q template: %w", name, err)
		}
	}
	if (login.Test.Path == "") != (login.Test.Selector == "") {
		return fmt.Errorf("test requires both path and selector")
	}
	for _, rule := range login.Error {
		if strings.TrimSpace(rule.Selector) == "" {
			return fmt.Errorf("error selector is empty")
		}
	}
	return nil
}

func validateDownloadBlock(download *downloadBlock) error {
	if download == nil {
		return nil
	}
	if len(download.Selectors) > 0 && download.InfoHash != nil {
		return fmt.Errorf("selectors and infohash are mutually exclusive")
	}
	if download.Before == nil && len(download.Selectors) == 0 && download.InfoHash == nil && strings.TrimSpace(download.Method) == "" && len(download.Headers) == 0 {
		return fmt.Errorf("download block has no behavior")
	}
	method := strings.ToLower(strings.TrimSpace(download.Method))
	if method != "" && method != "get" && method != "post" {
		return fmt.Errorf("download method %q is unsupported", download.Method)
	}
	if len(download.Headers) > 64 {
		return fmt.Errorf("download has too many headers")
	}
	for name, source := range download.Headers {
		canonical := strings.ToLower(strings.TrimSpace(name))
		if name != strings.TrimSpace(name) || !supportedDefinitionHeader(canonical) {
			return fmt.Errorf("unsupported download header %q", name)
		}
		if _, err := template.New("download-header").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(string(source))); err != nil {
			return fmt.Errorf("download header %q template: %w", name, err)
		}
	}
	if before := download.Before; before != nil {
		hasPath := strings.TrimSpace(before.Path) != ""
		hasPathSelector := strings.TrimSpace(before.PathSelector.Selector) != ""
		if hasPath == hasPathSelector {
			return fmt.Errorf("before requires exactly one of path or pathselector")
		}
		method := strings.ToLower(strings.TrimSpace(before.Method))
		if method != "" && method != "get" && method != "post" {
			return fmt.Errorf("before method %q is unsupported", before.Method)
		}
		if len(before.Inputs) > 64 {
			return fmt.Errorf("before has too many inputs")
		}
		if hasPath {
			if _, err := template.New("download-before-path").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(before.Path)); err != nil {
				return fmt.Errorf("before path template: %w", err)
			}
		}
		if hasPathSelector {
			if err := validateDownloadField(before.PathSelector, "before path selector"); err != nil {
				return err
			}
		}
		for name, source := range before.Inputs {
			if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
				return fmt.Errorf("before input has an invalid name")
			}
			if _, err := template.New("download-before-input").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source)); err != nil {
				return fmt.Errorf("before input %q template: %w", name, err)
			}
		}
	}
	if len(download.Selectors) > 16 {
		return fmt.Errorf("selectors must contain at most 16 entries")
	}
	for i, selector := range download.Selectors {
		if err := validateDownloadField(selector, fmt.Sprintf("selector %d", i)); err != nil {
			return err
		}
		if selector.UseBeforeResponse && download.Before == nil {
			return fmt.Errorf("selector %d requires a before response", i)
		}
	}
	if download.InfoHash != nil {
		if download.InfoHash.UseBeforeResponse && download.Before == nil {
			return fmt.Errorf("infohash requires a before response")
		}
		if strings.TrimSpace(download.InfoHash.Hash.Selector) == "" && strings.TrimSpace(download.InfoHash.Hash.Text) == "" {
			return fmt.Errorf("infohash hash field is empty")
		}
		if err := validateFilters(download.InfoHash.Hash.Filters); err != nil {
			return fmt.Errorf("infohash hash filters: %w", err)
		}
		if err := validateFilters(download.InfoHash.Title.Filters); err != nil {
			return fmt.Errorf("infohash title filters: %w", err)
		}
	}
	return nil
}

func validateDownloadField(field fieldBlock, label string) error {
	if strings.TrimSpace(field.Selector) == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if _, err := template.New("download-field").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(field.Selector)); err != nil {
		return fmt.Errorf("%s template: %w", label, err)
	}
	if err := validateFilters(field.Filters); err != nil {
		return fmt.Errorf("%s filters: %w", label, err)
	}
	return nil
}

func validateSupportedDefinition(def *Definition) error {
	if len(def.Settings) > 64 {
		return fmt.Errorf("too many settings")
	}
	settingNames := make(map[string]struct{}, len(def.Settings))
	for _, setting := range def.Settings {
		name := strings.TrimSpace(setting.Name)
		if name == "" || name != setting.Name || name == "sitelink" {
			return fmt.Errorf("invalid setting name")
		}
		if _, exists := settingNames[name]; exists {
			return fmt.Errorf("duplicate setting name")
		}
		settingNames[name] = struct{}{}
		settingType, editable := normalizeSettingType(setting.Type)
		if settingType == "" {
			return fmt.Errorf("setting %q type %q is unsupported", name, setting.Type)
		}
		if settingType == "select" && len(setting.Options) == 0 {
			return fmt.Errorf("setting %q select requires options", name)
		}
		if settingType != "select" && len(setting.Options) != 0 {
			return fmt.Errorf("setting %q options require select type", name)
		}
		_ = editable
	}
	if len(def.Caps.Modes) > 16 {
		return fmt.Errorf("too many capability modes")
	}
	if len(def.Caps.CategoryMappings) > 4096 {
		return fmt.Errorf("too many category mappings")
	}
	if len(def.Search.Paths) > 16 {
		return fmt.Errorf("too many search paths")
	}
	if len(def.Search.Fields) > 128 {
		return fmt.Errorf("too many search fields")
	}
	if len(def.Search.Inputs) > 128 {
		return fmt.Errorf("too many shared search inputs")
	}
	for name, source := range def.Search.Inputs {
		if _, err := template.New("shared-search-input").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source)); err != nil {
			return fmt.Errorf("shared search input %q template: %w", name, err)
		}
	}
	if len(def.Search.Headers) > 64 {
		return fmt.Errorf("too many shared search headers")
	}
	sharedHeaderNames := make(map[string]struct{}, len(def.Search.Headers))
	for name, source := range def.Search.Headers {
		trimmed := strings.TrimSpace(name)
		if name != trimmed {
			return fmt.Errorf("shared search header name must be canonical")
		}
		canonical := strings.ToLower(trimmed)
		if _, exists := sharedHeaderNames[canonical]; exists {
			return fmt.Errorf("shared search has duplicate header %q", canonical)
		}
		sharedHeaderNames[canonical] = struct{}{}
		if !supportedDefinitionHeader(canonical) {
			return fmt.Errorf("shared search header %q is unsupported", name)
		}
		if _, err := template.New("shared-search-header").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(string(source))); err != nil {
			return fmt.Errorf("shared search header %q template: %w", name, err)
		}
	}
	if err := validateFilters(def.Search.KeywordFilters); err != nil {
		return fmt.Errorf("keyword filters: %w", err)
	}
	if def.Search.Rows.After < 0 || def.Search.Rows.After > maxSearchResultRows {
		return fmt.Errorf("row after count must be between 0 and %d", maxSearchResultRows)
	}
	if err := validateFilters(def.Search.Rows.DateHeaders.Filters); err != nil {
		return fmt.Errorf("row date headers: %w", err)
	}
	if err := validateRowFilters(def.Search.Rows.Filters); err != nil {
		return fmt.Errorf("row filters: %w", err)
	}
	for _, rule := range def.Search.Error {
		if strings.TrimSpace(rule.Selector) == "" {
			return fmt.Errorf("search error selector is empty")
		}
		if err := validateFilters(rule.Message.Filters); err != nil {
			return fmt.Errorf("search error message filters: %w", err)
		}
	}
	for i, searchPath := range def.Search.Paths {
		if len(searchPath.Categories) > 4096 {
			return fmt.Errorf("search path %d has too many categories", i)
		}
		for _, category := range searchPath.Categories {
			if strings.TrimSpace(category) == "" {
				return fmt.Errorf("search path %d has an empty category", i)
			}
		}
		if len(searchPath.Response.NoResultsMessage) > maxExtractedFieldBytes {
			return fmt.Errorf("search path %d no-results message is too large", i)
		}
		if len(searchPath.Inputs) > 128 {
			return fmt.Errorf("search path %d has too many inputs", i)
		}
		method := strings.ToLower(strings.TrimSpace(searchPath.Method))
		if strings.Contains(method, "{{") {
			if _, err := template.New("search-method").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(searchPath.Method)); err != nil {
				return fmt.Errorf("search path %d method template: %w", i, err)
			}
		} else if method != "" && method != "get" && method != "post" {
			return fmt.Errorf("search path %d method %q is unsupported", i, searchPath.Method)
		}
		responseType := strings.ToLower(strings.TrimSpace(searchPath.Response.Type))
		if responseType != "" && responseType != "html" && responseType != "json" && responseType != "xml" {
			return fmt.Errorf("search path %d response type %q is unsupported", i, searchPath.Response.Type)
		}
		if responseType == "" {
			responseType = "html"
		}
		if def.Search.Rows.After > 0 && responseType != "html" {
			return fmt.Errorf("search path %d row after requires HTML response", i)
		}
		if strings.TrimSpace(def.Search.Rows.DateHeaders.Selector) != "" && responseType != "html" {
			return fmt.Errorf("search path %d row date headers require HTML response", i)
		}
		if strings.TrimSpace(def.Search.Rows.Count.Selector) != "" && responseType != "json" {
			return fmt.Errorf("search path %d row count requires JSON response", i)
		}
		headerNames := make(map[string]struct{}, len(searchPath.Headers))
		for name := range searchPath.Headers {
			trimmed := strings.TrimSpace(name)
			if name != trimmed {
				return fmt.Errorf("search path %d header name must be canonical", i)
			}
			canonical := strings.ToLower(trimmed)
			if _, exists := headerNames[canonical]; exists {
				return fmt.Errorf("search path %d has duplicate header %q", i, canonical)
			}
			headerNames[canonical] = struct{}{}
			if !supportedDefinitionHeader(canonical) {
				return fmt.Errorf("search path %d header %q is unsupported", i, name)
			}
		}
		if _, err := template.New("search-path").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(searchPath.Path)); err != nil {
			return fmt.Errorf("search path %d template: %w", i, err)
		}
		for name, source := range searchPath.Inputs {
			if _, err := template.New("search-input").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source)); err != nil {
				return fmt.Errorf("search path %d input %q template: %w", i, name, err)
			}
		}
		for name, source := range searchPath.Headers {
			if _, err := template.New("search-header").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(string(source))); err != nil {
				return fmt.Errorf("search path %d header %q template: %w", i, name, err)
			}
		}
	}
	for name, field := range def.Search.Fields {
		if strings.Contains(field.Text, "{{") {
			if _, err := template.New("field-template").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(field.Text)); err != nil {
				return fmt.Errorf("field %s template: %w", name, err)
			}
		}
		if field.Default != "" {
			if _, err := template.New("field-default-template").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(field.Default)); err != nil {
				return fmt.Errorf("field %s default template: %w", name, err)
			}
		}
		for _, rule := range field.Case {
			if strings.TrimSpace(rule.Match) == "" || len(rule.Match) > 4096 || len(rule.Value) > maxExtractedFieldBytes {
				return fmt.Errorf("field %s has an invalid case rule", name)
			}
			if strings.Contains(rule.Value, "{{") {
				if _, err := template.New("field-case-template").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(rule.Value)); err != nil {
					return fmt.Errorf("field %s case template: %w", name, err)
				}
			}
		}
		if err := validateFilters(field.Filters); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
	}
	return nil
}

func supportedDefinitionHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "accept", "authorization", "cookie", "referer", "user-agent", "api-key", "x-api-key", "x-csrf-token", "x-milkie-auth", "x-postman-token":
		return true
	default:
		return false
	}
}

// SettingSchemas returns the fields Add Indexer should render. Informational
// entries are included for guidance but marked non-editable so they can never
// be persisted as credentials.
func (d *Definition) SettingSchemas() []SettingSchema {
	if d == nil {
		return []SettingSchema{}
	}
	out := make([]SettingSchema, 0, len(d.Settings))
	for _, setting := range d.Settings {
		settingType, editable := normalizeSettingType(setting.Type)
		label := strings.TrimSpace(setting.Label)
		defaultValue := settingValue(setting.Default)
		if specializedLabel, specializedDefault, specialized := specializedInfoGuidance(setting.Type); specialized {
			if label == "" {
				label = specializedLabel
			}
			if defaultValue == "" {
				defaultValue = specializedDefault
			}
		}
		if label == "" {
			label = setting.Name
		}
		out = append(out, SettingSchema{
			Name:     setting.Name,
			Label:    label,
			Type:     settingType,
			Default:  defaultValue,
			Options:  append([]SettingOption(nil), setting.Options...),
			Secret:   editable && secretSetting(setting.Name, settingType),
			Editable: editable,
		})
	}
	return out
}

// sessionCookieSetting is added by Caravan to every definition that logs in
// with credentials. A pasted browser session replaces the login flow, which is
// the only way past trackers that show a captcha on their login form.
const sessionCookieSetting = "caravan_session_cookie"

func withSessionCookieSetting(login *loginBlock, settings []settingBlock) []settingBlock {
	if login == nil || loginMethod(login) == "cookie" {
		return settings
	}
	for _, setting := range settings {
		if setting.Name == sessionCookieSetting {
			return settings
		}
	}
	return append(settings, settingBlock{
		Name:  sessionCookieSetting,
		Type:  "text",
		Label: "Session cookie (optional, replaces the login above)",
	})
}

// RequiresFlareSolverr reports whether the definition declares that the site
// sits behind a browser challenge.
func (d *Definition) RequiresFlareSolverr() bool {
	if d == nil {
		return false
	}
	for _, setting := range d.Settings {
		if strings.EqualFold(strings.TrimSpace(setting.Type), flareSolverrSettingType) {
			return true
		}
	}
	return false
}

func specializedInfoGuidance(raw string) (label, message string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "info_category_8000":
		return "Adult categories", "Adult results use the 8000 category group and remain subject to Caravan's category filters.", true
	case "info_cookie":
		return "Cookie authentication", "Paste the tracker session cookie requested by this indexer.", true
	case flareSolverrSettingType:
		return "Browser challenge", "This site sits behind a Cloudflare or DDoS-Guard challenge. Caravan passes it through the FlareSolverr URL set in Settings > Indexers.", true
	case "info_useragent":
		return "Browser user agent", "Provide the same browser user agent associated with the tracker session.", true
	default:
		return "", "", false
	}
}

func normalizeSettingType(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "text", "password", "checkbox", "select":
		return value, true
	case "info":
		return "info", false
	default:
		if strings.HasPrefix(value, "info_") {
			return "info", false
		}
		return "", false
	}
}

func settingValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func secretSetting(name, settingType string) bool {
	if settingType == "password" {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{"password", "passwd", "passkey", "pass", "cookie", "token", "secret", "apikey", "api_key", "auth", "session", "pin", "hash", "uid"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func validateFilters(filters []filterBlock) error {
	if len(filters) > 64 {
		return fmt.Errorf("too many filters")
	}
	for _, filter := range filters {
		switch strings.ToLower(strings.TrimSpace(filter.Name)) {
		case "", "tolower", "toupper", "trim", "append", "prepend", "replace", "re_replace", "regexp", "split", "urlencode", "urldecode", "htmldecode":
			continue
		case "querystring":
			if err := validateQueryStringFilter(filter.Args); err != nil {
				return err
			}
		case "fuzzytime":
			if err := validateFuzzyTimeFilter(filter.Args); err != nil {
				return err
			}
		case "timeago":
			if err := validateFuzzyTimeFilter(filter.Args); err != nil {
				return err
			}
		case "diacritics":
			if _, err := applyDiacriticsFilter("", filter.Args); err != nil {
				return err
			}
		case "validfilename":
			if filter.Args != nil {
				return fmt.Errorf("validfilename does not accept arguments")
			}
		case "validate":
			if err := validateAllowedValuesFilter(filter.Args); err != nil {
				return err
			}
		case "dateparse", "timeparse":
			if err := validateDateParseFilter(filter.Args); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported filter %q", filter.Name)
		}
	}
	return nil
}

func validateRowFilters(filters []filterBlock) error {
	if len(filters) > 16 {
		return fmt.Errorf("too many row filters")
	}
	for _, filter := range filters {
		if !strings.EqualFold(strings.TrimSpace(filter.Name), "andmatch") {
			return fmt.Errorf("unsupported row filter %q", filter.Name)
		}
		if _, err := rowMatchThreshold(filter.Args); err != nil {
			return err
		}
	}
	return nil
}

// Capabilities maps tracker-specific categories to their Newznab/Torznab ids.
func (d *Definition) Capabilities() Capabilities {
	seen := map[int]string{}
	for _, mapping := range d.Caps.CategoryMappings {
		if id := standardCategoryID(mapping.Cat); id > 0 {
			if _, exists := seen[id]; !exists {
				seen[id] = strings.TrimSpace(mapping.Cat)
			}
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	cats := make([]core.IndexerCategory, 0, len(ids))
	for _, id := range ids {
		cats = append(cats, core.IndexerCategory{ID: id, Name: seen[id], Subcats: []core.IndexerCategory{}})
	}
	modes := map[string]bool{}
	for mode := range d.Caps.Modes {
		modes[normalizeMode(mode)] = true
	}
	return Capabilities{Categories: cats, Modes: modes}
}

func (d *Definition) categoryMap() map[string]int {
	out := make(map[string]int, len(d.Caps.CategoryMappings))
	for _, mapping := range d.Caps.CategoryMappings {
		id := standardCategoryID(mapping.Cat)
		if id > 0 {
			out[fmt.Sprint(mapping.ID)] = id
		}
	}
	return out
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tv-search", "tvsearch":
		return "tvsearch"
	case "movie-search", "movie":
		return "movie"
	case "music-search", "music":
		return "music"
	case "book-search", "book":
		return "book"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func standardCategoryID(name string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "movies":
		return 2000
	case strings.HasPrefix(n, "movies/uhd"):
		return 2045
	case strings.HasPrefix(n, "movies/hd"):
		return 2040
	case strings.HasPrefix(n, "movies/sd"):
		return 2030
	case strings.HasPrefix(n, "movies"):
		return 2000
	case n == "tv":
		return 5000
	case strings.HasPrefix(n, "tv/anime"):
		return 5070
	case strings.HasPrefix(n, "tv/hd"):
		return 5040
	case strings.HasPrefix(n, "tv/sd"):
		return 5030
	case strings.HasPrefix(n, "tv"):
		return 5000
	case strings.HasPrefix(n, "audio"):
		return 3000
	case strings.HasPrefix(n, "pc"):
		return 4000
	case strings.HasPrefix(n, "xxx"):
		return 6000
	case strings.HasPrefix(n, "books"):
		return 7000
	case strings.HasPrefix(n, "console"):
		return 1000
	case strings.HasPrefix(n, "other"):
		return 8000
	default:
		return 0
	}
}
