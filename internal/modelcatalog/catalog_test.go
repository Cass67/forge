package modelcatalog

import "testing"

func TestLookupFallsBackToBundledLimitsWhenLiveCatalogOmitsThem(t *testing.T) {
	origCatalog := catalog
	origBundled := bundledCatalog
	t.Cleanup(func() {
		catalog = origCatalog
		bundledCatalog = origBundled
	})

	bundledCatalog = map[string]providerData{
		"openrouter": {
			Models: map[string]modelEntry{
				"arcee-ai/trinity-large-preview:free": {
					Temperature: true,
					ToolCall:    true,
					Limit: struct {
						Context int `json:"context"`
						Output  int `json:"output"`
					}{Context: 131072, Output: 32768},
				},
			},
		},
	}
	catalog = map[string]providerData{
		"openrouter": {
			Models: map[string]modelEntry{
				"arcee-ai/trinity-large-preview:free": {
					Temperature: true,
					ToolCall:    true,
				},
			},
		},
	}

	info := Lookup("openrouter", "arcee-ai/trinity-large-preview:free")
	if info == nil {
		t.Fatal("Lookup returned nil")
	}
	if info.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want %d", info.ContextWindow, 131072)
	}
	if info.OutputLimit != 32768 {
		t.Fatalf("OutputLimit = %d, want %d", info.OutputLimit, 32768)
	}
	if !info.Temperature || !info.ToolCall {
		t.Fatalf("capability flags lost: %+v", info)
	}
}

func TestMergeModelInfoPrefersLiveLimitsWhenPresent(t *testing.T) {
	info := mergeModelInfo(
		&ModelInfo{Temperature: true, ContextWindow: 8192, OutputLimit: 4096},
		&ModelInfo{Reasoning: true, ToolCall: true, ContextWindow: 131072, OutputLimit: 32768},
	)
	if info == nil {
		t.Fatal("mergeModelInfo returned nil")
	}
	if info.ContextWindow != 8192 {
		t.Fatalf("ContextWindow = %d, want %d", info.ContextWindow, 8192)
	}
	if info.OutputLimit != 4096 {
		t.Fatalf("OutputLimit = %d, want %d", info.OutputLimit, 4096)
	}
	if !info.Reasoning || !info.Temperature || !info.ToolCall {
		t.Fatalf("expected merged capability flags, got %+v", info)
	}
}
