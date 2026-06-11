package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// dataURIRegex matches RFC 2397 base64 data URIs, e.g.
// data:image/png;base64,iVBORw0KG..., capturing the mime type and payload.
var dataURIRegex = regexp.MustCompile(`data:([a-zA-Z0-9.+/-]+)?(?:;[^,;]+)*;base64,([A-Za-z0-9+/=]+)`)

// minInlineBase64Len is the threshold above which a bare base64 string found in
// a known media field (e.g. {"data": "..."}) is externalized. Short strings are
// left in place to avoid false positives on ordinary text.
const minInlineBase64Len = 256

// knownMediaDataKeys are object keys whose string value commonly carries raw
// (non-data-URI) base64 media in provider payloads (Anthropic source.data,
// Gemini inlineData.data, OpenAI input_audio.data).
var knownMediaDataKeys = map[string]bool{
	"data": true,
}

// SanitizeResult holds a cleaned body plus any externalized media.
type SanitizeResult struct {
	Body      string
	Truncated bool
	Media     []*model.RequestMedia
}

// mediaCollector accumulates media within a single request, de-duplicating by
// content hash so the same image/audio repeated across a multi-turn message
// array is stored only once.
type mediaCollector struct {
	requestId     string
	role          string
	createdAt     int64
	mediaMaxBytes int
	seen          map[string]string // sha256 -> mediaId
	media         []*model.RequestMedia
}

func newMediaCollector(requestId, role string, createdAt int64, mediaMaxBytes int) *mediaCollector {
	return &mediaCollector{
		requestId:     requestId,
		role:          role,
		createdAt:     createdAt,
		mediaMaxBytes: mediaMaxBytes,
		seen:          make(map[string]string),
	}
}

// isUnsafeInlineMime reports whether a declared media MIME type could be
// rendered by a browser as active content (HTML/SVG/script). Such payloads are
// never stored as binary blobs: they are replaced by a placeholder instead, so
// that the admin viewer can never be tricked into fetching and rendering a
// stored XSS vector. Opaque binary (image/audio/video/octet-stream) is fine.
func isUnsafeInlineMime(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.Contains(m, "html"):
		return true
	case strings.Contains(m, "svg"):
		return true
	case strings.Contains(m, "javascript"), strings.Contains(m, "ecmascript"):
		return true
	case strings.Contains(m, "xml"):
		return true
	}
	return false
}

// add stores a decoded media blob (subject to dedup + size cap) and returns a
// human-readable placeholder token to embed in the cleaned body.
func (mc *mediaCollector) add(mime string, payload []byte) string {
	if mime == "" {
		mime = "application/octet-stream"
	}
	if isUnsafeInlineMime(mime) {
		return fmt.Sprintf("[media omitted: unsupported mime=%s size=%d]", mime, len(payload))
	}
	sum := sha256.Sum256(payload)
	hashHex := hex.EncodeToString(sum[:])
	mediaId := hashHex[:32]
	size := len(payload)

	if mc.mediaMaxBytes > 0 && size > mc.mediaMaxBytes {
		return fmt.Sprintf("[media omitted: too_large mime=%s size=%d]", mime, size)
	}
	if _, ok := mc.seen[hashHex]; !ok {
		mc.seen[hashHex] = mediaId
		mc.media = append(mc.media, &model.RequestMedia{
			RequestId: mc.requestId,
			MediaId:   mediaId,
			Sha256:    hashHex,
			MimeType:  mime,
			Size:      int64(size),
			Role:      mc.role,
			CreatedAt: mc.createdAt,
			Data:      payload,
		})
	}
	return fmt.Sprintf("[media id=%s mime=%s size=%d]", mediaId, mime, size)
}

// SanitizeBody extracts inline base64 media from a request/response body and
// returns a cleaned, placeholder-substituted body plus the media records. role
// is "request" or "response". If the body is not valid JSON, a regex-based pass
// over the raw text is used instead.
func SanitizeBody(raw []byte, role string, requestId string, createdAt int64) SanitizeResult {
	if len(raw) == 0 {
		return SanitizeResult{}
	}
	mediaMaxBytes := common.RequestDetailMediaMaxBytes
	maxBytes := common.RequestDetailMaxBytes
	mc := newMediaCollector(requestId, role, createdAt, mediaMaxBytes)

	var cleaned string
	var node interface{}
	if err := common.Unmarshal(raw, &node); err == nil {
		walked := sanitizeNode(node, mc)
		if out, mErr := common.Marshal(walked); mErr == nil {
			cleaned = string(out)
		} else {
			cleaned = string(raw)
		}
	} else {
		// Not JSON (e.g. SSE/text). Strip data URIs via regex.
		cleaned = stripDataURIs(string(raw), mc)
	}

	truncated := false
	if maxBytes > 0 && len(cleaned) > maxBytes {
		cleaned = cleaned[:maxBytes] + "\n...[truncated]"
		truncated = true
	}
	return SanitizeResult{Body: cleaned, Truncated: truncated, Media: mc.media}
}

// sanitizeNode recursively walks a decoded JSON tree, replacing inline base64
// media strings with placeholder tokens.
func sanitizeNode(node interface{}, mc *mediaCollector) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		for key, val := range v {
			if s, ok := val.(string); ok {
				v[key] = sanitizeString(key, s, mc)
			} else {
				v[key] = sanitizeNode(val, mc)
			}
		}
		return v
	case []interface{}:
		for i, val := range v {
			if s, ok := val.(string); ok {
				v[i] = sanitizeString("", s, mc)
			} else {
				v[i] = sanitizeNode(val, mc)
			}
		}
		return v
	default:
		return node
	}
}

// sanitizeString inspects a single string value. It externalizes data URIs
// always, and bare base64 only when found in a known media key.
func sanitizeString(key string, s string, mc *mediaCollector) string {
	if strings.Contains(s, ";base64,") && strings.HasPrefix(strings.TrimSpace(s), "data:") {
		return stripDataURIs(s, mc)
	}
	if knownMediaDataKeys[key] && len(s) >= minInlineBase64Len && looksLikeBase64(s) {
		if payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s)); err == nil {
			return mc.add("application/octet-stream", payload)
		}
	}
	return s
}

// stripDataURIs replaces every base64 data URI occurrence in text with a
// placeholder token, collecting the decoded media.
func stripDataURIs(text string, mc *mediaCollector) string {
	return dataURIRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := dataURIRegex.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		mime := sub[1]
		b64 := strings.Join(strings.Fields(sub[2]), "")
		payload, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return match
		}
		return mc.add(mime, payload)
	})
}

// looksLikeBase64 performs a cheap heuristic check to avoid decoding obvious
// non-base64 strings.
func looksLikeBase64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '+' || r == '/' || r == '=' {
			continue
		}
		return false
	}
	return true
}
