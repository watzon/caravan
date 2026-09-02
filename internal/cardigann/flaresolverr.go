package cardigann

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	flareSolverrMaxTimeout      = 60 * time.Second
	flareSolverrMaxResponseSize = maxSearchPageBytes + (1 << 20)
	flareSolverrSettingType     = "info_flaresolverr"
)

// ErrBrowserChallenge is returned when a tracker answers with a Cloudflare or
// DDoS-Guard challenge page and no FlareSolverr endpoint is configured.
var ErrBrowserChallenge = errors.New("the site answered with a browser challenge (Cloudflare or DDoS-Guard). Set a FlareSolverr URL in Settings > Indexers so Caravan can pass it")

// FlareSolverr talks to a FlareSolverr instance (https://github.com/FlareSolverr/FlareSolverr).
// It is owner-configured, so it uses an unrestricted HTTP client: the usual
// deployment is a container on the same host or LAN.
type FlareSolverr struct {
	endpoint *url.URL
	hc       *http.Client
}

// NewFlareSolverr validates the endpoint and returns a client for it. A nil
// http.Client uses a default with a timeout longer than the solver's own limit.
func NewFlareSolverr(endpoint string, hc *http.Client) (*FlareSolverr, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("FlareSolverr URL must be an http or https URL")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("FlareSolverr URL must not include credentials")
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	}
	if hc == nil {
		hc = &http.Client{Timeout: flareSolverrMaxTimeout + 30*time.Second}
	}
	return &FlareSolverr{endpoint: parsed, hc: hc}, nil
}

// Endpoint returns the normalized base URL.
func (f *FlareSolverr) Endpoint() string {
	if f == nil || f.endpoint == nil {
		return ""
	}
	return f.endpoint.String()
}

// Ping checks that the endpoint answers like FlareSolverr and returns its version.
func (f *FlareSolverr) Ping(ctx context.Context) (string, error) {
	if f == nil {
		return "", fmt.Errorf("FlareSolverr is not configured")
	}
	target := *f.endpoint
	target.Path += "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build FlareSolverr request")
	}
	resp, err := f.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("FlareSolverr is unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("read FlareSolverr response")
	}
	var status struct {
		Message string `json:"msg"`
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &status) != nil || !strings.Contains(strings.ToLower(status.Message), "flaresolverr") {
		return "", fmt.Errorf("the URL did not answer like FlareSolverr (HTTP %d)", resp.StatusCode)
	}
	return status.Version, nil
}

type flareSolution struct {
	URL       string
	Status    int
	Response  []byte
	Cookies   []*http.Cookie
	UserAgent string
}

type flareSolverrCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

