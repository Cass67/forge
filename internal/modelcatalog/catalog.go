// Package modelcatalog provides model metadata from models.dev.
// It ships a bundled snapshot and refreshes it hourly in the background.
package modelcatalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"forge/internal/fsutil"
)

//go:embed snapshot.json
var snapshotData []byte

// ModelInfo holds capability flags for a single model.
type ModelInfo struct {
	Reasoning           bool
	Temperature         bool
	ToolCall            bool
	ContextWindow       int
	OutputLimit         int
	SupportsImages      bool
	MaxImageBytes       int64
	SupportedImageMIMEs []string
	// ReasoningEfforts lists the effort levels the provider advertises for this
	// model (e.g. ["low","medium","high"]), taken from models.dev
	// reasoning_options. Empty when the model exposes no effort control.
	ReasoningEfforts []string
}

type OpenCodeGoModelCapability struct {
	WireAPI                         string
	SDK                             string
	InterleavedReasoningField       string
	SupportsRequiredChatToolChoice  bool
	SupportedByOpenAICompatibleChat bool
}

type CustomProviderRoute struct {
	APIModel string `json:"api_model"`
	APIBase  string `json:"api_base,omitempty"`
	WireAPI  string `json:"wire_api,omitempty"`
}

// providerData mirrors the relevant fields from models.dev's JSON structure.
type providerData struct {
	Models map[string]modelEntry `json:"models"`
}

