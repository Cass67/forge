package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"forge/internal/auth"
	"forge/internal/config"
)

const providerProbeBody = "ping"

func ProbeProviderModels(cfg *config.Config, tokens *auth.Tokens, currentModel string, available []string) []string {
	ref := ParseModelRef(currentModel)
	if ref.Provider == "" {
		return sortModelsByHealth(available)
	}

	var provider *CompatProvider
	providers := BuildCompatProviders(cfg, tokens)
	for i := range providers {
		p := &providers[i]
		if p.Name == ref.Provider && strings.TrimSpace(p.KeyFn()) != "" {
			provider = p
			break
		}
	}
	if provider == nil {
		return sortModelsByHealth(available)
	}

	var providerModels []string
	prefix := provider.Name + "/"
	for _, model := range available {
		if strings.HasPrefix(model, prefix) {
			providerModels = append(providerModels, model)
		}
	}
	if len(providerModels) == 0 {
		return sortModelsByHealth(available)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	probeCompatibleProviderModels(ctx, http.DefaultClient, provider.BaseURL, provider.KeyFn(), provider.Name, providerModels)
	return sortModelsByHealth(available)
}

func probeCompatibleProviderModels(ctx context.Context, client *http.Client, baseURL, apiKey, provider string, models []string) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || len(models) == 0 {
		return
	}

	const maxParallel = 4
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for _, model := range uniqueStrings(models) {
		model := model
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			err := probeCompatibleModel(ctx, client, baseURL, apiKey, provider, model)
			if err != nil {
				ReportModelFailure(model, err)
				return
			}
			ReportModelSuccess(model)
		}()
	}
	wg.Wait()
}

func probeCompatibleModel(ctx context.Context, client *http.Client, baseURL, apiKey, provider, model string) error {
	ref := ParseModelRef(model)
	apiModel := compatAPIModel(provider, ref, ref.Model)
	payload := map[string]any{
		"model": apiModel,
		"messages": []map[string]string{
			{"role": "user", "content": providerProbeBody},
		},
		"max_tokens": 1,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	applyProviderDiscoveryHeaders(req, provider)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg := strings.TrimSpace(readProbeErrorBody(resp.Body))
	if msg == "" {
		return fmt.Errorf("%s", resp.Status)
	}
	return fmt.Errorf("%s: %s", resp.Status, msg)
}

func readProbeErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 512))
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err == nil {
		if msg, ok := payload["message"].(string); ok {
			return msg
		}
		if errMap, ok := payload["error"].(map[string]any); ok {
			if msg, ok := errMap["message"].(string); ok {
				return msg
			}
		}
	}
	return strings.TrimSpace(string(data))
}
