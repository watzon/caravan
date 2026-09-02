package cardigann

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"golang.org/x/net/html/charset"
)

const (
	maxSearchPageBytes        = 8 << 20
	maxRenderedTemplateBytes  = 64 << 10
	maxSearchResultRows       = 1000
	maxExtractedFieldBytes    = 16 << 10
	maxReleaseFieldBytes      = 128 << 10
	maxConfiguredSettingBytes = 16 << 10
)

// Config is one configured instance of a definition.
type Config struct {
	BaseURL      string
	Settings     map[string]string
	FlareSolverr *FlareSolverr
}

// Query is the subset of Torznab search inputs the definition templates use.
type Query struct {
	Keywords   string
	Season     int
	Episode    int
	Categories []int
	TVDBID     int
	TVMazeID   int
	TMDBID     int
	TVRageID   int
	IMDbID     string
	Year       int
	Genre      string
	Author     string
	Artist     string
	Album      string
	Label      string
}

type templateQuery struct {
	Keywords    string
	Season      int
	Episode     int
	Ep          int
	Categories  []string
	TVDBID      int
	TVMazeID    int
	TVMAZEID    int
	TMDBID      int
	TVRageID    int
	TVRAGEID    int
	IMDbID      string
	IMDBID      string
	IMDBIDShort string
	Year        int
	Genre       string
	Author      string
	Artist      string
	Album       string
	Label       string
}

func newTemplateQuery(q Query) templateQuery {
	shortIMDbID := q.IMDbID
	if len(shortIMDbID) >= 2 && strings.EqualFold(shortIMDbID[:2], "tt") {
		shortIMDbID = shortIMDbID[2:]
	}
	categories := make([]string, 0, len(q.Categories))
	for _, category := range q.Categories {
		categories = append(categories, strconv.Itoa(category))
	}
	return templateQuery{
		Keywords: q.Keywords, Season: q.Season, Episode: q.Episode, Ep: q.Episode,
		Categories: categories, TVDBID: q.TVDBID, TVMazeID: q.TVMazeID, TVMAZEID: q.TVMazeID,
		TMDBID: q.TMDBID, TVRageID: q.TVRageID, TVRAGEID: q.TVRageID,
		IMDbID: q.IMDbID, IMDBID: q.IMDbID, IMDBIDShort: shortIMDbID,
		Year: q.Year, Genre: q.Genre, Author: q.Author, Artist: q.Artist, Album: q.Album, Label: q.Label,
	}
}

func (e *Engine) templateQuery(q Query) templateQuery {
	query := newTemplateQuery(q)
	query.Categories = e.siteCategoryIDs(q.Categories)
	return query
}

func (e *Engine) siteCategoryIDs(categories []int) []string {
	if len(categories) == 0 {
		return nil
	}
	wanted := make(map[int]bool, len(categories))
	for _, category := range categories {
		wanted[category] = true
	}
	ids := make([]string, 0)
	for siteID, canonical := range e.catBySite {
		matched := wanted[canonical]
		if !matched {
			for requested := range wanted {
				if requested > 0 && requested%1000 == 0 && canonical/1000 == requested/1000 {
					matched = true
					break
				}
			}
		}
		if matched {
			ids = append(ids, siteID)
		}
	}
	if len(ids) == 0 {
		for _, category := range categories {
			ids = append(ids, strconv.Itoa(category))
		}
	}
	sort.Strings(ids)
	return ids
}

// Engine runs one definition against one selected tracker base URL.
type Engine struct {
	def              *Definition
	base             *url.URL
	hc               *http.Client
	origins          map[string]struct{}
	catBySite        map[string]int
	settings         map[string]string
	templateSettings map[string]any
	secrets          []string
	requestMu        sync.Mutex
	lastRequest      time.Time
	loginMu          sync.Mutex
	loginReady       bool
	sessionCookie    string
	seededCookie     string
	waf              *FlareSolverr
	wafRequired      bool
	wafMu            sync.Mutex
	wafUserAgent     string
}

type followRedirectContextKey struct{}

