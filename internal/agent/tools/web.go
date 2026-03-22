package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// NewWebFetch creates the web_fetch tool with SSRF protection enabled.
func NewWebFetch() Tool {
	return newWebFetch(checkPrivateHost)
}

func newWebFetch(checkHost func(string) error) Tool {
	return Tool{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its content. Supports text, raw, links, and metadata modes. Only http/https URLs allowed.",
		Parameters: []ParameterDef{
			{Name: "url", Type: "string", Description: "URL to fetch", Required: true},
			{Name: "max_length", Type: "int", Description: "max response length in bytes (default 50000)", Required: false},
			{Name: "follow_pagination", Type: "bool", Description: "follow Link rel=next pagination for text/JSON responses (default true)", Required: false},
			{Name: "max_pages", Type: "int", Description: "maximum paginated pages to fetch (default 5)", Required: false},
			{Name: "mode", Type: "string", Description: "output mode: text, raw, links, metadata (default text)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			rawURL, _ := args["url"].(string)
			maxLen := 50000
			if v, ok := args["max_length"].(float64); ok && v > 0 {
				maxLen = int(v)
			}
			followPagination := true
			if v, ok := args["follow_pagination"].(bool); ok {
				followPagination = v
			}
			maxPages := 5
			if v, ok := args["max_pages"].(float64); ok && v > 0 {
				maxPages = int(v)
				if maxPages > 20 {
					maxPages = 20
				}
			}
			mode := "text"
			if v, ok := args["mode"].(string); ok && strings.TrimSpace(v) != "" {
				mode = strings.ToLower(strings.TrimSpace(v))
			}

			client := &http.Client{
				Timeout: 30 * time.Second,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					if len(via) >= 5 {
						return fmt.Errorf("too many redirects")
					}
					rHost := req.URL.Hostname()
					if err := checkHost(rHost); err != nil {
						return err
					}
					return nil
				},
			}

			result, truncated, errText := fetchWebContent(ctx, client, checkHost, rawURL, maxLen, followPagination, maxPages, mode)
			if errText != "" {
				return errText, nil
			}
			result, outputTruncated := truncateText(result, maxLen)
			if truncated || outputTruncated {
				result += "\n... truncated at " + fmt.Sprintf("%d", maxLen) + " bytes"
			}
			return result, nil
		},
	}
}

type fetchedLink struct {
	Text string `json:"text,omitempty"`
	URL  string `json:"url"`
}

type fetchedPage struct {
	url         string
	statusCode  int
	contentType string
	rawText     string
	text        string
	title       string
	links       []fetchedLink
	nextURL     string
	truncated   bool
	jsonArray   []json.RawMessage
	isJSONArray bool
}

func fetchWebContent(ctx context.Context, client *http.Client, checkHost func(string) error, rawURL string, maxLen int, followPagination bool, maxPages int, mode string) (string, bool, string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false, fmt.Sprintf("error: invalid URL: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false, fmt.Sprintf("error: only http/https schemes allowed, got %q", parsed.Scheme)
	}
	if err := checkHost(parsed.Hostname()); err != nil {
		return "", false, fmt.Sprintf("error: %v", err)
	}

	readLimit := computeReadLimit(maxLen)
	visited := map[string]struct{}{}
	var pages []fetchedPage
	currentURL := rawURL
	truncated := false

	for pageNum := 0; pageNum < maxPages; pageNum++ {
		if _, seen := visited[currentURL]; seen {
			break
		}
		visited[currentURL] = struct{}{}

		page, errText := fetchSinglePage(ctx, client, checkHost, currentURL, readLimit)
		if errText != "" {
			return "", false, errText
		}
		pages = append(pages, page)
		truncated = truncated || page.truncated

		if !followPagination || page.nextURL == "" {
			break
		}
		currentURL = page.nextURL
	}

	return combineFetchedPages(pages, mode), truncated, ""
}

func computeReadLimit(maxLen int) int {
	readLimit := maxLen * 20
	if readLimit < 200000 {
		readLimit = 200000
	}
	if readLimit > 5000000 {
		readLimit = 5000000
	}
	return readLimit
}

