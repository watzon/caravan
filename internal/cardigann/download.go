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
	"github.com/anacrolix/torrent/metainfo"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/torrentmeta"
)

// ResolveDownload follows a definition's bounded detail-page selectors at grab
// time. Search results remain cheap; only the release the user actually grabs
// incurs the additional tracker request.
func (e *Engine) ResolveDownload(ctx context.Context, raw string) (resolved string, err error) {
	if e == nil || e.def == nil || e.def.Download == nil {
		return raw, nil
	}
	defer func() { err = e.redactError(err) }()
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		if _, parseErr := metainfo.ParseMagnetV2Uri(raw); parseErr != nil {
			return "", fmt.Errorf("invalid magnet download")
		}
		return raw, nil
	}
	if err := e.ensureLogin(ctx); err != nil {
		return "", fmt.Errorf("authenticate tracker: %w", err)
	}
	target, err := e.resolveDownloadTarget(raw)
	if err != nil {
		return "", err
	}
	beforeBody, detailBody, err := e.executeDownloadBefore(ctx, target)
	if err != nil {
		return "", err
	}
	if len(e.def.Download.Selectors) == 0 && e.def.Download.InfoHash == nil {
		return target.String(), nil
	}
	var beforeDoc, detailDoc *goquery.Document
	document := func(useBefore bool) (*goquery.Document, error) {
		if useBefore {
			if len(beforeBody) == 0 {
				return nil, fmt.Errorf("download selector requires a pre-download response")
			}
			if beforeDoc == nil {
				beforeDoc, err = goquery.NewDocumentFromReader(bytes.NewReader(beforeBody))
				if err != nil {
					return nil, fmt.Errorf("parse pre-download response")
				}
			}
			return beforeDoc, nil
		}
		if detailDoc == nil {
			if detailBody == nil {
				detailBody, err = e.fetchDownloadDocument(ctx, target)
				if err != nil {
					return nil, err
				}
			}
			detailDoc, err = goquery.NewDocumentFromReader(bytes.NewReader(detailBody))
			if err != nil {
				return nil, fmt.Errorf("parse download detail page")
			}
		}
		return detailDoc, nil
	}
	for i, declared := range e.def.Download.Selectors {
		doc, documentErr := document(declared.UseBeforeResponse)
		if documentErr != nil {
			return "", documentErr
		}
		field, renderErr := e.renderDownloadField(declared, target)
		if renderErr != nil {
			return "", fmt.Errorf("render download selector %d: %w", i, renderErr)
		}
		value, found, extractErr := extractField(doc.Selection, field)
		if extractErr != nil {
			return "", fmt.Errorf("extract download selector %d: %w", i, extractErr)
		}
		if !found || strings.TrimSpace(value) == "" {
			continue
		}
		resolved, resolveErr := e.resolveDownloadTargetFrom(target, value)
		if resolveErr != nil {
			return "", fmt.Errorf("download selector %d: %w", i, resolveErr)
		}
		return resolved, nil
	}
	if info := e.def.Download.InfoHash; info != nil {
		doc, documentErr := document(info.UseBeforeResponse)
		if documentErr != nil {
			return "", documentErr
		}
		hashField, renderErr := e.renderDownloadField(info.Hash, target)
		if renderErr != nil {
			return "", fmt.Errorf("render download infohash: %w", renderErr)
		}
		hash, found, extractErr := extractField(doc.Selection, hashField)
		if extractErr != nil {
			return "", fmt.Errorf("extract download infohash: %w", extractErr)
		}
		hash = normalizeInfoHash(hash)
		if !found || hash == "" {
			return "", fmt.Errorf("download detail page contained no valid info hash")
		}
		title := ""
		if strings.TrimSpace(info.Title.Selector) != "" || strings.TrimSpace(info.Title.Text) != "" {
			titleField, titleErr := e.renderDownloadField(info.Title, target)
			if titleErr != nil {
				return "", fmt.Errorf("render download title: %w", titleErr)
			}
			title, _, titleErr = extractField(doc.Selection, titleField)
			if titleErr != nil {
				return "", fmt.Errorf("extract download title: %w", titleErr)
			}
		}
		return "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(title), nil
	}
	return "", fmt.Errorf("download detail page contained no supported link")
}

// FetchDownload retrieves torrent bytes through the configured indexer's
// isolated authenticated session. It is intentionally separate from URL
// resolution so magnets never become payload requests and remote download
// clients never need tracker credentials or network reachability.
func (e *Engine) FetchDownload(ctx context.Context, raw string) (payload []byte, err error) {
	defer func() {
		if err != nil {
			err = e.redactError(err)
		}
	}()
	if e == nil || e.def == nil {
		return nil, fmt.Errorf("download engine is unavailable")
	}
	if err := e.ensureLogin(ctx); err != nil {
		return nil, fmt.Errorf("authenticate tracker: %w", err)
	}
	target, err := e.resolveDownloadTarget(raw)
	if err != nil {
		return nil, err
	}
	method := http.MethodGet
	if e.def.Download != nil && strings.TrimSpace(e.def.Download.Method) != "" {
		method = strings.ToUpper(strings.TrimSpace(e.def.Download.Method))
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build tracker download request")
	}
	req.Header.Set("Accept", "application/x-bittorrent,application/octet-stream,*/*")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if e.def.Download != nil {
		for name, source := range e.def.Download.Headers {
			value, renderErr := e.renderTemplateWithDownloadURI(string(source), Query{}, target)
			if renderErr != nil {
				return nil, fmt.Errorf("render download header %q: %w", name, renderErr)
			}
			if strings.ContainsAny(value, "\r\n") {
				return nil, fmt.Errorf("invalid download header value")
			}
			req.Header.Set(name, value)
		}
	}
	e.applySessionCookie(req)
	return e.executeDownloadPayloadRequest(req)
}