// New validates a configured definition and constructs its HTTP engine.
func New(def *Definition, cfg Config, hc *http.Client) (*Engine, error) {
	if def == nil {
		return nil, fmt.Errorf("definition is nil")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" && len(def.Links) > 0 {
		base = def.Links[0]
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid tracker base URL")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	settings := make(map[string]string, len(def.Settings)+len(cfg.Settings)+1)
	declaredSettings := make(map[string]struct{}, len(def.Settings))
	settingTypes := make(map[string]string, len(def.Settings))
	for _, setting := range def.Settings {
		name := strings.TrimSpace(setting.Name)
		if name == "" || name != setting.Name || name == "sitelink" {
			return nil, fmt.Errorf("definition contains an invalid setting name")
		}
		if _, exists := declaredSettings[name]; exists {
			return nil, fmt.Errorf("definition contains a duplicate setting name")
		}
		declaredSettings[name] = struct{}{}
		settingTypes[name] = strings.ToLower(strings.TrimSpace(setting.Type))
		if setting.Default != nil {
			value := fmt.Sprint(setting.Default)
			if len(value) > maxConfiguredSettingBytes {
				return nil, fmt.Errorf("definition setting default exceeds size limit")
			}
			settings[name] = value
		}
	}
	for name, value := range cfg.Settings {
		if _, declared := declaredSettings[name]; !declared {
			return nil, fmt.Errorf("configured setting is not declared by definition")
		}
		if len(value) > maxConfiguredSettingBytes {
			return nil, fmt.Errorf("configured setting exceeds size limit")
		}
		settings[name] = value
	}
	settings["sitelink"] = strings.TrimRight(u.String(), "/") + "/"
	templateSettings := make(map[string]any, len(settings))
	for name, value := range settings {
		typed, typeErr := templateSettingValue(settingTypes[name], value)
		if typeErr != nil {
			return nil, fmt.Errorf("definition setting %q: %w", name, typeErr)
		}
		templateSettings[name] = typed
	}
	origins := map[string]struct{}{}
	if len(def.approvedOrigins) > 0 {
		for _, origin := range def.approvedOrigins {
			origins[origin] = struct{}{}
		}
		if _, approved := origins[requestOrigin(u)]; !approved {
			return nil, fmt.Errorf("configured tracker base is not one of this indexer's supported tracker URLs")
		}
	} else {
		origins[requestOrigin(u)] = struct{}{}
		for _, link := range def.Links {
			if parsed, parseErr := url.Parse(strings.TrimSpace(link)); parseErr == nil && parsed.Host != "" {
				origins[requestOrigin(parsed)] = struct{}{}
			}
		}
	}
	for name, value := range settings {
		origin, ok := urlSettingOrigin(name, value)
		if !ok {
			continue
		}
		if len(def.approvedOrigins) > 0 {
			if _, approved := origins[origin]; !approved {
				return nil, fmt.Errorf("configured URL setting is not one of this indexer's supported tracker URLs")
			}
		} else {
			origins[origin] = struct{}{}
		}
	}
	client := *hc
	jar, jarErr := cookiejar.New(nil)
	if jarErr != nil {
		return nil, fmt.Errorf("create isolated tracker cookie jar: %w", jarErr)
	}
	client.Jar = jar
	previousRedirectPolicy := hc.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if follow, set := req.Context().Value(followRedirectContextKey{}).(bool); set && !follow {
			return http.ErrUseLastResponse
		}
		if _, ok := origins[requestOrigin(req.URL)]; !ok {
			return fmt.Errorf("unapproved redirect origin")
		}
		if len(via) > 0 && requestOrigin(req.URL) != requestOrigin(via[len(via)-1].URL) {
			req.Header.Del("Cookie")
			req.Header.Del("Authorization")
			req.Header.Del("Proxy-Authorization")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &Engine{
		def: def, base: u, hc: &client, origins: origins,
		catBySite: def.categoryMap(), settings: settings, templateSettings: templateSettings,
		secrets: configuredSecretValues(base, settings),
		waf:     cfg.FlareSolverr, wafRequired: def.RequiresFlareSolverr(),
	}, nil
}

func templateSettingValue(settingType, value string) (any, error) {
	if strings.EqualFold(strings.TrimSpace(settingType), "checkbox") {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("checkbox value must be true or false")
		}
	}
	return value, nil
}

func (e *Engine) templateConfig() map[string]any {
	if e != nil && e.templateSettings != nil {
		return e.templateSettings
	}
	out := map[string]any{}
	if e != nil {
		for name, value := range e.settings {
			out[name] = value
		}
	}
	return out
}

func configuredSecretValues(base string, settings map[string]string) []string {
	values := make([]string, 0, len(settings)*3+4)
	collect := func(value string) {
		value = strings.TrimSpace(value)
		// Fragments shorter than 4 bytes carry no secrecy and shred
		// unrelated words when substituted out of error messages.
		if len(value) >= 4 {
			values = append(values, value)
		}
	}
	collectURL := func(raw string) {
		collect(raw)
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return
		}
		collect(parsed.Hostname())
		if parsed.User != nil {
			collect(parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				collect(password)
			}
		}
		if path, err := url.PathUnescape(strings.Trim(parsed.EscapedPath(), "/")); err == nil {
			collect(path)
		}
		for _, queryValues := range parsed.Query() {
			for _, value := range queryValues {
				collect(value)
			}
		}
		collect(parsed.Fragment)
	}
	collectURL(base)
	for _, value := range settings {
		collectURL(value)
	}
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	deduplicated := values[:0]
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		deduplicated = append(deduplicated, value)
	}
	return deduplicated
}

