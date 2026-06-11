package middleware

import (
	"bufio"
	"bytes"
	"net"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// captureWriter wraps gin.ResponseWriter to record the response body into a
// bounded buffer while transparently proxying all streaming/hijack behavior.
// Because it embeds gin.ResponseWriter, Flush/Hijack/CloseNotify/Pusher and the
// gin-specific methods are promoted unchanged, preserving SSE compatibility.
type captureWriter struct {
	gin.ResponseWriter
	buf       *bytes.Buffer
	limit     int
	truncated bool
}

func (w *captureWriter) capture(b []byte) {
	if w.limit <= 0 {
		return
	}
	if w.buf.Len() >= w.limit {
		w.truncated = true
		return
	}
	remaining := w.limit - w.buf.Len()
	if len(b) <= remaining {
		w.buf.Write(b)
	} else {
		w.buf.Write(b[:remaining])
		w.truncated = true
	}
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.capture(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// Hijack is explicitly defined (rather than relying solely on promotion) to make
// the http.Hijacker assertion robust for websocket/realtime relays.
func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// RequestDetailCapture records the sanitized request/response bodies of a relay
// call into the request_details table when the global switch is enabled.
func RequestDetailCapture() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.RequestDetailLogEnabled {
			c.Next()
			return
		}

		limit := common.RequestDetailMaxBytes
		if limit <= 0 {
			limit = 1 << 20
		}
		cw := &captureWriter{
			ResponseWriter: c.Writer,
			buf:            bytes.NewBuffer(make([]byte, 0, 4096)),
			limit:          limit,
		}
		c.Writer = cw

		c.Next()

		requestId := c.GetString(common.RequestIdKey)
		if requestId == "" {
			return
		}

		reqBody := readRequestBodyCopy(c)
		respBody := cw.buf.Bytes()
		if len(reqBody) == 0 && len(respBody) == 0 {
			return
		}
		respCopy := make([]byte, len(respBody))
		copy(respCopy, respBody)
		respTruncated := cw.truncated

		userId := common.GetContextKeyInt(c, constant.ContextKeyUserId)
		channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		tokenId := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
		modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		isStream := common.GetContextKeyBool(c, constant.ContextKeyIsStream)
		endpoint := c.Request.URL.Path
		statusCode := c.Writer.Status()
		createdAt := common.GetTimestamp()

		gopool.Go(func() {
			persistRequestDetail(requestDetailJob{
				requestId:     requestId,
				userId:        userId,
				channelId:     channelId,
				tokenId:       tokenId,
				modelName:     modelName,
				endpoint:      endpoint,
				statusCode:    statusCode,
				isStream:      isStream,
				createdAt:     createdAt,
				reqBody:       reqBody,
				respBody:      respCopy,
				respTruncated: respTruncated,
			})
		})
	}
}

type requestDetailJob struct {
	requestId     string
	userId        int
	channelId     int
	tokenId       int
	modelName     string
	endpoint      string
	statusCode    int
	isStream      bool
	createdAt     int64
	reqBody       []byte
	respBody      []byte
	respTruncated bool
}

func persistRequestDetail(job requestDetailJob) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError("request detail capture panic recovered")
		}
	}()

	reqRes := service.SanitizeBody(job.reqBody, "request", job.requestId, job.createdAt)
	respRes := service.SanitizeBody(job.respBody, "response", job.requestId, job.createdAt)

	detail := &model.RequestDetail{
		RequestId:         job.requestId,
		UserId:            job.userId,
		CreatedAt:         job.createdAt,
		Endpoint:          job.endpoint,
		ModelName:         job.modelName,
		ChannelId:         job.channelId,
		TokenId:           job.tokenId,
		StatusCode:        job.statusCode,
		IsStream:          job.isStream,
		RequestBody:       reqRes.Body,
		ResponseBody:      respRes.Body,
		RequestTruncated:  reqRes.Truncated,
		ResponseTruncated: respRes.Truncated || job.respTruncated,
	}

	mediaList := append(reqRes.Media, respRes.Media...)
	if err := model.SaveRequestDetail(detail, mediaList); err != nil {
		common.SysError("failed to save request detail: " + err.Error())
	}
}

// readRequestBodyCopy returns a private copy of the cached request body, or nil
// if no body storage is present.
func readRequestBodyCopy(c *gin.Context) []byte {
	v, ok := c.Get(common.KeyBodyStorage)
	if !ok || v == nil {
		return nil
	}
	bs, ok := v.(common.BodyStorage)
	if !ok {
		return nil
	}
	b, err := bs.Bytes()
	if err != nil || len(b) == 0 {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}
