package cardigann

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

func (e *Engine) ensureLogin(ctx context.Context) error {
	if e == nil || e.def == nil || e.def.Login == nil {
		return nil
	}
	e.loginMu.Lock()
	defer e.loginMu.Unlock()
	if e.loginReady {
		return nil
	}
	login := e.def.Login
	if err := e.seedLoginCookies(login.Cookies); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(login.Method)) {
	case "cookie":
		cookie, err := e.renderTemplate(login.Inputs["cookie"], Query{})
		if err != nil {
			return fmt.Errorf("render cookie: %w", err)
		}
		cookie = strings.TrimSpace(cookie)
		if cookie == "" || strings.ContainsAny(cookie, "\r\n") {
			return fmt.Errorf("configured cookie is empty or invalid")
		}
		e.sessionCookie = cookie
	case "get", "post":
		body, err := e.performLoginRequest(ctx, login)
		if err != nil {
			return err
		}
		if err := e.checkLoginErrors(body, login.Error); err != nil {
			return err
		}
	case "form":
		body, err := e.performFormLogin(ctx, login)
		if err != nil {
			return err
		}
		if err := e.checkLoginErrors(body, login.Error); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported login method")
	}
	if login.Test.Path != "" {
		if err := e.performLoginTest(ctx, login.Test); err != nil {
			return err
		}
	}
	e.loginReady = true
	return nil
}

func (e *Engine) performFormLogin(ctx context.Context, login *loginBlock) ([]byte, error) {
	loginURL, err := e.searchURL(login.Path, Query{})
	if err != nil {
		return nil, fmt.Errorf("resolve login form path: %w", err)
	}
	pageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build login form request")
	}
	pageRequest.Header.Set("Accept", "text/html,application/xhtml+xml")
	pageRequest.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if err := e.applyLoginHeaders(pageRequest, login.Headers); err != nil {
		return nil, err
	}
	e.applySessionCookie(pageRequest)
	body, err := e.executeLoginRequest(pageRequest)
	if err != nil {
		return nil, fmt.Errorf("load login form: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse login form")
	}
	formSelector := strings.TrimSpace(login.Form)
	if formSelector == "" {
		formSelector = "form"
	}
	form := doc.Find(formSelector).First()
	if form.Length() == 0 {
		return nil, fmt.Errorf("login form selector did not match")
	}
	values := url.Values{}
	form.Find("input[name]").Each(func(_ int, input *goquery.Selection) {
		if _, disabled := input.Attr("disabled"); disabled {
			return
		}
		name, _ := input.Attr("name")
		inputType := strings.ToLower(strings.TrimSpace(attrOr(input, "type", "text")))
		switch inputType {
		case "button", "file", "image", "reset", "submit":
			return
		case "checkbox", "radio":
			if _, checked := input.Attr("checked"); !checked {
				return
			}
		}
		value := attrOr(input, "value", "")
		if (inputType == "checkbox" || inputType == "radio") && value == "" {
			value = "on"
		}
		values.Add(name, value)
	})
	form.Find("textarea[name]").Each(func(_ int, field *goquery.Selection) {
		if _, disabled := field.Attr("disabled"); disabled {
			return
		}
		name, _ := field.Attr("name")
		values.Add(name, field.Text())
	})
	form.Find("select[name]").Each(func(_ int, field *goquery.Selection) {
		if _, disabled := field.Attr("disabled"); disabled {
			return
		}
		name, _ := field.Attr("name")
		options := field.Find("option[selected]")
		if options.Length() == 0 {
			options = field.Find("option").First()
		}
		options.Each(func(_ int, option *goquery.Selection) {
			values.Add(name, attrOr(option, "value", strings.TrimSpace(option.Text())))
		})
	})
	for name, declared := range login.SelectorInputs {
		field, renderErr := e.renderDownloadField(declared, nil)
		if renderErr != nil {
			return nil, fmt.Errorf("render login selector input %q: %w", name, renderErr)
		}
		value, found, extractErr := extractField(doc.Selection, field)
		if extractErr != nil {
			return nil, fmt.Errorf("extract login selector input %q: %w", name, extractErr)
		}
		if !found {
			if field.Optional {
				continue
			}
			return nil, fmt.Errorf("login selector input %q was not found", name)
		}
		values.Set(name, value)
	}
	for name, source := range login.Inputs {
		value, renderErr := e.renderTemplate(source, Query{})
		if renderErr != nil {
			return nil, fmt.Errorf("render login input %q: %w", name, renderErr)
		}
		inputName := name
		if login.Selectors {
			control := form.Find(name).First()
			if control.Length() == 0 {
				control = doc.Find(name).First()
			}
			var found bool
			inputName, found = control.Attr("name")
			if !found || strings.TrimSpace(inputName) == "" {
				return nil, fmt.Errorf("login input selector %q did not resolve to a named control", name)
			}
		}
		values.Set(inputName, value)
	}
	actionURL := loginURL
	if action, exists := form.Attr("action"); exists && strings.TrimSpace(action) != "" {
		reference, parseErr := url.Parse(strings.TrimSpace(action))
		if parseErr != nil {
			return nil, fmt.Errorf("login form has an invalid action")
		}
		actionURL = loginURL.ResolveReference(reference)
	}
	if strings.TrimSpace(login.SubmitPath) != "" {
		rendered, renderErr := e.renderTemplate(login.SubmitPath, Query{})
		if renderErr != nil {
			return nil, fmt.Errorf("render login submit path: %w", renderErr)
		}
		reference, parseErr := url.Parse(strings.TrimSpace(rendered))
		if parseErr != nil {
			return nil, fmt.Errorf("login submit path is invalid")
		}
		actionURL = loginURL.ResolveReference(reference)
	}
	if _, approved := e.origins[requestOrigin(actionURL)]; !approved {
		return nil, fmt.Errorf("login form action uses an unapproved origin")
	}
	method := strings.ToUpper(strings.TrimSpace(attrOr(form, "method", http.MethodGet)))
	var requestBody io.Reader
	if method == http.MethodGet {
		query := actionURL.Query()
		for name, items := range values {
			for _, value := range items {
				query.Add(name, value)
			}
		}
		actionURL.RawQuery = query.Encode()
	} else if method == http.MethodPost {
		requestBody = strings.NewReader(values.Encode())
	} else {
		return nil, fmt.Errorf("login form method is unsupported")
	}
	submitRequest, err := http.NewRequestWithContext(ctx, method, actionURL.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build login form submission")
	}
	submitRequest.Header.Set("Accept", "text/html,application/xhtml+xml")
	submitRequest.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if method == http.MethodPost {
		submitRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if err := e.applyLoginHeaders(submitRequest, login.Headers); err != nil {
		return nil, err
	}
	e.applySessionCookie(submitRequest)
	return e.executeLoginRequest(submitRequest)
}