func requestOrigin(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// urlSettingOrigin derives the request origin a URL-carrying setting points
// at. Cardigann definitions name these "sitelink" or "*url", and upstream
// defaults may omit the scheme (https is assumed, as in YTS's apiurl).
func urlSettingOrigin(name, value string) (string, bool) {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if lowerName != "sitelink" && !strings.HasSuffix(lowerName, "url") {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse("https://" + value)
	}
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return requestOrigin(parsed), true
}

// Search renders every declared search path, scrapes its rows, and normalizes
// them into Caravan releases.
func (e *Engine) Search(ctx context.Context, q Query) (releases []core.Release, err error) {
	defer func() {
		if err != nil {
			err = e.redactError(err)
		}
	}()
	if err := e.ensureLogin(ctx); err != nil {
		return nil, fmt.Errorf("authenticate tracker: %w", err)
	}
	keywords, err := applyFilters(q.Keywords, e.def.Search.KeywordFilters)
	if err != nil {
		return nil, fmt.Errorf("filter keywords: %w", err)
	}
	q.Keywords = keywords
	out := make([]core.Release, 0)
	for i, path := range e.def.Search.Paths {
		if !e.searchPathMatchesCategories(path, q.Categories) {
			continue
		}
		req, err := e.buildSearchRequest(ctx, path, q)
		if err != nil {
			return nil, fmt.Errorf("search path %d: %w", i, err)
		}
		rows, err := e.fetchRows(req, path.Response, q)
		if err != nil {
			return nil, fmt.Errorf("search path %d: %w", i, err)
		}
		if len(rows) > maxSearchResultRows-len(out) {
			return nil, fmt.Errorf("search returned too many results")
		}
		out = append(out, rows...)
	}
	return filterByCategories(deduplicateReleases(out), q.Categories), nil
}

func (e *Engine) searchPathMatchesCategories(path pathBlock, categories []int) bool {
	if len(path.Categories) == 0 || len(categories) == 0 {
		return true
	}
	siteCategories := e.siteCategoryIDs(categories)
	wanted := make(map[string]bool, len(siteCategories))
	for _, category := range siteCategories {
		wanted[category] = true
	}
	for _, category := range path.Categories {
		if wanted[category] {
			return true
		}
	}
	return false
}

func (e *Engine) redactError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range e.secrets {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return errors.New(message)
}

func deduplicateReleases(releases []core.Release) []core.Release {
	seen := make(map[string]bool, len(releases))
	unique := make([]core.Release, 0, len(releases))
	for _, release := range releases {
		key := releaseIdentity(release)
		if key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		unique = append(unique, release)
	}
	return unique
}

func releaseIdentity(release core.Release) string {
	if guid := strings.TrimSpace(release.GUID); guid != "" {
		return "guid:" + strings.ToLower(guid)
	}
	if hash := strings.TrimSpace(release.InfoHash); hash != "" {
		return "hash:" + strings.ToUpper(hash)
	}
	if download := strings.TrimSpace(release.DownloadURL); download != "" {
		return "download:" + strings.ToLower(download)
	}
	return ""
}

func filterByCategories(releases []core.Release, requested []int) []core.Release {
	if len(requested) == 0 {
		return releases
	}
	filtered := make([]core.Release, 0, len(releases))
	for _, release := range releases {
		if releaseMatchesCategories(release.Categories, requested) {
			filtered = append(filtered, release)
		}
	}
	return filtered
}

func releaseMatchesCategories(releaseCategories, requested []int) bool {
	for _, want := range requested {
		for _, got := range releaseCategories {
			if got == want || (want%1000 == 0 && got >= want && got < want+1000) {
				return true
			}
		}
	}
	return false
}

func (e *Engine) searchURL(path string, q Query) (*url.URL, error) {
	renderedPath, err := e.renderTemplate(path, q)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(renderedPath, "http://") || strings.HasPrefix(renderedPath, "https://") {
		target, err := url.Parse(renderedPath)
		if err != nil {
			return nil, fmt.Errorf("rendered tracker URL is invalid")
		}
		return target, nil
	}
	rel, err := url.Parse(strings.TrimLeft(renderedPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("rendered tracker path is invalid")
	}
	base := *e.base
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return base.ResolveReference(rel), nil
}

func (e *Engine) renderTemplate(source string, q Query) (string, error) {
	return e.renderTemplateWithDownloadURI(source, q, nil)
}

type downloadTemplateURI struct {
	AbsoluteUri  string
	AbsolutePath string
	PathAndQuery string
	Query        map[string]string
}

func (e *Engine) renderTemplateWithDownloadURI(source string, q Query, downloadURI *url.URL) (string, error) {
	queryData := e.templateQuery(q)
	data := struct {
		templateQuery
		Query       templateQuery
		Config      map[string]any
		DownloadUri downloadTemplateURI
		Today       time.Time
		True        bool
		False       bool
	}{templateQuery: queryData, Query: queryData, Config: e.templateConfig(), Today: time.Now(), True: true, False: false}
	if downloadURI != nil {
		values := downloadURI.Query()
		query := make(map[string]string, len(values))
		for name, items := range values {
			if len(items) > 0 {
				query[name] = items[0]
			}
		}
		data.DownloadUri = downloadTemplateURI{
			AbsoluteUri:  downloadURI.String(),
			AbsolutePath: downloadURI.EscapedPath(),
			PathAndQuery: downloadURI.RequestURI(),
			Query:        query,
		}
	}
	tpl, err := template.New("definition-template").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	if rendered.Len() > maxRenderedTemplateBytes {
		return "", fmt.Errorf("rendered template exceeds %d bytes", maxRenderedTemplateBytes)
	}
	return rendered.String(), nil
}

func (e *Engine) renderedFields(q Query) (map[string]fieldBlock, error) {
	fields := make(map[string]fieldBlock, len(e.def.Search.Fields))
	for name, field := range e.def.Search.Fields {
		filters := append([]filterBlock(nil), field.Filters...)
		deferredAt := len(filters)
		for i := range filters {
			if filterArgumentDependsOnResult(filters[i].Args) {
				deferredAt = i
				break
			}
			args, err := e.renderFilterArgument(filters[i].Args, q)
			if err != nil {
				return nil, fmt.Errorf("field %s filter %s: %w", name, filters[i].Name, err)
			}
			filters[i].Args = args
		}
		field.Filters = filters[:deferredAt]
		field.deferredFilters = filters[deferredAt:]
		fields[name] = field
	}
	return fields, nil
}

func filterArgumentDependsOnResult(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.Contains(value, ".Result") || strings.Contains(value, ".Fields")
	case []any:
		for _, item := range value {
			if filterArgumentDependsOnResult(item) {
				return true
			}
		}
	case []string:
		for _, item := range value {
			if filterArgumentDependsOnResult(item) {
				return true
			}
		}
	}
	return false
}

func (e *Engine) renderFilterArgument(value any, q Query) (any, error) {
	switch value := value.(type) {
	case string:
		if !strings.Contains(value, "{{") {
			return value, nil
		}
		return e.renderTemplate(value, q)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			rendered, err := e.renderFilterArgument(item, q)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case []string:
		out := make([]string, len(value))
		for i, item := range value {
			rendered, err := e.renderFilterArgument(item, q)
			if err != nil {
				return nil, err
			}
			out[i] = fmt.Sprint(rendered)
		}
		return out, nil
	default:
		return value, nil
	}
}

func (e *Engine) buildSearchRequest(ctx context.Context, path pathBlock, q Query) (*http.Request, error) {
	target, err := e.searchURL(path.Path, q)
	if err != nil {
		return nil, err
	}
	if e.origins != nil {
		if _, ok := e.origins[requestOrigin(target)]; !ok {
			return nil, fmt.Errorf("resolve search path: unapproved origin")
		}
	}
	var sharedInputs map[string]string
	var sharedHeaders map[string]headerTemplate
	allowEmptyInputs := false
	if e.def != nil {
		sharedInputs, sharedHeaders = e.def.Search.Inputs, e.def.Search.Headers
		allowEmptyInputs = e.def.Search.AllowEmptyInputs
	}
	inputs := mergedDefinitionMap(sharedInputs, path.Inputs)
	values := make(url.Values, len(inputs))
	for name, source := range inputs {
		value, err := e.renderTemplate(source, q)
		if err != nil {
			return nil, fmt.Errorf("render input %q: %w", name, err)
		}
		if name == "$raw" {
			parsed, parseErr := url.ParseQuery(value)
			if parseErr != nil {
				return nil, fmt.Errorf("render raw input: invalid query string")
			}
			for rawName, rawValues := range parsed {
				for _, rawValue := range rawValues {
					values.Add(rawName, rawValue)
				}
			}
			continue
		}
		if value != "" || allowEmptyInputs {
			values.Set(name, value)
		}
	}
	renderedMethod, err := e.renderTemplate(path.Method, q)
	if err != nil {
		return nil, fmt.Errorf("render request method: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(renderedMethod))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodGet {
		query := target.Query()
		for name, values := range values {
			for _, value := range values {
				query.Add(name, value)
			}
		}
		target.RawQuery = query.Encode()
	} else if method == http.MethodPost {
		body = strings.NewReader(values.Encode())
	} else {
		return nil, fmt.Errorf("unsupported request method %q", path.Method)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build tracker request: invalid URL")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, source := range mergedDefinitionHeaders(sharedHeaders, path.Headers) {
		value, err := e.renderTemplate(source, q)
		if err != nil {
			return nil, fmt.Errorf("render header %q: %w", name, err)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid header value for %q", name)
		}
		req.Header.Set(name, value)
	}
	e.applySessionCookie(req)
	return e.withRedirectPolicy(req, path.FollowRedirect), nil
}

func (e *Engine) withRedirectPolicy(req *http.Request, override *bool) *http.Request {
	follow := true
	if e != nil && e.def != nil && e.def.FollowRedirect != nil {
		follow = *e.def.FollowRedirect
	}
	if override != nil {
		follow = *override
	}
	return req.WithContext(context.WithValue(req.Context(), followRedirectContextKey{}, follow))
}

func mergedDefinitionMap(shared, specific map[string]string) map[string]string {
	out := make(map[string]string, len(shared)+len(specific))
	for name, value := range shared {
		out[name] = value
	}
	for name, value := range specific {
		out[name] = value
	}
	return out
}

func mergedDefinitionHeaders(shared, specific map[string]headerTemplate) map[string]string {
	out := make(map[string]string, len(shared)+len(specific))
	for name, value := range shared {
		out[name] = string(value)
	}
	for name, value := range specific {
		out[name] = string(value)
	}
	return out
}

func (e *Engine) waitRequestDelay(ctx context.Context) error {
	return e.waitRequestDelayWith(ctx, time.Now, waitForRequestDelay)
}

func (e *Engine) waitRequestDelayWith(ctx context.Context, now func() time.Time, wait func(context.Context, time.Duration) error) error {
	if e == nil || e.def == nil || e.def.RequestDelay <= 0 {
		return nil
	}
	delay := time.Duration(e.def.RequestDelay * float64(time.Second))
	e.requestMu.Lock()
	defer e.requestMu.Unlock()
	if !e.lastRequest.IsZero() {
		remaining := delay - now().Sub(e.lastRequest)
		if remaining > 0 {
			if err := wait(ctx, remaining); err != nil {
				return err
			}
		}
	}
	e.lastRequest = now()
	return nil
}

func waitForRequestDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *Engine) fetchRows(req *http.Request, response responseBlock, q Query) ([]core.Release, error) {
	if err := e.waitRequestDelay(req.Context()); err != nil {
		return nil, err
	}
	resp, err := e.do(req)
	if err != nil {
		return nil, safeRequestError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tracker returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tracker response: %w", err)
	}
	if len(body) > maxSearchPageBytes {
		return nil, fmt.Errorf("tracker response exceeds %d bytes", maxSearchPageBytes)
	}
	if !strings.EqualFold(e.def.Encoding, "UTF-8") && !strings.EqualFold(e.def.Encoding, "utf8") {
		decoded, decodeErr := charset.NewReaderLabel(e.def.Encoding, bytes.NewReader(body))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode tracker response as %s: %w", e.def.Encoding, decodeErr)
		}
		body, err = io.ReadAll(io.LimitReader(decoded, maxSearchPageBytes+1))
		if err != nil {
			return nil, fmt.Errorf("decode tracker response as %s: %w", e.def.Encoding, err)
		}
		if len(body) > maxSearchPageBytes {
			return nil, fmt.Errorf("decoded tracker response exceeds %d bytes", maxSearchPageBytes)
		}
	}
	if err := e.checkResponseErrors(body, e.def.Search.Error, "tracker reported a search error"); err != nil {
		return nil, err
	}
	if message := strings.TrimSpace(response.NoResultsMessage); message != "" && strings.Contains(string(body), message) {
		return []core.Release{}, nil
	}
	reader := bytes.NewReader(body)
	if strings.EqualFold(strings.TrimSpace(response.Type), "json") {
		return e.jsonRowsForQuery(reader, q)
	}
	if strings.EqualFold(strings.TrimSpace(response.Type), "xml") {
		return e.xmlRows(body, q)
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}
	selection := doc.Find(e.def.Search.Rows.Selector)
	if selection.Length() > maxSearchResultRows {
		return nil, fmt.Errorf("parse HTML: too many rows")
	}
	fields, err := e.renderedFields(q)
	if err != nil {
		return nil, err
	}
	results := make([]core.Release, 0, selection.Length())
	var rowErr error
	step := e.def.Search.Rows.After + 1
	for start := 0; start < selection.Length(); start += step {
		end := min(start+step, selection.Length())
		row := selection.Slice(start, end)
		release, ok, err := e.release(row, fields, q)
		if err != nil {
			rowErr = err
			break
		}
		if ok && matchesRowFilters(release, q, e.def.Search.Rows.Filters) {
			results = append(results, release)
		}
	}
	if rowErr != nil {
		return nil, rowErr
	}
	return results, nil
}