type modelEntry struct {
	Reasoning        bool              `json:"reasoning"`
	Temperature      bool              `json:"temperature"`
	ToolCall         bool              `json:"tool_call"`
	ReasoningOptions []reasoningOption `json:"reasoning_options"`
	Limit            struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

type reasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// effortValues extracts the effort-type reasoning levels from a model's
// reasoning_options. Returns nil when the model advertises no effort control.
func (e modelEntry) effortValues() []string {
	for _, opt := range e.ReasoningOptions {
		if opt.Type == "effort" && len(opt.Values) > 0 {
			return append([]string(nil), opt.Values...)
		}
	}
	return nil
}

// forgeToModelsDev maps forge provider labels to models.dev provider IDs.
var forgeToModelsDev = map[string]string{
	"openai":      "openai",
	"chatgpt":     "openai",
	"copilot":     "github-copilot",
	"openrouter":  "openrouter",
	"nvidia":      "nvidia",
	"xai":         "xai",
	"mistral":     "mistral",
	"perplexity":  "perplexity",
	"cerebras":    "cerebras",
	"groq":        "groq",
	"together":    "togetherai",
	"deepinfra":   "deepinfra",
	"anthropic":   "anthropic",
	"deepseek":    "deepseek",
	"google":      "google",
	"cohere":      "cohere",
	"fireworks":   "fireworks-ai",
	"novita":      "novita-ai",
	"opencode":    "opencode",
	"opencode-go": "opencode-go",
}

var openCodeGoModelOrder = []string{
	"glm-5.1",
	"glm-5",
	"kimi-k2.6",
	"kimi-k2.5",
	"deepseek-v4-pro",
	"deepseek-v4-flash",
	"mimo-v2.5-pro",
	"mimo-v2.5",
	"mimo-v2-pro",
	"mimo-v2-omni",
}

var openCodeGoModelCapabilities = map[string]OpenCodeGoModelCapability{
	"glm-5.1":           openCodeGoOpenAICompatibleReasoningModel(),
	"glm-5":             openCodeGoOpenAICompatibleReasoningModel(),
	"kimi-k2.6":         openCodeGoOpenAICompatibleReasoningModel(),
	"kimi-k2.5":         openCodeGoOpenAICompatibleReasoningModel(),
	"deepseek-v4-pro":   openCodeGoOpenAICompatibleReasoningModel(),
	"deepseek-v4-flash": openCodeGoOpenAICompatibleReasoningModel(),
	"mimo-v2.5-pro":     openCodeGoOpenAICompatibleReasoningModel(),
	"mimo-v2.5":         openCodeGoOpenAICompatibleReasoningModel(),
	"mimo-v2-pro":       openCodeGoOpenAICompatibleReasoningModel(),
	"mimo-v2-omni":      openCodeGoOpenAICompatibleReasoningModel(),
	"minimax-m2.7":      {WireAPI: "messages", SDK: "@ai-sdk/anthropic"},
	"minimax-m2.5":      {WireAPI: "messages", SDK: "@ai-sdk/anthropic"},
	"qwen3.6-plus":      {WireAPI: "chat", SDK: "@ai-sdk/alibaba"},
	"qwen3.5-plus":      {WireAPI: "chat", SDK: "@ai-sdk/alibaba"},
	"hy3-preview":       {},
}

func openCodeGoOpenAICompatibleReasoningModel() OpenCodeGoModelCapability {
	return OpenCodeGoModelCapability{
		WireAPI:                         "chat",
		SDK:                             "@ai-sdk/openai-compatible",
		InterleavedReasoningField:       "reasoning_content",
		SupportsRequiredChatToolChoice:  false,
		SupportedByOpenAICompatibleChat: true,
	}
}

func OpenCodeGoSupportedChatModels() []string {
	out := make([]string, 0, len(openCodeGoModelOrder))
	seen := make(map[string]bool, len(openCodeGoModelOrder))
	for _, model := range openCodeGoModelOrder {
		cap, ok := openCodeGoModelCapabilities[model]
		if ok && cap.SupportedByOpenAICompatibleChat {
			out = append(out, model)
			seen[model] = true
		}
	}
	// New models from the models.dev catalog (refreshed hourly / on launch)
	// that the hardcoded list doesn't know about yet.
	live := ProviderModels("opencode-go")
	sort.Strings(live)
	for _, model := range live {
		if !seen[model] && OpenCodeGoModelSupportedByOpenAICompatibleChat(model) {
			out = append(out, model)
		}
	}
	return out
}

func OpenCodeGoModelCapabilityFor(model string) (OpenCodeGoModelCapability, bool) {
	model = normalizeOpenCodeGoModel(model)
	if model == "" {
		return OpenCodeGoModelCapability{}, false
	}
	cap, ok := openCodeGoModelCapabilities[model]
	if ok {
		return cap, true
	}
	// Unknown model present in the models.dev opencode-go catalog: assume the
	// provider's default openai-compatible chat wire (only anthropic/alibaba-wire
	// models need explicit entries above).
	if Lookup("opencode-go", model) != nil {
		return openCodeGoOpenAICompatibleReasoningModel(), true
	}
	return OpenCodeGoModelCapability{}, false
}

func OpenCodeGoModelSupportedByOpenAICompatibleChat(model string) bool {
	cap, ok := OpenCodeGoModelCapabilityFor(model)
	return ok && cap.SupportedByOpenAICompatibleChat
}

func normalizeOpenCodeGoModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "opencode-go/")
	return strings.TrimSpace(model)
}

var (
	mu             sync.RWMutex
	bundledCatalog = parseSnapshot(snapshotData)
	catalog        map[string]providerData // keyed by models.dev provider ID
	customSources  = map[string]customSource{}
)

type imageCapability struct {
	MaxBytes int64
	MIMEs    []string
}

