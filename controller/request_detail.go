package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetRequestDetail returns the full sanitized request/response bodies and media
// metadata for a given request id. Admin-only (enforced by router middleware).
//
// Reads are intentionally NOT gated by RequestDetailLogEnabled: the toggle only
// controls capture (writes). Historical logs recorded while the feature was on
// must remain viewable after it is turned off.
func GetRequestDetail(c *gin.Context) {
	requestId := c.Query("request_id")
	if requestId == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "request_id is required",
		})
		return
	}
	view, err := model.GetRequestDetailByRequestId(requestId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "request detail not found",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    view,
	})
}

// GetRequestMedia streams a single externalized media blob (image/audio) by
// media id, scoped to the owning request id. Admin-only.
//
// Reads are not gated by RequestDetailLogEnabled (see GetRequestDetail). The
// blob is always served with a forced attachment disposition and nosniff so
// that, even if a malicious payload slipped past the capture-side allowlist,
// the browser will never render it inline as active content.
func GetRequestMedia(c *gin.Context) {
	mediaId := c.Query("media_id")
	requestId := c.Query("request_id")
	if mediaId == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "media_id is required",
		})
		return
	}
	// request_id is mandatory: media is always scoped to its owning request to
	// prevent enumeration / cross-request leakage by media_id alone.
	if requestId == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "request_id is required",
		})
		return
	}
	media, err := model.GetRequestMediaData(requestId, mediaId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "media not found",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	mime := media.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "attachment; filename=\""+media.MediaId+"\"")
	c.Data(http.StatusOK, mime, media.Data)
}