func (e *Engine) xmlRows(body []byte, q Query) ([]core.Release, error) {
	root, err := parseXMLDocument(body)
	if err != nil {
		return nil, err
	}
	rows := xmlRows(root, e.def.Search.Rows.Selector)
	if len(rows) > maxSearchResultRows {
		return nil, fmt.Errorf("parse XML: too many rows")
	}
	fields, err := e.renderedFields(q)
	if err != nil {
		return nil, err
	}
	results := make([]core.Release, 0, len(rows))
	for _, row := range rows {
		values := make(map[string]string, len(fields))
		skip := false
		for name, field := range fields {
			if strings.Contains(field.Text, "{{") {
				continue
			}
			value, found, err := extractXMLField(row, field)
			if err != nil {
				// Cardigann treats a field parse failure like a missing
				// value: only this row is affected, never the search.
				value, found = "", false
			}
			if !found && field.Default == "" && !field.Optional {
				skip = true
				break
			}
			values[name] = value
		}
		if skip {
			continue
		}
		values, err = e.renderResultFieldTemplates(values, fields, q)
		if err != nil {
			return nil, err
		}
		release, keep, err := e.releaseValues(values)
		if err != nil {
			return nil, err
		}
		if keep && matchesRowFilters(release, q, e.def.Search.Rows.Filters) {
			results = append(results, release)
		}
	}
	return results, nil
}

func safeRequestError(err error) error {
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || urlErr.Err == nil || urlErr.Err == err {
			break
		}
		err = urlErr.Err
	}
	return fmt.Errorf("tracker request failed: %w", err)
}

