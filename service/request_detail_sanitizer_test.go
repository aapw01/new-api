package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestSanitizeBody_DataURIExtracted(t *testing.T) {
	payload := strings.Repeat("A", 400)
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64(payload) + `"}}]}]}`

	res := SanitizeBody([]byte(body), "request", "req-1", 100)

	require.Len(t, res.Media, 1)
	assert.NotContains(t, res.Body, b64(payload))
	assert.Contains(t, res.Body, "[media id=")
	assert.Equal(t, "image/png", res.Media[0].MimeType)
}

func TestSanitizeBody_DedupWithinRequest(t *testing.T) {
	payload := strings.Repeat("B", 400)
	uri := "data:image/jpeg;base64," + b64(payload)
	body := `{"a":"` + uri + `","b":"` + uri + `"}`

	res := SanitizeBody([]byte(body), "request", "req-2", 100)
	require.Len(t, res.Media, 1)
}

func TestSanitizeBody_NonJSONStripsDataURI(t *testing.T) {
	payload := strings.Repeat("C", 400)
	text := "data: prefix data:image/gif;base64," + b64(payload) + " suffix"

	res := SanitizeBody([]byte(text), "response", "req-3", 100)
	require.Len(t, res.Media, 1)
	assert.NotContains(t, res.Body, b64(payload))
}

func TestSanitizeBody_PlainTextUntouched(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hello world, this is a normal prompt"}]}`
	res := SanitizeBody([]byte(body), "request", "req-4", 100)
	assert.Empty(t, res.Media)
	assert.Contains(t, res.Body, "hello world")
}

func TestSanitizeBody_Truncation(t *testing.T) {
	old := common.RequestDetailMaxBytes
	common.RequestDetailMaxBytes = 50
	defer func() { common.RequestDetailMaxBytes = old }()

	body := `{"text":"` + strings.Repeat("x", 500) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-5", 100)
	assert.True(t, res.Truncated)
	assert.True(t, strings.HasSuffix(res.Body, "[truncated]"))
}

func TestSanitizeBody_UnsafeMimeOmitted(t *testing.T) {
	payload := strings.Repeat("<svg onload=alert(1)>", 30)
	body := `{"url":"data:image/svg+xml;base64,` + b64(payload) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-svg", 100)
	assert.Empty(t, res.Media)
	assert.NotContains(t, res.Body, b64(payload))
	assert.Contains(t, res.Body, "unsupported mime=image/svg+xml")
}

func TestSanitizeBody_MediaTooLarge(t *testing.T) {
	old := common.RequestDetailMediaMaxBytes
	common.RequestDetailMediaMaxBytes = 10
	defer func() { common.RequestDetailMediaMaxBytes = old }()

	payload := strings.Repeat("D", 400)
	body := `{"url":"data:image/png;base64,` + b64(payload) + `"}`
	res := SanitizeBody([]byte(body), "request", "req-6", 100)
	assert.Empty(t, res.Media)
	assert.Contains(t, res.Body, "too_large")
}
