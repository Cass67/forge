package copilot

import (
	"net/http"
	"testing"
)

func TestExtractQuotaJSON_QuotaSnapshots(t *testing.T) {
	raw := `{
		"type":"session.usage_info",
		"quotaSnapshots":{
			"premium_interactions":{
				"includedRequests":300,
				"usedRequests":42,
				"remainingPercentage":86,
				"overageAllowedWithExhaustedQuota":true,
				"resetDate":"2026-03-31T00:00:00Z"
			}
		}
	}`
	q := ExtractQuotaJSON(raw)
	if q == nil {
		t.Fatal("expected quota, got nil")
	}
	if q.Type != "premium_interactions" {
		t.Fatalf("type = %q, want premium_interactions", q.Type)
	}
	if q.Included != 300 || q.Used != 42 || q.Remaining != 258 {
		t.Fatalf("unexpected counts: %+v", *q)
	}
	if q.PercentRemaining != 86 {
		t.Fatalf("percent = %v, want 86", q.PercentRemaining)
	}
	if !q.Unlimited {
		t.Fatalf("unlimited = false, want true")
	}
	if q.ResetAt != "2026-03-31T00:00:00Z" {
		t.Fatalf("reset_at = %q", q.ResetAt)
	}
}

func TestExtractQuotaJSON_FlatObject(t *testing.T) {
	raw := `{
		"remaining": 12,
		"included": 100,
		"used": 88,
		"remainingPercentage": 12,
		"resetDate": "2026-03-31T00:00:00Z"
	}`
	q := ExtractQuotaJSON(raw)
	if q == nil {
		t.Fatal("expected quota, got nil")
	}
	if q.Type != "premium_interactions" {
		t.Fatalf("type = %q, want default premium_interactions", q.Type)
	}
	if q.Remaining != 12 || q.Included != 100 || q.Used != 88 {
		t.Fatalf("unexpected counts: %+v", *q)
	}
}

func TestExtractQuotaHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-RateLimit-Remaining-Premium-Interactions", "120")
	h.Set("X-RateLimit-Limit-Premium-Interactions", "300")
	h.Set("X-RateLimit-Used-Premium-Interactions", "180")
	h.Set("X-RateLimit-Remaining-Percentage-Premium-Interactions", "40")
	h.Set("X-RateLimit-Reset-Premium-Interactions", "2026-03-31T00:00:00Z")

	q := ExtractQuotaHeaders(h)
	if q == nil {
		t.Fatal("expected quota, got nil")
	}
	if q.Type != "premium-interactions" {
		t.Fatalf("type = %q, want premium-interactions", q.Type)
	}
	if q.Remaining != 120 || q.Included != 300 || q.Used != 180 {
		t.Fatalf("unexpected counts: %+v", *q)
	}
	if q.PercentRemaining != 40 {
		t.Fatalf("percent = %v, want 40", q.PercentRemaining)
	}
}