func (e *Engine) release(row *goquery.Selection, fields map[string]fieldBlock, q Query) (core.Release, bool, error) {
	values := map[string]string{}
	if dateHeader := e.def.Search.Rows.DateHeaders; strings.TrimSpace(dateHeader.Selector) != "" {
		value, found, err := extractDateHeader(row.First(), dateHeader)
		if err == nil && found {
			values["date"] = value
		}
	}
	for name, field := range fields {
		if strings.Contains(field.Text, "{{") {
			continue
		}
		value, found, err := extractField(row, field)
		if err != nil {
			// Cardigann treats a field parse failure like a missing
			// value: only this row is affected, never the search.
			value, found = "", false
		}
		if !found && field.Default == "" && !field.Optional {
			return core.Release{}, false, nil
		}
		values[name] = value
	}
	var err error
	values, err = e.renderResultFieldTemplates(values, fields, q)
	if err != nil {
		return core.Release{}, false, err
	}
	return e.releaseValues(values)
}

func extractDateHeader(row *goquery.Selection, field fieldBlock) (string, bool, error) {
	if value, found, err := extractField(row, field); found || err != nil {
		return value, found, err
	}
	selector := strings.TrimSpace(field.Selector)
	for previous := row.Prev(); previous.Length() > 0; previous = previous.Prev() {
		match := previous.Find(selector).First()
		if previous.Is(selector) {
			match = previous.First()
		}
		if match.Length() == 0 {
			continue
		}
		return extractMatchedHTMLField(previous, match, field)
	}
	return "", false, nil
}

func extractMatchedHTMLField(row, found *goquery.Selection, field fieldBlock) (string, bool, error) {
	value := ""
	if field.Attribute != "" {
		var ok bool
		value, ok = found.Attr(field.Attribute)
		if !ok {
			return "", false, nil
		}
	} else {
		if strings.TrimSpace(field.Remove) != "" {
			found = found.Clone()
			found.Find(field.Remove).Remove()
		}
		value = found.Text()
	}
	if len(field.Case) > 0 {
		var ok bool
		var err error
		value, ok, err = htmlCaseValue(row, found, strings.TrimSpace(value), field.Case)
		if err != nil {
			return "", false, err
		}
		if !ok {
			return "", false, nil
		}
	}
	filtered, err := applyFilters(strings.TrimSpace(value), field.Filters)
	return filtered, true, err
}

func (e *Engine) jsonRows(reader io.Reader) ([]core.Release, error) {
	return e.jsonRowsForQuery(reader, Query{})
}

func (e *Engine) jsonRowsForQuery(reader io.Reader, q Query) ([]core.Release, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("parse JSON: response must contain exactly one JSON value")
	}
	if selector := strings.TrimSpace(e.def.Search.Rows.Count.Selector); selector != "" {
		value, found := jsonDocumentPathValue(payload, selector)
		if !found {
			return nil, fmt.Errorf("JSON count selector %q did not resolve to a value", selector)
		}
		count, countErr := jsonCount(value)
		if countErr != nil {
			return nil, fmt.Errorf("JSON count selector %q: %w", selector, countErr)
		}
		if count == 0 {
			return []core.Release{}, nil
		}
	}
	selector := strings.TrimSpace(e.def.Search.Rows.Selector)
	var rows []any
	if selector == "$" {
		rows, _ = payload.([]any)
	} else if root, ok := payload.(map[string]any); ok {
		value, found := jsonPathValue(root, selector)
		if found {
			rows, _ = value.([]any)
		}
	}
	if rows == nil {
		return nil, fmt.Errorf("JSON rows selector %q did not resolve to an array", selector)
	}
	if len(rows) > maxSearchResultRows {
		return nil, fmt.Errorf("parse JSON: too many rows")
	}
	preparedRows, err := prepareJSONRows(rows, e.def.Search.Rows)
	if err != nil {
		return nil, err
	}
	fields, err := e.renderedFields(q)
	if err != nil {
		return nil, err
	}
	results := make([]core.Release, 0, len(preparedRows))
	for _, row := range preparedRows {
		values := make(map[string]string, len(fields))
		skip := false
		for name, field := range fields {
			if strings.Contains(field.Text, "{{") {
				continue
			}
			value, found, err := extractJSONRowField(row, field)
			if err != nil {
				// Cardigann treats a field parse failure like a missing
				// value: only this row is affected, never the search.
				value, found = "", false
			}
			if !found && field.Default == "" && !field.Optional {
				skip = true
				break
			}
			values[name] = value
		}
		if skip {
			continue
		}
		values, err = e.renderResultFieldTemplates(values, fields, q)
		if err != nil {
			return nil, err
		}
		release, keep, err := e.releaseValues(values)
		if err != nil {
			return nil, err
		}
		if keep && matchesRowFilters(release, q, e.def.Search.Rows.Filters) {
			results = append(results, release)
		}
	}
	return results, nil
}

type jsonRow struct {
	value   map[string]any
	parents []map[string]any
}

func prepareJSONRows(rows []any, block rowsBlock) ([]jsonRow, error) {
	prepared := make([]jsonRow, 0, len(rows))
	attribute := strings.TrimSpace(block.Attribute)
	for _, raw := range rows {
		parent, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if attribute == "" {
			prepared = append(prepared, jsonRow{value: parent})
			continue
		}
		value, found := jsonPathValue(parent, attribute)
		if !found || value == nil {
			if block.MissingAttributeEqualsNoResults {
				continue
			}
			return nil, fmt.Errorf("JSON row attribute %q was missing", attribute)
		}
		if block.Multiple {
			children, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("JSON row attribute %q did not resolve to an array", attribute)
			}
			for _, child := range children {
				object, ok := child.(map[string]any)
				if !ok {
					continue
				}
				prepared = append(prepared, jsonRow{value: object, parents: []map[string]any{parent}})
				if len(prepared) > maxSearchResultRows {
					return nil, fmt.Errorf("parse JSON: too many nested rows")
				}
			}
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("JSON row attribute %q did not resolve to an object", attribute)
		}
		prepared = append(prepared, jsonRow{value: object, parents: []map[string]any{parent}})
	}
	return prepared, nil
}