// imageCapableModels maps provider/model to image capability metadata.
// These override the catalog (which doesn't yet carry image metadata).
var imageCapableModels = map[string]map[string]imageCapability{
	"openai": {
		"gpt-4o":      {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-4-turbo": {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-5.5":     {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-5":       {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"o3":          {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"o4-mini":     {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
	},
	"chatgpt": {
		"gpt-4o":      {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-4-turbo": {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-5.5":     {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"gpt-5":       {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"o3":          {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
		"o4-mini":     {MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}},
	},
}

// imageGatedProviders lists providers whose models must be explicitly
// declared image-capable (via image_models in a custom provider TOML) before
// forge sends image chat parts. Guarded by mu together with the custom
// entries added to imageCapableModels.
var imageGatedProviders = map[string]bool{}

// RegisterCustomProviderImageModels gates providerID's image sending and marks
// the given models (exact name or prefix, same matching as the builtin table)
// as accepting image chat parts.
func RegisterCustomProviderImageModels(providerID string, models []string) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	imageGatedProviders[providerID] = true
	if len(models) == 0 {
		return
	}
	caps, ok := imageCapableModels[providerID]
	if !ok {
		caps = map[string]imageCapability{}
		imageCapableModels[providerID] = caps
	}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		caps[m] = imageCapability{MaxBytes: 20 * 1024 * 1024, MIMEs: []string{"image/png", "image/jpeg", "image/gif"}}
	}
}

// AllowsImageParts reports whether image chat parts may be sent to
// providerID/modelID. Non-gated providers always allow (capability unknown);
// gated (custom TOML) providers allow only declared image_models.
func AllowsImageParts(providerID, modelID string) bool {
	mu.RLock()
	gated := imageGatedProviders[strings.TrimSpace(providerID)]
	mu.RUnlock()
	if !gated {
		return true
	}
	info := lookupImageCapabilityOnly(providerID, modelID)
	return info != nil && info.SupportsImages
}

type customSource struct {
	URL     string
	Headers map[string]string
	KeyFn   func() string
}

type customProviderCache struct {
	Order  []string                       `json:"order,omitempty"`
	Models map[string]modelEntry          `json:"models"`
	Routes map[string]CustomProviderRoute `json:"routes,omitempty"`
}

func init() {
	catalog = bundledCatalog
	go refreshLoop()
}

func parseSnapshot(data []byte) map[string]providerData {
	var raw map[string]providerData
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]providerData{}
	}
	return raw
}

func refreshLoop() {
	// Attempt to load from disk cache first, then start periodic refresh.
	if cached := loadDiskCache(); cached != nil {
		mu.Lock()
		catalog = cached
		mu.Unlock()
	} else {
		// No fresh disk cache: refresh now instead of running on the
		// stale bundled snapshot for the first hour.
		refresh()
	}
	for {
		time.Sleep(time.Hour)
		refresh()
	}
}

func refresh() {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://models.dev/api.json")
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var raw map[string]providerData
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return
	}

	mu.Lock()
	catalog = raw
	mu.Unlock()

	writeDiskCache(raw)
}

func cacheFilePath() string {
	return fsutil.ForgeConfigPath("models.json")
}

func loadDiskCache() map[string]providerData {
	path := cacheFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > 24*time.Hour {
		return nil // stale
	}
	return parseSnapshot(data)
}