func fetchSinglePage(ctx context.Context, client *http.Client, checkHost func(string) error, rawURL string, readLimit int) (fetchedPage, string) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return fetchedPage{}, fmt.Sprintf("error: %v", err)
	}
	req.Header.Set("User-Agent", "forge/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return fetchedPage{}, fmt.Sprintf("error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fetchedPage{}, fmt.Sprintf("error: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(readLimit)+1))
	if err != nil {
		return fetchedPage{}, fmt.Sprintf("error reading response: %v", err)
	}
	responseTruncated := len(body) > readLimit
	if responseTruncated {
		body = body[:readLimit]
	}

	ct := detectContentType(resp.Header.Get("Content-Type"), body)
	if isBinaryContentType(ct) {
		return fetchedPage{}, fmt.Sprintf("error: binary content type %q, cannot display", ct)
	}

	decoded, err := decodeBody(body, ct)
	if err != nil {
		decoded = string(body)
	}

	nextURL := ""
	if candidate := parseNextLink(resp.Header.Get("Link")); candidate != "" {
		if resolved, err := resolveAndValidateURL(rawURL, candidate, checkHost); err == nil {
			nextURL = resolved
		}
	}

	page := fetchedPage{url: rawURL, statusCode: resp.StatusCode, contentType: ct, rawText: decoded, nextURL: nextURL, truncated: responseTruncated}
	if isJSONContentType(ct) {
		if arr, ok := decodeJSONArray([]byte(decoded)); ok {
			page.jsonArray = arr
			page.isJSONArray = true
		}
		page.text = formatJSON([]byte(decoded))
	} else if isHTMLContentType(ct) {
		page.text = extractText(decoded)
		page.title = extractHTMLTitle(decoded)
		page.links = extractHTMLLinks(rawURL, decoded)
	} else {
		page.text = decoded
	}
	return page, ""
}

func combineFetchedPages(pages []fetchedPage, mode string) string {
	if len(pages) == 0 {
		return ""
	}

	switch mode {
	case "raw":
		parts := make([]string, 0, len(pages))
		for _, page := range pages {
			if page.rawText != "" {
				parts = append(parts, page.rawText)
			}
		}
		return strings.Join(parts, "\n\n")
	case "links":
		var links []fetchedLink
		for _, page := range pages {
			links = append(links, page.links...)
		}
		if len(links) == 0 {
			return "no links found"
		}
		b, err := json.MarshalIndent(links, "", "  ")
		if err == nil {
			return string(b)
		}
		return "no links found"
	case "metadata":
		items := make([]map[string]any, 0, len(pages))
		for _, page := range pages {
			item := map[string]any{
				"url":           page.url,
				"status_code":   page.statusCode,
				"content_type":  page.contentType,
				"truncated":     page.truncated,
				"has_next_page": page.nextURL != "",
			}
			if page.title != "" {
				item["title"] = page.title
			}
			if len(page.links) > 0 {
				item["link_count"] = len(page.links)
			}
			items = append(items, item)
		}
		b, err := json.MarshalIndent(items, "", "  ")
		if err == nil {
			return string(b)
		}
		return "[]"
	default:
		allJSONArray := true
		var merged []json.RawMessage
		for _, page := range pages {
			if !page.isJSONArray {
				allJSONArray = false
				break
			}
			merged = append(merged, page.jsonArray...)
		}
		if allJSONArray {
			combined, err := json.MarshalIndent(merged, "", "  ")
			if err == nil {
				return string(combined)
			}
		}

		parts := make([]string, 0, len(pages))
		for _, page := range pages {
			if page.text != "" {
				parts = append(parts, page.text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
}

func extractHTMLTitle(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return ""
	}
	var walk func(*html.Node) string
	walk = func(n *html.Node) string {
		if n.Type == html.ElementNode && n.Data == "title" {
			return collapseWhitespace(textContent(n))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if title := walk(c); title != "" {
				return title
			}
		}
		return ""
	}
	return walk(doc)
}

func extractHTMLLinks(baseURL, s string) []fetchedLink {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	base, _ := url.Parse(baseURL)
	seen := map[string]struct{}{}
	var links []fetchedLink
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href string
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					href = strings.TrimSpace(attr.Val)
					break
				}
			}
			if href != "" {
				if u, err := url.Parse(href); err == nil {
					resolved := href
					if base != nil {
						resolved = base.ResolveReference(u).String()
					}
					if _, ok := seen[resolved]; !ok {
						seen[resolved] = struct{}{}
						links = append(links, fetchedLink{Text: collapseWhitespace(textContent(n)), URL: resolved})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return links
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return sb.String()
}

func detectContentType(contentType string, body []byte) string {
	if strings.TrimSpace(contentType) != "" {
		return contentType
	}
	return http.DetectContentType(body)
}

func decodeBody(body []byte, contentType string) (string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
		params = map[string]string{}
	}
	charsetLabel := strings.TrimSpace(params["charset"])
	if charsetLabel == "" {
		charsetLabel = "utf-8"
	}
	reader, err := charset.NewReaderLabel(charsetLabel, bytes.NewReader(body))
	if err != nil {
		return string(body), err
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return string(body), err
	}
	if strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "json") || strings.Contains(mediaType, "html") || strings.Contains(mediaType, "xml") || mediaType == "" {
		return string(decoded), nil
	}
	return string(body), nil
}

func isHTMLContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

func isJSONContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "+json")
}

func decodeJSONArray(body []byte) ([]json.RawMessage, bool) {
	var arr []json.RawMessage
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, false
	}
	return arr, true
}

