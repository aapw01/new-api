package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestSanitizeBody_DataURIExtracted(t *testing.T) {
	payload := strings.Repeat("A", 400)
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64(payload) + `"}}]}]}`

	res := SanitizeBody([]byte(body), "request", "req-1", 100)

	if len(res.Media) != 1 {
		t.Fatalf("expected 1 media, got %d", len(res.Media))
	}
	if strings.Contains(res.Body, b64(payload)) {
		t.Fatalf("base64 payload should be stripped from body: %s", res.Body)
	}
	if !strings.Contains(res.Body, "[media id=") {
		t.Fatalf("placeholder token missing: %s", res.Body)
	}
	if res.Media[0].MimeType != "image/png" {
		t.Fatalf("expected mime image/png, got %s", res.Media[0].MimeType)
	}
}

func TestSanitizeBody_DedupWithinRequest(t *testing.T) {
	payload := strings.Repeat("B", 400)
	uri := "data:image/jpeg;base64," + b64(payload)
	body := `{"a":"` + uri + `","b":"` + uri + `"}`

	res := SanitizeBody([]byte(body), "request", "req-2", 100)
	if len(res.Media) != 1 {
		t.Fatalf("expected dedup to 1 media, got %d", len(res.Media))
	}
}

func TestSanitizeBody_NonJSONStripsDataURI(t *testing.T) {
	payload := strings.Repeat("C", 400)
	text := "data: prefix data:image/gif;base64," + b64(payload) + " suffix"

	res := SanitizeBody([]byte(text), "response", "req-3", 100)
	if len(res.Media) != 1 {
		t.Fatalf("expected 1 media from non-json text, got %d", len(res.Media))
	}
	if strings.Contains(res.Body, b64(payload)) {
		t.Fatalf("payload should be stripped: %s", res.Body)
	}
}

func TestSanitizeBody_PlainTextUntouched(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hello world, this is a normal prompt"}]}`
	res := SanitizeBody([]byte(body), "request", "req-4", 100)
	if len(res.Media) != 0 {
		t.Fatalf("expected no media, got %d", len(res.Media))
	}
	if !strings.Contains(res.Body, "hello world") {
		t.Fatalf("normal text should be preserved: %s", res.Body)
	}
}

func TestSanitizeBody_Truncation(t *testing.T) {
	old := common.RequestDetailMaxBytes
	common.RequestDetailMaxBytes = 50
	defer func() { common.RequestDetailMaxBytes = old }()

	body := `{"text":"` + strings.Repeat("x", 500) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-5", 100)
	if !res.Truncated {
		t.Fatalf("expected truncated flag")
	}
	if !strings.HasSuffix(res.Body, "[truncated]") {
		t.Fatalf("expected truncation marker, got: %s", res.Body)
	}
}

func TestSanitizeBody_UnsafeMimeOmitted(t *testing.T) {
	payload := strings.Repeat("<svg onload=alert(1)>", 30)
	body := `{"url":"data:image/svg+xml;base64,` + b64(payload) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-svg", 100)
	if len(res.Media) != 0 {
		t.Fatalf("svg media must not be stored, got %d", len(res.Media))
	}
	if strings.Contains(res.Body, b64(payload)) {
		t.Fatalf("svg payload should still be stripped from body: %s", res.Body)
	}
	if !strings.Contains(res.Body, "unsupported mime=image/svg+xml") {
		t.Fatalf("expected unsupported-mime marker: %s", res.Body)
	}
}

func TestSanitizeBody_MediaTooLarge(t *testing.T) {
	old := common.RequestDetailMediaMaxBytes
	common.RequestDetailMediaMaxBytes = 10
	defer func() { common.RequestDetailMediaMaxBytes = old }()

	payload := strings.Repeat("D", 400)
	body := `{"url":"data:image/png;base64,` + b64(payload) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-6", 100)
	if len(res.Media) != 0 {
		t.Fatalf("oversized media should not be stored, got %d", len(res.Media))
	}
	if !strings.Contains(res.Body, "too_large") {
		t.Fatalf("expected too_large marker: %s", res.Body)
	}
}