func (e *Engine) releaseValues(values map[string]string) (core.Release, bool, error) {
	totalBytes := 0
	for name, value := range values {
		if len(value) > maxExtractedFieldBytes {
			return core.Release{}, false, fmt.Errorf("field %s exceeds size limit", name)
		}
		totalBytes += len(value)
		if totalBytes > maxReleaseFieldBytes {
			return core.Release{}, false, fmt.Errorf("release fields exceed size limit")
		}
	}
	title := strings.TrimSpace(values["title"])
	downloadValue := values["download"]
	if strings.TrimSpace(downloadValue) == "" {
		downloadValue = values["magnet"]
	}
	download := e.absoluteURL(downloadValue)
	infoHash := normalizeInfoHash(values["infohash"])
	if download == "" && infoHash != "" {
		download = "magnet:?xt=urn:btih:" + infoHash + "&dn=" + url.QueryEscape(title)
	}
	if title == "" || download == "" {
		return core.Release{}, false, nil
	}
	if infoHash == "" {
		infoHash = magnetInfoHash(download)
	}
	details := e.absoluteURL(values["details"])
	guid := details
	if guid == "" {
		guid = download
	}
	release := core.Release{
		Title:       title,
		GUID:        guid,
		DownloadURL: download,
		InfoHash:    infoHash,
		Protocol:    core.ProtocolTorrent,
		Size:        parseByteSize(values["size"]),
		Seeders:     parseInt(values["seeders"]),
		Leechers:    parseInt(values["leechers"]),
		PublishedAt: parseDate(values["date"]),
		Categories:  e.categories(values["category"]),
		Attributes:  extendedReleaseAttributes(values),
	}
	release.Parsed = parse.Parse(release.Title)
	return release, true, nil
}

func extendedReleaseAttributes(values map[string]string) []core.ReleaseAttribute {
	reserved := map[string]bool{"title": true, "download": true, "infohash": true, "details": true, "size": true, "seeders": true, "leechers": true, "date": true, "category": true}
	names := make([]string, 0, len(values))
	for name := range values {
		if !reserved[name] && strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	attributes := make([]core.ReleaseAttribute, 0, len(names))
	for _, name := range names {
		attributes = append(attributes, core.ReleaseAttribute{Name: name, Value: values[name]})
	}
	return core.NormalizeReleaseAttributes(attributes)
}

func extractField(row *goquery.Selection, field fieldBlock) (string, bool, error) {
	value := ""
	foundValue := false
	var found *goquery.Selection
	if field.Text != "" {
		value, foundValue = field.Text, true
	} else if strings.TrimSpace(field.Selector) != "" {
		found = row.Find(field.Selector).First()
		if found.Length() == 0 {
			return "", false, nil
		}
		if field.Attribute != "" {
			value, foundValue = found.Attr(field.Attribute)
			if !foundValue {
				return "", false, nil
			}
		} else {
			if strings.TrimSpace(field.Remove) != "" {
				found = found.Clone()
				found.Find(field.Remove).Remove()
			}
			value, foundValue = found.Text(), true
		}
	}
	if len(field.Case) > 0 {
		var err error
		value, foundValue, err = htmlCaseValue(row, found, strings.TrimSpace(value), field.Case)
		if err != nil {
			return "", false, err
		}
	}
	if !foundValue {
		return "", false, nil
	}
	if len(field.Case) > 0 && strings.Contains(value, "{{") {
		return strings.TrimSpace(value), true, nil
	}
	value, err := applyFilters(strings.TrimSpace(value), field.Filters)
	return value, true, err
}

func matchesRowFilters(release core.Release, q Query, filters []filterBlock) bool {
	for _, filter := range filters {
		if strings.EqualFold(strings.TrimSpace(filter.Name), "andmatch") {
			threshold, err := rowMatchThreshold(filter.Args)
			if err != nil || !containsKeywordThreshold(release.Title, q.Keywords, threshold) {
				return false
			}
		}
	}
	return true
}

func rowMatchThreshold(raw any) (float64, error) {
	if raw == nil {
		return 100, nil
	}
	threshold, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(raw)), 64)
	if err != nil || threshold < 0 || threshold > 100 {
		return 0, fmt.Errorf("row filter andmatch threshold must be between 0 and 100")
	}
	return threshold, nil
}

func containsKeywordThreshold(title, keywords string, threshold float64) bool {
	title = strings.ToLower(title)
	parts := strings.Fields(strings.ToLower(keywords))
	if len(parts) == 0 {
		return true
	}
	matched := 0
	for _, keyword := range parts {
		if strings.Contains(title, keyword) {
			matched++
		}
	}
	return float64(matched)*100 >= threshold*float64(len(parts))
}

func containsAllKeywords(title, keywords string) bool {
	title = strings.ToLower(title)
	for _, keyword := range strings.Fields(strings.ToLower(keywords)) {
		if !strings.Contains(title, keyword) {
			return false
		}
	}
	return true
}

func extractJSONField(row map[string]any, field fieldBlock) (string, bool, error) {
	return extractJSONRowField(jsonRow{value: row}, field)
}

func extractJSONRowField(row jsonRow, field fieldBlock) (string, bool, error) {
	scalar := ""
	found := false
	if field.Text != "" {
		scalar, found = field.Text, true
	} else {
		selector := strings.TrimSpace(field.Selector)
		if selector == "" {
			if len(field.Case) == 0 {
				return "", false, nil
			}
		} else {
			value, ok := jsonRowPathValue(row, selector)
			if !ok || value == nil {
				return "", false, nil
			}
			switch value := value.(type) {
			case string:
				scalar = value
			case json.Number:
				scalar = value.String()
			case bool:
				scalar = strconv.FormatBool(value)
			case float64:
				scalar = strconv.FormatFloat(value, 'g', -1, 64)
			case []any:
				var err error
				scalar, err = jsonScalarArray(value)
				if err != nil {
					return "", false, err
				}
			default:
				return "", false, fmt.Errorf("JSON field selector must resolve to a scalar")
			}
			found = true
		}
	}
	if len(field.Case) > 0 {
		scalar, found = scalarCaseValue(strings.TrimSpace(scalar), field.Case)
	}
	if !found {
		return "", false, nil
	}
	if len(field.Case) > 0 && strings.Contains(scalar, "{{") {
		return strings.TrimSpace(scalar), true, nil
	}
	if len(scalar) > maxExtractedFieldBytes {
		return "", false, fmt.Errorf("JSON field exceeds size limit")
	}
	filtered, err := applyFilters(scalar, field.Filters)
	return filtered, true, err
}

func jsonRowPathValue(row jsonRow, selector string) (any, bool) {
	selector = strings.TrimSpace(selector)
	current := row.value
	parents := row.parents
	for strings.HasPrefix(selector, "..") {
		if len(parents) == 0 {
			return nil, false
		}
		current = parents[0]
		parents = parents[1:]
		selector = strings.TrimPrefix(selector, "..")
		selector = strings.TrimPrefix(selector, ".")
	}
	if selector == "" {
		return current, true
	}
	return jsonPathValue(current, selector)
}