func (e *Engine) seedLoginCookies(declared []string) error {
	if len(declared) == 0 {
		return nil
	}
	parts := make([]string, 0, len(declared))
	for _, source := range declared {
		rendered, err := e.renderTemplate(source, Query{})
		if err != nil {
			return fmt.Errorf("render login cookie: %w", err)
		}
		parts = append(parts, rendered)
	}
	request := &http.Request{Header: http.Header{"Cookie": []string{strings.Join(parts, "; ")}}}
	cookies := request.Cookies()
	if len(cookies) != len(parts) {
		return fmt.Errorf("login cookie is invalid")
	}
	serialized := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		serialized = append(serialized, cookie.String())
	}
	e.seededCookie = strings.Join(serialized, "; ")
	return nil
}

func (e *Engine) applyLoginHeaders(req *http.Request, headers map[string]headerTemplate) error {
	for name, source := range headers {
		value, err := e.renderTemplate(string(source), Query{})
		if err != nil {
			return fmt.Errorf("render login header %q: %w", name, err)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid login header value")
		}
		req.Header.Set(name, value)
	}
	return nil
}

func attrOr(selection *goquery.Selection, name, fallback string) string {
	if value, exists := selection.Attr(name); exists {
		return value
	}
	return fallback
}

func (e *Engine) performLoginRequest(ctx context.Context, login *loginBlock) ([]byte, error) {
	target, err := e.searchURL(login.Path, Query{})
	if err != nil {
		return nil, fmt.Errorf("resolve login path: %w", err)
	}
	values := make(url.Values, len(login.Inputs))
	for name, source := range login.Inputs {
		value, renderErr := e.renderTemplate(source, Query{})
		if renderErr != nil {
			return nil, fmt.Errorf("render login input %q: %w", name, renderErr)
		}
		values.Set(name, value)
	}
	method := strings.ToUpper(strings.TrimSpace(login.Method))
	var requestBody io.Reader
	if method == http.MethodGet {
		query := target.Query()
		for name, items := range values {
			for _, value := range items {
				query.Add(name, value)
			}
		}
		target.RawQuery = query.Encode()
	} else if method == http.MethodPost {
		requestBody = strings.NewReader(values.Encode())
	} else {
		return nil, fmt.Errorf("unsupported login method")
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build login request")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, source := range login.Headers {
		value, renderErr := e.renderTemplate(string(source), Query{})
		if renderErr != nil {
			return nil, fmt.Errorf("render login header %q: %w", name, renderErr)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid login header value")
		}
		req.Header.Set(name, value)
	}
	e.applySessionCookie(req)
	return e.executeLoginRequest(req)
}