func (e *Engine) executeDownloadPayloadRequest(req *http.Request) ([]byte, error) {
	req = e.withRedirectPolicy(req, nil)
	if err := e.waitRequestDelay(req.Context()); err != nil {
		return nil, err
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, safeRequestError(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, core.MaxTorrentPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tracker download response")
	}
	if len(payload) > core.MaxTorrentPayloadBytes {
		return nil, fmt.Errorf("tracker download exceeds size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("tracker returned HTTP %d for download", resp.StatusCode)
	}
	if _, _, err := torrentmeta.Parse(payload); err != nil {
		return nil, fmt.Errorf("tracker returned an invalid torrent payload")
	}
	return payload, nil
}

func (e *Engine) executeDownloadBefore(ctx context.Context, downloadURI *url.URL) ([]byte, []byte, error) {
	before := e.def.Download.Before
	if before == nil {
		return nil, nil, nil
	}
	var detailBody []byte
	var target *url.URL
	if strings.TrimSpace(before.PathSelector.Selector) != "" {
		var err error
		detailBody, err = e.fetchDownloadDocument(ctx, downloadURI)
		if err != nil {
			return nil, nil, err
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(detailBody))
		if err != nil {
			return nil, nil, fmt.Errorf("parse download detail page for before path")
		}
		field, err := e.renderDownloadField(before.PathSelector, downloadURI)
		if err != nil {
			return nil, nil, fmt.Errorf("render before path selector: %w", err)
		}
		value, found, err := extractField(doc.Selection, field)
		if err != nil {
			return nil, nil, fmt.Errorf("extract before path selector: %w", err)
		}
		if !found || strings.TrimSpace(value) == "" {
			return nil, nil, fmt.Errorf("download detail page contained no before path")
		}
		target, err = e.resolveDownloadURLFrom(downloadURI, value)
		if err != nil {
			return nil, nil, fmt.Errorf("before path selector: %w", err)
		}
	} else {
		rendered, err := e.renderTemplateWithDownloadURI(before.Path, Query{}, downloadURI)
		if err != nil {
			return nil, nil, fmt.Errorf("render before path: %w", err)
		}
		target, err = e.resolveDownloadURLFrom(e.base, rendered)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve before path: %w", err)
		}
	}
	values := make(url.Values, len(before.Inputs))
	for name, source := range before.Inputs {
		value, err := e.renderTemplateWithDownloadURI(source, Query{}, downloadURI)
		if err != nil {
			return nil, nil, fmt.Errorf("render before input %q: %w", name, err)
		}
		values.Set(name, value)
	}
	method := strings.ToUpper(strings.TrimSpace(before.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodGet {
		query := target.Query()
		for name, items := range values {
			for _, value := range items {
				query.Add(name, value)
			}
		}
		target.RawQuery = query.Encode()
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("build pre-download request")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	e.applySessionCookie(req)
	beforeBody, err := e.executeLoginRequest(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute pre-download request: %w", err)
	}
	return beforeBody, detailBody, nil
}

func (e *Engine) fetchDownloadDocument(ctx context.Context, target *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build download detail request")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Caravan/1 Cardigann-compatible indexer")
	e.applySessionCookie(req)
	body, err := e.executeLoginRequest(req)
	if err != nil {
		return nil, fmt.Errorf("load download detail page: %w", err)
	}
	return body, nil
}

func (e *Engine) renderDownloadField(declared fieldBlock, downloadURI *url.URL) (fieldBlock, error) {
	field := declared
	var err error
	if strings.TrimSpace(field.Selector) != "" {
		field.Selector, err = e.renderTemplateWithDownloadURI(field.Selector, Query{}, downloadURI)
		if err != nil {
			return fieldBlock{}, err
		}
	}
	field.Filters = append([]filterBlock(nil), field.Filters...)
	for i := range field.Filters {
		field.Filters[i].Args, err = e.renderDownloadFilterArgument(field.Filters[i].Args, downloadURI)
		if err != nil {
			return fieldBlock{}, err
		}
	}
	return field, nil
}

func (e *Engine) renderDownloadFilterArgument(value any, downloadURI *url.URL) (any, error) {
	switch value := value.(type) {
	case string:
		if !strings.Contains(value, "{{") {
			return value, nil
		}
		return e.renderTemplateWithDownloadURI(value, Query{}, downloadURI)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			rendered, err := e.renderDownloadFilterArgument(item, downloadURI)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case []string:
		out := make([]string, len(value))
		for i, item := range value {
			rendered, err := e.renderDownloadFilterArgument(item, downloadURI)
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

func (e *Engine) resolveDownloadTarget(raw string) (*url.URL, error) {
	return e.resolveDownloadURLFrom(e.base, raw)
}

func (e *Engine) resolveDownloadTargetFrom(base *url.URL, raw string) (string, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		return strings.TrimSpace(raw), nil
	}
	resolved, err := e.resolveDownloadURLFrom(base, raw)
	if err != nil {
		return "", err
	}
	return resolved.String(), nil
}

func (e *Engine) resolveDownloadURLFrom(base *url.URL, raw string) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || reference.User != nil {
		return nil, fmt.Errorf("invalid download URL")
	}
	resolved := base.ResolveReference(reference)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return nil, fmt.Errorf("unsupported download URL scheme")
	}
	if _, approved := e.origins[requestOrigin(resolved)]; !approved {
		return nil, fmt.Errorf("download URL uses an unapproved origin")
	}
	return resolved, nil
}

func (c *Client) ResolveDownload(ctx context.Context, raw string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return c.engine.ResolveDownload(ctx, raw)
}

func (c *Client) FetchDownload(ctx context.Context, raw string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.engine.FetchDownload(ctx, raw)
}