func jsonScalarArray(values []any) (string, error) {
	if len(values) > 256 {
		return "", fmt.Errorf("JSON field array exceeds item limit")
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		switch value := value.(type) {
		case string:
			parts = append(parts, value)
		case json.Number:
			parts = append(parts, value.String())
		case bool:
			parts = append(parts, strconv.FormatBool(value))
		case float64:
			parts = append(parts, strconv.FormatFloat(value, 'g', -1, 64))
		default:
			return "", fmt.Errorf("JSON field array must contain only scalars")
		}
	}
	return strings.Join(parts, ", "), nil
}

func jsonPathValue(value any, selector string) (any, bool) {
	return jsonDocumentPathValue(value, selector)
}

func jsonDocumentPathValue(value any, selector string) (any, bool) {
	selector = strings.TrimSpace(selector)
	if selector == "$" || selector == "" {
		return value, true
	}
	selector = strings.TrimPrefix(selector, "$")
	selector = strings.TrimPrefix(selector, ".")
	for selector != "" {
		if selector[0] == '[' {
			close := strings.IndexByte(selector, ']')
			if close <= 1 {
				return nil, false
			}
			index, err := strconv.Atoi(selector[1:close])
			array, ok := value.([]any)
			if err != nil || !ok || index < 0 || index >= len(array) {
				return nil, false
			}
			value = array[index]
			selector = strings.TrimPrefix(selector[close+1:], ".")
			continue
		}
		next := len(selector)
		if dot := strings.IndexByte(selector, '.'); dot >= 0 && dot < next {
			next = dot
		}
		if bracket := strings.IndexByte(selector, '['); bracket >= 0 && bracket < next {
			next = bracket
		}
		if next == 0 {
			return nil, false
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[selector[:next]]
		if !ok {
			return nil, false
		}
		selector = strings.TrimPrefix(selector[next:], ".")
	}
	return value, true
}

func jsonCount(value any) (int64, error) {
	raw := ""
	switch value := value.(type) {
	case json.Number:
		raw = value.String()
	case string:
		raw = strings.TrimSpace(value)
	default:
		return 0, fmt.Errorf("must resolve to a non-negative integer")
	}
	count, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("must resolve to a non-negative integer")
	}
	return count, nil
}

func (e *Engine) renderResultFieldTemplates(values map[string]string, fields map[string]fieldBlock, q Query) (map[string]string, error) {
	out := make(map[string]string, len(values)+len(fields))
	for name := range fields {
		out[name] = ""
	}
	for name, value := range values {
		out[name] = value
	}
	names, err := orderedResultFieldNames(fields)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		field := fields[name]
		source := ""
		switch {
		case len(field.Case) > 0 && strings.Contains(out[name], "{{"):
			source = out[name]
		case strings.Contains(field.Text, "{{"):
			source = field.Text
		case strings.TrimSpace(out[name]) == "" && field.Default != "":
			source = field.Default
		}
		value := out[name]
		filters := field.deferredFilters
		if source != "" {
			filters = append(append([]filterBlock(nil), field.Filters...), field.deferredFilters...)
		}
		if source == "" && len(filters) == 0 {
			continue
		}
		// Cardigann treats a field render or filter failure like a missing
		// value: only this row's field is affected, never the search.
		rendered, renderErr := e.renderResultFieldValue(source, value, filters, q, out)
		if renderErr != nil {
			rendered = ""
		}
		out[name] = rendered
	}
	return out, nil
}

func (e *Engine) renderResultFieldValue(source, value string, filters []filterBlock, q Query, out map[string]string) (string, error) {
	var err error
	if source != "" {
		value, err = e.renderResultTemplate(source, q, out)
		if err != nil {
			return "", err
		}
	}
	filters = append([]filterBlock(nil), filters...)
	for i := range filters {
		filters[i].Args, err = e.renderResultFilterArgument(filters[i].Args, q, out)
		if err != nil {
			return "", err
		}
	}
	return applyFilters(value, filters)
}

func (e *Engine) renderResultTemplate(source string, q Query, result map[string]string) (string, error) {
	queryData := e.templateQuery(q)
	data := struct {
		templateQuery
		Query  templateQuery
		Result map[string]string
		Fields map[string]string
		Config map[string]any
		Today  time.Time
		True   bool
		False  bool
	}{templateQuery: queryData, Query: queryData, Result: result, Fields: result, Config: e.templateConfig(), Today: time.Now(), True: true, False: false}
	tpl, err := template.New("result-field").Funcs(definitionTemplateFuncs()).Parse(normalizeDefinitionTemplate(source))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", err
	}
	if rendered.Len() > maxRenderedTemplateBytes {
		return "", fmt.Errorf("rendered template exceeds size limit")
	}
	return rendered.String(), nil
}

func (e *Engine) renderResultFilterArgument(value any, q Query, result map[string]string) (any, error) {
	switch value := value.(type) {
	case string:
		if !strings.Contains(value, "{{") {
			return value, nil
		}
		return e.renderResultTemplate(value, q, result)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			rendered, err := e.renderResultFilterArgument(item, q, result)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case []string:
		out := make([]string, len(value))
		for i, item := range value {
			rendered, err := e.renderResultFilterArgument(item, q, result)
			if err != nil {
				return nil, err
			}
			out[i] = fmt.Sprint(rendered)
		}
		return out, nil
	default:
		return value, nil
	}
}

var resultDependencyPattern = regexp.MustCompile(`\.(?:Result|Fields)\.([A-Za-z_][A-Za-z0-9_-]*)`)