// Solve asks FlareSolverr to load req in a real browser. Only GET and
// form-encoded POST requests can be replayed by the solver.
func (f *FlareSolverr) Solve(ctx context.Context, req *http.Request) (*flareSolution, error) {
	if f == nil {
		return nil, ErrBrowserChallenge
	}
	command := map[string]any{
		"url":        req.URL.String(),
		"maxTimeout": int(flareSolverrMaxTimeout / time.Millisecond),
	}
	switch req.Method {
	case http.MethodGet:
		command["cmd"] = "request.get"
	case http.MethodPost:
		if req.GetBody == nil {
			return nil, fmt.Errorf("FlareSolverr cannot replay this POST request")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("FlareSolverr cannot replay this POST request")
		}
		data, err := io.ReadAll(io.LimitReader(body, maxRenderedTemplateBytes))
		body.Close()
		if err != nil {
			return nil, fmt.Errorf("FlareSolverr cannot replay this POST request")
		}
		command["cmd"] = "request.post"
		command["postData"] = string(data)
	default:
		return nil, fmt.Errorf("FlareSolverr cannot replay %s requests", req.Method)
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("encode FlareSolverr request")
	}
	target := *f.endpoint
	target.Path += "/v1"
	solverReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build FlareSolverr request")
	}
	solverReq.Header.Set("Content-Type", "application/json")
	resp, err := f.hc.Do(solverReq)
	if err != nil {
		return nil, fmt.Errorf("FlareSolverr is unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, flareSolverrMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read FlareSolverr response")
	}
	if len(raw) > flareSolverrMaxResponseSize {
		return nil, fmt.Errorf("FlareSolverr response exceeds size limit")
	}
	var result struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Solution struct {
			URL       string               `json:"url"`
			Status    int                  `json:"status"`
			Response  string               `json:"response"`
			Cookies   []flareSolverrCookie `json:"cookies"`
			UserAgent string               `json:"userAgent"`
		} `json:"solution"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("FlareSolverr returned an unreadable response (HTTP %d)", resp.StatusCode)
	}
	if result.Status != "ok" {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("FlareSolverr could not pass the browser challenge: %s", message)
	}
	solution := &flareSolution{
		URL:       result.Solution.URL,
		Status:    result.Solution.Status,
		Response:  []byte(result.Solution.Response),
		UserAgent: strings.TrimSpace(result.Solution.UserAgent),
	}
	for _, cookie := range result.Solution.Cookies {
		if cookie.Name == "" || strings.ContainsAny(cookie.Name+cookie.Value, "\r\n;") {
			continue
		}
		converted := &http.Cookie{Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, HttpOnly: cookie.HTTPOnly, Secure: cookie.Secure}
		if cookie.Expires > 0 {
			converted.Expires = time.Unix(int64(cookie.Expires), 0)
		}
		solution.Cookies = append(solution.Cookies, converted)
	}
	return solution, nil
}

// looksLikeBrowserChallenge recognizes Cloudflare and DDoS-Guard interstitials.
func looksLikeBrowserChallenge(resp *http.Response, body []byte) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests {
		return false
	}
	if strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge") {
		return true
	}
	server := strings.ToLower(resp.Header.Get("Server"))
	sample := strings.ToLower(string(body[:min(len(body), 256<<10)]))
	if strings.HasPrefix(server, "cloudflare") {
		for _, marker := range []string{"_cf_chl_opt", "cf-browser-verification", "cf_chl_prog", "challenge-platform", "just a moment", "cf-turnstile"} {
			if strings.Contains(sample, marker) {
				return true
			}
		}
	}
	if strings.HasPrefix(server, "ddos-guard") || strings.Contains(sample, "ddos-guard") {
		return true
	}
	return false
}

// do executes a tracker request and passes browser challenges through
// FlareSolverr when one is configured. After a solve, the solver's cookies and
// user agent are kept for the rest of this engine's requests.
func (e *Engine) do(req *http.Request) (*http.Response, error) {
	if e.waf != nil || e.wafRequired {
		e.wafMu.Lock()
		if e.wafUserAgent != "" {
			req.Header.Set("User-Agent", e.wafUserAgent)
		}
		e.wafMu.Unlock()
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchPageBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read tracker response")
	}
	if !looksLikeBrowserChallenge(resp, body) {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	if e.waf == nil {
		return nil, ErrBrowserChallenge
	}
	solution, err := e.waf.Solve(req.Context(), req)
	if err != nil {
		return nil, err
	}
	e.rememberSolution(req.URL, solution)
	retry, err := cloneRequest(req)
	if err == nil {
		retry.Header.Set("User-Agent", solution.UserAgent)
		retried, retryErr := e.hc.Do(retry)
		if retryErr == nil {
			if retried.StatusCode != http.StatusForbidden && retried.StatusCode != http.StatusServiceUnavailable && retried.StatusCode != http.StatusTooManyRequests {
				return retried, nil
			}
			retried.Body.Close()
		}
	}
	synthesized := &http.Response{
		StatusCode: solution.Status,
		Status:     fmt.Sprintf("%d %s", solution.Status, http.StatusText(solution.Status)),
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(bytes.NewReader(solution.Response)),
		Request:    req,
	}
	if synthesized.StatusCode == 0 {
		synthesized.StatusCode = http.StatusOK
	}
	return synthesized, nil
}

func (e *Engine) rememberSolution(target *url.URL, solution *flareSolution) {
	if solution == nil {
		return
	}
	e.wafMu.Lock()
	if solution.UserAgent != "" {
		e.wafUserAgent = solution.UserAgent
	}
	e.wafMu.Unlock()
	if e.hc.Jar != nil && len(solution.Cookies) > 0 {
		e.hc.Jar.SetCookies(target, solution.Cookies)
	}
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("request body cannot be replayed")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}
