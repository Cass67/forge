package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// NewWebFetch creates the web_fetch tool with SSRF protection enabled.
func NewWebFetch() Tool {
	return newWebFetch(checkPrivateHost)
}

func newWebFetch(checkHost func(string) error) Tool {
	return Tool{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its content as text. HTML is stripped to text, JSON returned as-is. Only http/https URLs allowed.",
		Parameters: []ParameterDef{
			{Name: "url", Type: "string", Description: "URL to fetch", Required: true},
			{Name: "max_length", Type: "int", Description: "max response length in bytes (default 50000)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			rawURL, _ := args["url"].(string)
			maxLen := 50000
			if v, ok := args["max_length"].(float64); ok && v > 0 {
				maxLen = int(v)
			}

			parsed, err := url.Parse(rawURL)
			if err != nil {
				return fmt.Sprintf("error: invalid URL: %v", err), nil
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return fmt.Sprintf("error: only http/https schemes allowed, got %q", parsed.Scheme), nil
			}

			host := parsed.Hostname()
			if err := checkHost(host); err != nil {
				return fmt.Sprintf("error: %v", err), nil
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

			req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
			req.Header.Set("User-Agent", "forge/1.0")

			resp, err := client.Do(req)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				return fmt.Sprintf("error: HTTP %d %s", resp.StatusCode, resp.Status), nil
			}

			ct := resp.Header.Get("Content-Type")

			if isBinaryContentType(ct) {
				return fmt.Sprintf("error: binary content type %q, cannot display", ct), nil
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen)+1))
			if err != nil {
				return fmt.Sprintf("error reading response: %v", err), nil
			}
			truncated := len(body) > maxLen
			if truncated {
				body = body[:maxLen]
			}

			var result string
			if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
				result = extractText(string(body))
			} else {
				result = string(body)
			}

			if truncated {
				result += "\n... truncated at " + fmt.Sprintf("%d", maxLen) + " bytes"
			}
			return result, nil
		},
	}
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

const braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

func NewWebSearch(apiKey string) Tool {
	return newWebSearchWithEndpoint(apiKey, braveSearchEndpoint)
}

func newWebSearchWithEndpoint(apiKey, endpoint string) Tool {
	return Tool{
		Name:        "web_search",
		Description: "Search the web using Brave Search. Returns titles, URLs, and snippets.",
		Parameters: []ParameterDef{
			{Name: "query", Type: "string", Description: "search query", Required: true},
			{Name: "count", Type: "int", Description: "number of results (default 5, max 10)", Required: false},
		},
		AutoApprove: true,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if apiKey == "" {
				return "web_search unavailable: set BRAVE_API_KEY or keys.brave in config", nil
			}

			query, _ := args["query"].(string)
			count := 5
			if v, ok := args["count"].(float64); ok && v > 0 {
				count = int(v)
				if count > 10 {
					count = 10
				}
			}

			reqURL := fmt.Sprintf("%s?q=%s&count=%d", endpoint, url.QueryEscape(query), count)
			req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
			req.Header.Set("X-Subscription-Token", apiKey)
			req.Header.Set("Accept", "application/json")

			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				return fmt.Sprintf("error: Brave API returned HTTP %d: %s", resp.StatusCode, body), nil
			}

			var result braveSearchResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Sprintf("error decoding response: %v", err), nil
			}

			if len(result.Web.Results) == 0 {
				return "no results found", nil
			}

			var sb strings.Builder
			for i, r := range result.Web.Results {
				fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description)
			}
			return sb.String(), nil
		},
	}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}