func (e *Engine) performLoginTest(ctx context.Context, test loginTestBlock) error {
	target, err := e.searchURL(test.Path, Query{})
	if err != nil {
		return fmt.Errorf("resolve login test path: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("build login test request")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	e.applySessionCookie(req)
	body, err := e.executeLoginRequest(req)
	if err != nil {
		return fmt.Errorf("test credentials: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("test credentials: parse response")
	}
	if doc.Find(test.Selector).Length() == 0 {
		return fmt.Errorf("tracker did not confirm the configured credentials")
	}
	return nil
}

func (e *Engine) executeLoginRequest(req *http.Request) ([]byte, error) {
	if e.origins != nil {
		if _, approved := e.origins[requestOrigin(req.URL)]; !approved {
			return nil, fmt.Errorf("request uses an unapproved origin")
		}
	}
	req = e.withRedirectPolicy(req, nil)
	if err := e.waitRequestDelay(req.Context()); err != nil {
		return nil, err
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, safeRequestError(err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxSearchPageBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read tracker response")
	}
	if len(body) > maxSearchPageBytes {
		return nil, fmt.Errorf("tracker response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tracker returned HTTP %d", resp.StatusCode)
	}
	if e.def.Encoding != "" && !strings.EqualFold(e.def.Encoding, "UTF-8") && !strings.EqualFold(e.def.Encoding, "utf8") {
		decoded, decodeErr := charset.NewReaderLabel(e.def.Encoding, bytes.NewReader(body))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode tracker response")
		}
		body, err = io.ReadAll(io.LimitReader(decoded, maxSearchPageBytes+1))
		if err != nil || len(body) > maxSearchPageBytes {
			return nil, fmt.Errorf("decode tracker response")
		}
	}
	return body, nil
}

func (e *Engine) checkLoginErrors(body []byte, rules []loginErrorBlock) error {
	return e.checkResponseErrors(body, rules, "tracker rejected the configured credentials")
}

func (e *Engine) checkResponseErrors(body []byte, rules []loginErrorBlock, defaultMessage string) error {
	if len(rules) == 0 {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse login response")
	}
	for _, rule := range rules {
		if doc.Find(rule.Selector).Length() == 0 {
			continue
		}
		message := defaultMessage
		if rule.Message.Text != "" {
			if rendered, renderErr := e.renderResultTemplate(rule.Message.Text, Query{}, map[string]string{}); renderErr == nil && strings.TrimSpace(rendered) != "" {
				message = strings.TrimSpace(rendered)
			}
		} else if rule.Message.Selector != "" {
			if extracted := strings.TrimSpace(doc.Find(rule.Message.Selector).First().Text()); extracted != "" && len(extracted) <= maxExtractedFieldBytes {
				message = extracted
			}
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func (e *Engine) applySessionCookie(req *http.Request) {
	if e == nil || req == nil || req.Header.Get("Cookie") != "" {
		return
	}
	if e.base == nil || requestOrigin(req.URL) != requestOrigin(e.base) {
		return
	}
	configured := make([]string, 0, 2)
	if e.seededCookie != "" {
		configured = append(configured, e.seededCookie)
	}
	if e.sessionCookie != "" {
		configured = append(configured, e.sessionCookie)
	}
	if len(configured) > 0 {
		req.Header.Set("Cookie", strings.Join(configured, "; "))
	}
}