func parseNextLink(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}

func resolveAndValidateURL(baseURL, candidate string, checkHost func(string) error) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	nextURL, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(nextURL)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", resolved.Scheme)
	}
	if err := checkHost(resolved.Hostname()); err != nil {
		return "", err
	}
	return resolved.String(), nil
}

func checkPrivateHost(host string) error {
	ips, err := net.LookupHost(host)
	if err != nil {
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("cannot resolve host %q: %v", host, err)
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("blocked: %s is a private/reserved address", host)
		}
		return nil
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("blocked: %s resolves to private address %s", host, ipStr)
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("127.0.0.0/8")},
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
		{mustParseCIDR("169.254.0.0/16")},
		{mustParseCIDR("::1/128")},
		{mustParseCIDR("fe80::/10")},
		{mustParseCIDR("fc00::/7")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustParseCIDR(s string) *net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return network
}

func isBinaryContentType(ct string) bool {
	ct = strings.ToLower(ct)
	binaryPrefixes := []string{"image/", "audio/", "video/", "application/octet-stream", "application/zip", "application/gzip", "application/pdf"}
	for _, p := range binaryPrefixes {
		if strings.Contains(ct, p) {
			return true
		}
	}
	return false
}

func extractText(htmlContent string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var sb strings.Builder
	skip := false
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return collapseWhitespace(sb.String())
		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip = true
			}
			if tag == "br" || tag == "p" || tag == "div" || tag == "li" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" || tag == "h5" || tag == "h6" || tag == "tr" {
				sb.WriteByte('\n')
			}
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip = false
			}
		case html.TextToken:
			if !skip {
				sb.Write(tokenizer.Text())
			}
		}
	}
}

func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n")
}

func formatJSON(body []byte) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err == nil {
		return pretty.String()
	}
	return string(body)
}

func truncateText(s string, maxLen int) (string, bool) {
	if len(s) <= maxLen {
		return s, false
	}

	cut := maxLen
	if cut > len(s) {
		cut = len(s)
	}

	windowStart := cut - 200
	if windowStart < 0 {
		windowStart = 0
	}
	if idx := strings.LastIndex(s[windowStart:cut], "\n"); idx >= 0 {
		cut = windowStart + idx
	}
	if cut <= 0 {
		cut = maxLen
		if cut > len(s) {
			cut = len(s)
		}
	}

	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return strings.TrimRight(s[:cut], "\n"), true
}

const ddgSearchURL = "https://html.duckduckgo.com/html/"
const braveSearchURL = "https://search.brave.com/search"

type searchKind string

const (
	searchKindDDG   searchKind = "ddg"
	searchKindBrave searchKind = "brave"
)

type searchEndpoint struct {
	url  string
	kind searchKind
}

func NewWebSearch() Tool {
	return newWebSearchWithConfiguredEndpoints(
		searchEndpoint{url: ddgSearchURL, kind: searchKindDDG},
		searchEndpoint{url: braveSearchURL, kind: searchKindBrave},
	)
}

func newWebSearchWithEndpoint(endpoint string) Tool {
	return newWebSearchWithConfiguredEndpoints(searchEndpoint{url: endpoint, kind: searchKindDDG})
}

func newWebSearchWithConfiguredEndpoints(endpoints ...searchEndpoint) Tool {
	if len(endpoints) == 0 {
		endpoints = []searchEndpoint{{url: ddgSearchURL, kind: searchKindDDG}, {url: braveSearchURL, kind: searchKindBrave}}
	}

	return Tool{
		Name:        "web_search",
		Description: "Search the web using DuckDuckGo with fallback providers. Returns titles, URLs, and snippets.",
		Parameters: []ParameterDef{
			{Name: "query", Type: "string", Description: "search query", Required: true},
			{Name: "count", Type: "int", Description: "max number of results to return (default 5)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			query, _ := args["query"].(string)
			count := 5
			if v, ok := args["count"].(float64); ok && v > 0 {
				count = int(v)
				if count > 10 {
					count = 10
				}
			}

			results, errText := runWebSearch(ctx, query, count, endpoints)
			if errText != "" {
				return errText, nil
			}
			if len(results) == 0 {
				return "no results found", nil
			}

			var sb strings.Builder
			for i, r := range results {
				fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.title, r.url)
				if r.snippet != "" {
					fmt.Fprintf(&sb, "   %s\n", r.snippet)
				}
				sb.WriteByte('\n')
			}
			return sb.String(), nil
		},
	}
}

func runWebSearch(ctx context.Context, query string, count int, endpoints []searchEndpoint) ([]ddgResult, string) {
	client := &http.Client{Timeout: 15 * time.Second}
	var errs []string

	for _, endpoint := range endpoints {
		results, err := executeSearchRequest(ctx, client, endpoint, query, count)
		if err == nil {
			return results, ""
		}
		errs = append(errs, err.Error())
	}

	if len(errs) == 0 {
		return nil, "no results found"
	}
	return nil, "error: " + strings.Join(errs, "; ")
}