func orderedResultFieldNames(fields map[string]fieldBlock) ([]string, error) {
	dependencies := make(map[string]map[string]struct{}, len(fields))
	for name, field := range fields {
		deps := make(map[string]struct{})
		addResultDependencies(deps, field.Text)
		addResultDependencies(deps, field.Default)
		for _, rule := range field.Case {
			addResultDependencies(deps, rule.Value)
		}
		for _, filter := range field.deferredFilters {
			collectResultDependencies(deps, filter.Args)
		}
		delete(deps, name)
		for dependency := range deps {
			if _, exists := fields[dependency]; !exists {
				delete(deps, dependency)
			}
		}
		dependencies[name] = deps
	}
	ordered := make([]string, 0, len(fields))
	completed := make(map[string]struct{}, len(fields))
	for len(ordered) < len(fields) {
		ready := make([]string, 0)
		for name, deps := range dependencies {
			if _, done := completed[name]; done {
				continue
			}
			allDone := true
			for dependency := range deps {
				if _, done := completed[dependency]; !done {
					allDone = false
					break
				}
			}
			if allDone {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			return nil, fmt.Errorf("result field templates contain a dependency cycle")
		}
		sort.Strings(ready)
		for _, name := range ready {
			completed[name] = struct{}{}
			ordered = append(ordered, name)
		}
	}
	return ordered, nil
}

func collectResultDependencies(into map[string]struct{}, value any) {
	switch value := value.(type) {
	case string:
		addResultDependencies(into, value)
	case []any:
		for _, item := range value {
			collectResultDependencies(into, item)
		}
	case []string:
		for _, item := range value {
			addResultDependencies(into, item)
		}
	}
}

func addResultDependencies(into map[string]struct{}, source string) {
	for _, match := range resultDependencyPattern.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			into[match[1]] = struct{}{}
		}
	}
}

func (e *Engine) absoluteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "magnet:") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return e.base.ResolveReference(u).String()
}

func (e *Engine) categories(raw string) []int {
	// Multi-word mapping ids such as "TV shows" match whole before the
	// value is treated as a delimited list.
	if id := e.catBySite[strings.TrimSpace(raw)]; id > 0 {
		return []int{id}
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '|' || r == ' ' })
	seen := map[int]bool{}
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		id := e.catBySite[field]
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

var sizePattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*([kmgtp]?i?b)`) // decimal value, binary unit

func parseByteSize(raw string) int64 {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), ",", "")
	if bytes, err := strconv.ParseInt(normalized, 10, 64); err == nil && bytes >= 0 {
		return bytes
	}
	m := sizePattern.FindStringSubmatch(normalized)
	if len(m) != 3 {
		return 0
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	multipliers := map[string]float64{"b": 1, "kb": 1 << 10, "kib": 1 << 10, "mb": 1 << 20, "mib": 1 << 20, "gb": 1 << 30, "gib": 1 << 30, "tb": 1 << 40, "tib": 1 << 40, "pb": 1 << 50, "pib": 1 << 50}
	return int64(value * multipliers[strings.ToLower(m[2])])
}

func parseInt(raw string) int {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), ",", "")
	n, _ := strconv.Atoi(raw)
	return n
}

func parseDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Unix(seconds, 0).UTC()
	}
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func magnetInfoHash(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if strings.HasPrefix(strings.ToLower(xt), prefix) {
			hash := strings.TrimSpace(xt[len(prefix):])
			if len(hash) == 40 {
				return strings.ToUpper(hash)
			}
		}
	}
	return ""
}

func normalizeInfoHash(raw string) string {
	hash := strings.ToUpper(strings.TrimSpace(raw))
	if len(hash) != 40 {
		return ""
	}
	for _, r := range hash {
		if !strings.ContainsRune("0123456789ABCDEF", r) {
			return ""
		}
	}
	return hash
}

func applyFilters(value string, filters []filterBlock) (string, error) {
	var err error
	for _, filter := range filters {
		switch strings.ToLower(strings.TrimSpace(filter.Name)) {
		case "":
			continue
		case "tolower":
			value = strings.ToLower(value)
		case "toupper":
			value = strings.ToUpper(value)
		case "trim":
			if filter.Args == nil {
				value = strings.TrimSpace(value)
			} else {
				value = strings.Trim(value, fmt.Sprint(filter.Args))
			}
		case "append":
			value += fmt.Sprint(filter.Args)
		case "prepend":
			value = fmt.Sprint(filter.Args) + value
		case "replace", "re_replace":
			args, ok := filterArgs(filter.Args, 2)
			if !ok {
				return "", fmt.Errorf("filter %s requires two arguments", filter.Name)
			}
			if strings.EqualFold(filter.Name, "replace") {
				value = strings.ReplaceAll(value, args[0], args[1])
			} else {
				var re *regexp.Regexp
				re, err = regexp.Compile(args[0])
				if err == nil {
					value = re.ReplaceAllString(value, args[1])
				}
			}
		case "regexp":
			pattern := fmt.Sprint(filter.Args)
			var re *regexp.Regexp
			re, err = regexp.Compile(pattern)
			if err == nil {
				matches := re.FindStringSubmatch(value)
				if len(matches) == 0 {
					return "", fmt.Errorf("filter regexp %q did not match", pattern)
				}
				value = matches[0]
				if len(matches) > 1 {
					value = matches[1]
				}
			}
		case "split":
			args, ok := filterArgs(filter.Args, 2)
			if !ok {
				return "", fmt.Errorf("filter split requires two arguments")
			}
			position, parseErr := strconv.Atoi(args[1])
			if parseErr != nil {
				return "", fmt.Errorf("filter split position: %w", parseErr)
			}
			parts := strings.Split(value, args[0])
			if position < 0 {
				position += len(parts)
			}
			if position < 0 || position >= len(parts) {
				return "", fmt.Errorf("filter split position %d out of range", position)
			}
			value = parts[position]
		case "urldecode":
			value, err = url.QueryUnescape(value)
		case "urlencode":
			value = url.QueryEscape(value)
		case "querystring":
			value, err = applyQueryStringFilter(value, filter.Args)
		case "htmldecode":
			value = html.UnescapeString(value)
		case "fuzzytime":
			value, err = applyFuzzyTimeFilterAt(value, time.Now())
		case "timeago":
			value, err = applyTimeAgoFilterAt(value, time.Now())
		case "diacritics":
			value, err = applyDiacriticsFilter(value, filter.Args)
		case "validfilename":
			value = applyValidFilenameFilter(value)
		case "validate":
			value, err = applyAllowedValuesFilter(value, filter.Args)
		case "dateparse", "timeparse":
			value, err = applyDateParseFilter(value, filter.Args)
		default:
			return "", fmt.Errorf("unsupported filter %q", filter.Name)
		}
		if err != nil {
			return "", fmt.Errorf("filter %s: %w", filter.Name, err)
		}
	}
	return strings.TrimSpace(value), nil
}

func filterArgs(raw any, count int) ([]string, bool) {
	values, ok := raw.([]any)
	if !ok || len(values) < count {
		return nil, false
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fmt.Sprint(value)
	}
	return out, true
}