func writeDiskCache(data map[string]providerData) {
	path := cacheFilePath()
	if path == "" {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}

func RegisterCustomProviderSource(providerID, url string, headers map[string]string, keyFn func() string) {
	providerID = strings.TrimSpace(providerID)
	url = strings.TrimSpace(url)
	refreshedThisRun.Delete(providerID)
	mu.Lock()
	defer mu.Unlock()
	if providerID == "" || url == "" {
		delete(customSources, providerID)
		return
	}
	copied := make(map[string]string, len(headers))
	for k, v := range headers {
		copied[k] = v
	}
	customSources[providerID] = customSource{
		URL:     url,
		Headers: copied,
		KeyFn:   keyFn,
	}
}

// Lookup returns capability info for a model. providerID is the forge provider
// label (e.g. "openai", "copilot", "openrouter"). modelID is the bare model
// name as sent to the provider API (no provider prefix).
// Returns nil when the model is not found in the catalog.
func Lookup(providerID, modelID string) *ModelInfo {
	mdevID, ok := forgeToModelsDev[providerID]
	if !ok {
		mdevID = providerID
	}

	mu.RLock()
	provider, ok := catalog[mdevID]
	mu.RUnlock()
	liveInfo, liveOK := lookupModelInfo(provider, ok, modelID)
	bundledInfo, bundledOK := lookupModelInfo(bundledCatalog[mdevID], bundledCatalog != nil, modelID)

	switch {
	case liveOK && bundledOK:
		info := mergeModelInfo(liveInfo, bundledInfo)
		injectImageCapability(providerID, modelID, info)
		return info
	case liveOK:
		injectImageCapability(providerID, modelID, liveInfo)
		return liveInfo
	case bundledOK:
		injectImageCapability(providerID, modelID, bundledInfo)
		return bundledInfo
	default:
		info := lookupCustomProvider(providerID, modelID)
		if info != nil {
			injectImageCapability(providerID, modelID, info)
			return info
		}
		// Model not found in any catalog, but may still have hardcoded image capability.
		// Create a minimal ModelInfo just for the capability check.
		imageInfo := lookupImageCapabilityOnly(providerID, modelID)
		if imageInfo != nil {
			return imageInfo
		}
		return nil
	}
}

func lookupImageCapabilityOnly(providerID, modelID string) *ModelInfo {
	mu.RLock()
	defer mu.RUnlock()
	providerImages, ok := imageCapableModels[providerID]
	if !ok {
		return nil
	}
	cap, ok := providerImages[modelID]
	if !ok {
		// Try prefix match (e.g. "gpt-5.5-preview" matches "gpt-5.5*")
		for prefix, c := range providerImages {
			if strings.HasPrefix(modelID, prefix+"-") || strings.HasPrefix(modelID, prefix+".") {
				cap = c
				break
			}
		}
		if cap.MaxBytes == 0 {
			return nil
		}
	}
	return &ModelInfo{
		SupportsImages:      true,
		MaxImageBytes:       cap.MaxBytes,
		SupportedImageMIMEs: cap.MIMEs,
	}
}

func injectImageCapability(providerID, modelID string, info *ModelInfo) {
	if info == nil {
		return
	}
	mu.RLock()
	cap, ok := imageCapableModels[providerID][modelID]
	mu.RUnlock()
	if !ok {
		return
	}
	info.SupportsImages = true
	info.MaxImageBytes = cap.MaxBytes
	info.SupportedImageMIMEs = cap.MIMEs
}

func lookupModelInfo(provider providerData, providerOK bool, modelID string) (*ModelInfo, bool) {
	if !providerOK {
		return nil, false
	}
	entry, ok := provider.Models[modelID]
	if !ok {
		return nil, false
	}
	return &ModelInfo{
		Reasoning:        entry.Reasoning,
		Temperature:      entry.Temperature,
		ToolCall:         entry.ToolCall,
		ContextWindow:    entry.Limit.Context,
		OutputLimit:      entry.Limit.Output,
		ReasoningEfforts: entry.effortValues(),
	}, true
}

func mergeModelInfo(primary, fallback *ModelInfo) *ModelInfo {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	out := *primary
	out.Reasoning = out.Reasoning || fallback.Reasoning
	out.Temperature = out.Temperature || fallback.Temperature
	out.ToolCall = out.ToolCall || fallback.ToolCall
	if out.ContextWindow <= 0 {
		out.ContextWindow = fallback.ContextWindow
	}
	if out.OutputLimit <= 0 {
		out.OutputLimit = fallback.OutputLimit
	}
	if len(out.ReasoningEfforts) == 0 {
		out.ReasoningEfforts = fallback.ReasoningEfforts
	}
	return &out
}

// ProviderModels returns all tool-callable model IDs for a forge provider.
// Model IDs are returned bare (no provider prefix).
func ProviderModels(providerID string) []string {
	mdevID, ok := forgeToModelsDev[providerID]
	if !ok {
		if cache, ok := loadCustomProviderData(providerID); ok {
			if len(cache.Order) > 0 {
				return append([]string(nil), cache.Order...)
			}
			out := make([]string, 0, len(cache.Models))
			for id := range cache.Models {
				out = append(out, id)
			}
			sort.Strings(out)
			return out
		}
		mdevID = providerID
	}

	mu.RLock()
	provider, ok := catalog[mdevID]
	mu.RUnlock()
	if !ok {
		return nil
	}

	out := make([]string, 0, len(provider.Models))
	for id, m := range provider.Models {
		if m.ToolCall {
			out = append(out, id)
		}
	}
	return out
}

func lookupCustomProvider(providerID, modelID string) *ModelInfo {
	data, ok := loadCustomProviderData(providerID)
	if !ok {
		return nil
	}
	info, ok := lookupModelInfo(providerData{Models: data.Models}, true, modelID)
	if !ok {
		return nil
	}
	return info
}

func CustomProviderRouteForModel(providerID, modelID string) *CustomProviderRoute {
	data, ok := loadCustomProviderData(providerID)
	if !ok {
		return nil
	}
	route, ok := data.Routes[modelID]
	if !ok {
		return nil
	}
	copyRoute := route
	return &copyRoute
}

func CustomProviderModels(providerID string) []string {
	data, ok := loadCustomProviderData(providerID)
	if !ok {
		return nil
	}
	if len(data.Order) > 0 {
		return append([]string(nil), data.Order...)
	}
	out := make([]string, 0, len(data.Models))
	for id := range data.Models {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func customProviderCachePath(providerID string) string {
	return filepath.Join(fsutil.ForgeConfigDir(), "providers", providerID+"-models.json")
}

// refreshedThisRun ensures each provider is fetched live at most once per
// process. The fetch used to run before the cache was consulted, which put a
// network round trip per provider on the startup path; now a cache that is
// still fresh answers immediately and the refresh happens behind it.
var refreshedThisRun sync.Map

func loadCustomProviderData(providerID string) (customProviderCache, bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return customProviderCache{}, false
	}
	_, refreshed := refreshedThisRun.Load(providerID)
	// A legacy cache carries models but no routes, so it cannot answer routing
	// questions: that one still has to be refreshed before it is used.
	if data, ok := loadCustomProviderCache(providerID); ok && len(data.Routes) > 0 {
		if !refreshed {
			refreshCustomProviderCacheAsync(providerID)
		}
		return data, true
	}
	if !refreshed {
		if data, ok := refreshCustomProviderCache(providerID); ok {
			refreshedThisRun.Store(providerID, true)
			return data, true
		}
	}
	if data, ok := loadCustomProviderCache(providerID); ok {
		return data, true
	}
	return customProviderCache{}, false
}

// refreshCustomProviderCacheAsync updates the on-disk cache in the background,
// so the next launch sees the source's current model list.
func refreshCustomProviderCacheAsync(providerID string) {
	if _, loaded := refreshedThisRun.LoadOrStore(providerID, true); loaded {
		return
	}
	go func() { _, _ = refreshCustomProviderCache(providerID) }()
}

func loadCustomProviderCache(providerID string) (customProviderCache, bool) {
	path := customProviderCachePath(providerID)
	if path == "" {
		return customProviderCache{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return customProviderCache{}, false
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > 24*time.Hour {
		return customProviderCache{}, false
	}
	var cache customProviderCache
	if err := json.Unmarshal(data, &cache); err == nil && len(cache.Models) > 0 {
		if len(cache.Order) == 0 {
			for id := range cache.Models {
				cache.Order = append(cache.Order, id)
			}
			sort.Strings(cache.Order)
		}
		if cache.Routes == nil {
			cache.Routes = map[string]CustomProviderRoute{}
		}
		return cache, true
	}
	var legacy providerData
	if err := json.Unmarshal(data, &legacy); err != nil || len(legacy.Models) == 0 {
		return customProviderCache{}, false
	}
	order := make([]string, 0, len(legacy.Models))
	for id := range legacy.Models {
		order = append(order, id)
	}
	sort.Strings(order)
	return customProviderCache{Order: order, Models: legacy.Models, Routes: map[string]CustomProviderRoute{}}, true
}

func refreshCustomProviderCache(providerID string) (customProviderCache, bool) {
	mu.RLock()
	source, ok := customSources[providerID]
	mu.RUnlock()
	if !ok || strings.TrimSpace(source.URL) == "" || source.KeyFn == nil {
		return customProviderCache{}, false
	}

	isModelsDev := strings.Contains(source.URL, "models.dev/api.json")

	apiKey := strings.TrimSpace(source.KeyFn())
	if !isModelsDev && apiKey == "" {
		return customProviderCache{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return customProviderCache{}, false
	}
	if !isModelsDev && apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range source.Headers {
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return customProviderCache{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return customProviderCache{}, false
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return customProviderCache{}, false
	}

	if isModelsDev {
		return parseModelsDevPayload(providerID, raw)
	}

	provider, ok := parseCustomProviderPayload(providerID, raw)
	if !ok {
		return customProviderCache{}, false
	}
	writeCustomProviderCache(providerID, provider)
	return provider, true
}

func parseModelsDevPayload(providerID string, raw json.RawMessage) (customProviderCache, bool) {
	var provider struct {
		Models map[string]modelEntry `json:"models"`
	}
	if err := json.Unmarshal(raw, &provider); err != nil {
		return customProviderCache{}, false
	}
	if len(provider.Models) == 0 {
		return customProviderCache{}, false
	}
	models := make(map[string]modelEntry, len(provider.Models))
	order := make([]string, 0, len(provider.Models))
	for id, entry := range provider.Models {
		if entry.ToolCall {
			models[id] = entry
			order = append(order, id)
		}
	}
	if len(models) == 0 {
		return customProviderCache{}, false
	}
	sort.Strings(order)
	return customProviderCache{Order: order, Models: models, Routes: map[string]CustomProviderRoute{}}, true
}

func writeCustomProviderCache(providerID string, provider customProviderCache) {
	path := customProviderCachePath(providerID)
	if path == "" {
		return
	}
	data, err := json.Marshal(provider)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

type customProviderPayload struct {
	Data   []customProviderItem  `json:"data"`
	Models map[string]modelEntry `json:"models"`
}

type customProviderItem struct {
	Key         string `json:"key"`
	ID          string `json:"id"`
	Model       string `json:"model"`
	ModelName   string `json:"model_name"`
	Reasoning   *bool  `json:"reasoning"`
	Temperature *bool  `json:"temperature"`
	ToolCall    *bool  `json:"tool_call"`
	Limit       struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	ModelInfo     customProviderModelInfo     `json:"model_info"`
	LiteLLMParams customProviderLiteLLMParams `json:"litellm_params"`
}

type customProviderModelInfo struct {
	Key                          string   `json:"key"`
	Mode                         string   `json:"mode"`
	SupportedAPIList             []string `json:"supported_api_list"`
	IsReasoningModel             *bool    `json:"is_reasoning_model"`
	Reasoning                    *bool    `json:"reasoning"`
	SupportsReasoning            *bool    `json:"supports_reasoning"`
	SupportsReasoningEffort      *bool    `json:"supports_reasoning_effort"`
	Temperature                  *bool    `json:"temperature"`
	SupportsTemperature          *bool    `json:"supports_temperature"`
	ToolCall                     *bool    `json:"tool_call"`
	SupportsFunctionCalling      *bool    `json:"supports_function_calling"`
	SupportsParallelFunctionCall *bool    `json:"supports_parallel_function_calling"`
	SupportsResponses            *bool    `json:"supports_responses"`
	SupportsResponsesAPI         *bool    `json:"supports_responses_api"`
	WireAPI                      string   `json:"wire_api"`
	ContextWindow                int      `json:"context_window"`
	MaxInputTokens               int      `json:"max_input_tokens"`
	MaxOutputTokens              int      `json:"max_output_tokens"`
	MaxTokens                    int      `json:"max_tokens"`
}

type customProviderLiteLLMParams struct {
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
}

func parseCustomProviderPayload(providerID string, raw json.RawMessage) (customProviderCache, bool) {
	var payload customProviderPayload
	if err := json.Unmarshal(raw, &payload); err == nil {
		if len(payload.Models) > 0 {
			order := make([]string, 0, len(payload.Models))
			for id := range payload.Models {
				order = append(order, id)
			}
			sort.Strings(order)
			return customProviderCache{Order: order, Models: payload.Models, Routes: map[string]CustomProviderRoute{}}, true
		}
		if len(payload.Data) > 0 {
			models := make(map[string]modelEntry, len(payload.Data))
			routes := make(map[string]CustomProviderRoute, len(payload.Data))
			order := make([]string, 0, len(payload.Data))
			for _, item := range payload.Data {
				aliases := customProviderAliases(providerID, item)
				if len(aliases) == 0 {
					continue
				}
				id := aliases[0]
				if _, exists := models[id]; !exists {
					order = append(order, id)
				}
				entry := modelEntry{
					Reasoning:   anyTrue(item.Reasoning, item.ModelInfo.Reasoning, item.ModelInfo.SupportsReasoning, item.ModelInfo.SupportsReasoningEffort),
					Temperature: pickBool(true, item.Temperature, item.ModelInfo.Temperature, item.ModelInfo.SupportsTemperature),
					ToolCall:    anyTrue(item.ToolCall, item.ModelInfo.ToolCall, item.ModelInfo.SupportsFunctionCalling, item.ModelInfo.SupportsParallelFunctionCall),
					Limit: struct {
						Context int `json:"context"`
						Output  int `json:"output"`
					}{
						Context: firstPositive(item.ModelInfo.MaxInputTokens, item.ModelInfo.ContextWindow, item.Limit.Context),
						Output:  firstPositive(item.ModelInfo.MaxOutputTokens, item.ModelInfo.MaxTokens, item.Limit.Output),
					},
				}
				if item.ModelInfo.IsReasoningModel != nil {
					entry.Reasoning = entry.Reasoning || *item.ModelInfo.IsReasoningModel
				}
				route := CustomProviderRoute{
					APIModel: firstNonEmpty(item.LiteLLMParams.Model, id),
					APIBase:  strings.TrimSpace(item.LiteLLMParams.APIBase),
					WireAPI:  customProviderWireAPI(item),
				}
				for _, alias := range aliases {
					models[alias] = entry
					routes[alias] = route
				}
			}
			if len(models) > 0 {
				return customProviderCache{Order: order, Models: models, Routes: routes}, true
			}
		}
	}

	var direct []customProviderItem
	if err := json.Unmarshal(raw, &direct); err == nil && len(direct) > 0 {
		return parseCustomProviderPayload(providerID, mustJSON(customProviderPayload{Data: direct}))
	}
	return customProviderCache{}, false
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func deriveModelKey(providerID, apiModel string) string {
	apiModel = strings.TrimSpace(apiModel)
	if apiModel == "" {
		return ""
	}
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		prefix := providerID + "/"
		if strings.HasPrefix(apiModel, prefix) && len(apiModel) > len(prefix) {
			return apiModel[len(prefix):]
		}
	}
	switch {
	case strings.HasPrefix(apiModel, "openai/"),
		strings.HasPrefix(apiModel, "chatgpt/"),
		strings.HasPrefix(apiModel, "anthropic/"),
		strings.HasPrefix(apiModel, "claude/"),
		strings.HasPrefix(apiModel, "groq/"),
		strings.HasPrefix(apiModel, "xai/"),
		strings.HasPrefix(apiModel, "mistral/"),
		strings.HasPrefix(apiModel, "openrouter/"):
		if idx := strings.Index(apiModel, "/"); idx >= 0 && idx+1 < len(apiModel) {
			return apiModel[idx+1:]
		}
	}
	return apiModel
}

func customProviderAliases(providerID string, item customProviderItem) []string {
	candidates := []string{
		item.ModelName,
		item.ModelInfo.Key,
		item.Key,
		deriveModelKey(providerID, item.LiteLLMParams.Model),
		item.Model,
		item.ID,
		strings.TrimSpace(item.LiteLLMParams.Model),
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func anyTrue(values ...*bool) bool {
	for _, v := range values {
		if v != nil && *v {
			return true
		}
	}
	return false
}

func pickBool(defaultValue bool, values ...*bool) bool {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return defaultValue
}

func customProviderWireAPI(item customProviderItem) string {
	hasResponses := false
	hasChat := false
	for _, entry := range item.ModelInfo.SupportedAPIList {
		switch strings.ToUpper(strings.TrimSpace(entry)) {
		case "RESPONSES", "RESPONSE":
			hasResponses = true
		case "CHAT_COMPLETIONS", "CHAT", "COMPLETIONS", "COMPLETION":
			hasChat = true
		}
	}
	switch {
	case hasResponses && !hasChat:
		return "responses"
	case hasChat && !hasResponses:
		return "chat"
	}
	switch strings.ToLower(strings.TrimSpace(firstNonEmpty(item.ModelInfo.WireAPI, item.ModelInfo.Mode))) {
	case "responses", "response":
		return "responses"
	case "chat", "chat_completions", "chat-completions", "completions", "completion":
		return "chat"
	}
	if anyTrue(item.ModelInfo.SupportsResponses, item.ModelInfo.SupportsResponsesAPI) {
		return "responses"
	}
	return ""
}