func executeSearchRequest(ctx context.Context, client *http.Client, endpoint searchEndpoint, query string, count int) ([]ddgResult, error) {
	var req *http.Request
	var err error

	switch endpoint.kind {
	case searchKindBrave:
		u, parseErr := url.Parse(endpoint.url)
		if parseErr != nil {
			return nil, parseErr
		}
		q := u.Query()
		q.Set("q", query)
		u.RawQuery = q.Encode()
		req, err = http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
	default:
		formData := url.Values{"q": {query}}
		req, err = http.NewRequestWithContext(ctx, "POST", endpoint.url, strings.NewReader(formData.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", searchProviderName(endpoint), resp.StatusCode)
	}

	results := parseSearchResults(resp.Body, endpoint, count)
	if len(results) == 0 {
		return nil, fmt.Errorf("%s returned no parseable results", searchProviderName(endpoint))
	}
	return results, nil
}

func searchProviderName(endpoint searchEndpoint) string {
	switch endpoint.kind {
	case searchKindBrave:
		return "Brave"
	default:
		return "DDG"
	}
}

func parseSearchResults(r io.Reader, endpoint searchEndpoint, max int) []ddgResult {
	switch endpoint.kind {
	case searchKindBrave:
		return parseBraveResults(r, max)
	default:
		return parseDDGResults(r, max)
	}
}

type ddgResult struct {
	title   string
	url     string
	snippet string
}

func parseDDGResults(r io.Reader, max int) []ddgResult {
	var results []ddgResult
	z := html.NewTokenizer(r)

	var current ddgResult
	var inTitle, inSnippet bool

	for len(results) < max {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}

		switch tt {
		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			tag := string(tn)

			if tag == "a" && hasAttr {
				cls, href := "", ""
				for {
					k, v, more := z.TagAttr()
					switch string(k) {
					case "class":
						cls = string(v)
					case "href":
						href = string(v)
					}
					if !more {
						break
					}
				}
				if strings.Contains(cls, "result__a") {
					current = ddgResult{url: href}
					inTitle = true
					inSnippet = false
				} else if strings.Contains(cls, "result__snippet") {
					inSnippet = true
					inTitle = false
				}
			}

		case html.TextToken:
			text := strings.TrimSpace(string(z.Text()))
			if text == "" {
				break
			}
			if inTitle {
				current.title = text
			} else if inSnippet {
				current.snippet += text
			}

		case html.EndTagToken:
			tn, _ := z.TagName()
			if string(tn) == "a" {
				if inTitle && current.title != "" && current.url != "" {
					inTitle = false
				} else if inSnippet {
					inSnippet = false
					if current.title != "" {
						results = append(results, current)
						current = ddgResult{}
					}
				}
			}
		}
	}
	return results
}

func parseBraveResults(r io.Reader, max int) []ddgResult {
	var results []ddgResult
	z := html.NewTokenizer(r)

	var current ddgResult
	var inTitle, inSnippet bool

	for len(results) < max {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}

		switch tt {
		case html.StartTagToken:
			tn, hasAttr := z.TagName()
			tag := string(tn)
			if !hasAttr {
				continue
			}

			cls, href := "", ""
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "class":
					cls = string(v)
				case "href":
					href = string(v)
				}
				if !more {
					break
				}
			}

			if tag == "a" && href != "" && (strings.Contains(cls, "result-header") || strings.Contains(cls, "snippet-title") || strings.Contains(cls, "heading")) {
				current = ddgResult{url: href}
				inTitle = true
				inSnippet = false
			} else if (tag == "div" || tag == "p") && (strings.Contains(cls, "snippet-description") || strings.Contains(cls, "description")) {
				inSnippet = true
			}

		case html.TextToken:
			text := strings.TrimSpace(string(z.Text()))
			if text == "" {
				break
			}
			if inTitle {
				if current.title == "" {
					current.title = text
				} else {
					current.title += " " + text
				}
			} else if inSnippet {
				if current.snippet == "" {
					current.snippet = text
				} else {
					current.snippet += " " + text
				}
			}

		case html.EndTagToken:
			tn, _ := z.TagName()
			tag := string(tn)
			if tag == "a" && inTitle {
				inTitle = false
				if current.title != "" && current.url != "" && current.snippet == "" {
					results = append(results, current)
					current = ddgResult{}
				}
			}
			if (tag == "div" || tag == "p") && inSnippet {
				inSnippet = false
				if current.title != "" && current.url != "" {
					results = append(results, current)
					current = ddgResult{}
				}
			}
		}
	}
	return results
}
