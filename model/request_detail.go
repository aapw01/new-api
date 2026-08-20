package model

import (
	"context"

	"gorm.io/gorm"
)

// RequestDetail stores the full (sanitized) request/response bodies of a relay
// call, linked to a Log row by RequestId. Large base64 media is extracted into
// RequestMedia and replaced by a readable placeholder token inside the bodies.
//
// This table is only written when the global switch is enabled. Bodies are
// stored as TEXT (cross-DB compatible).
type RequestDetail struct {
	Id                int    `json:"id"`
	RequestId         string `json:"request_id" gorm:"type:varchar(64);index:idx_rd_request_id;default:''"`
	UserId            int    `json:"user_id" gorm:"index"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index"`
	Endpoint          string `json:"endpoint" gorm:"type:varchar(191);default:''"`
	ModelName         string `json:"model_name" gorm:"type:varchar(191);default:''"`
	ChannelId         int    `json:"channel_id" gorm:"default:0"`
	TokenId           int    `json:"token_id" gorm:"default:0"`
	StatusCode        int    `json:"status_code" gorm:"default:0"`
	IsStream          bool   `json:"is_stream"`
	RequestBody       string `json:"request_body" gorm:"type:text"`
	ResponseBody      string `json:"response_body" gorm:"type:text"`
	RequestTruncated  bool   `json:"request_truncated"`
	ResponseTruncated bool   `json:"response_truncated"`
}

func (RequestDetail) TableName() string {
	return "request_details"
}

// RequestMedia stores binary media (image/audio) extracted from a request or
// response body. Binary is kept in a separate table so that body/list queries
// never load it. The Data column maps to bytea (PG) / longblob (MySQL) /
// blob (SQLite) automatically via the []byte type — do NOT pin a gorm type tag,
// as that would break cross-DB compatibility.
type RequestMedia struct {
	Id        int    `json:"id"`
	RequestId string `json:"request_id" gorm:"type:varchar(64);index:idx_rm_request_id;default:''"`
	MediaId   string `json:"media_id" gorm:"type:varchar(64);index:idx_rm_media_id;default:''"`
	Sha256    string `json:"sha256" gorm:"type:varchar(64);index:idx_rm_sha256;default:''"`
	MimeType  string `json:"mime_type" gorm:"type:varchar(128);default:''"`
	Size      int64  `json:"size" gorm:"default:0"`
	Role      string `json:"role" gorm:"type:varchar(16);default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index"`
	Data      []byte `json:"-"`
}

func (RequestMedia) TableName() string {
	return "request_media"
}

// SaveRequestDetail persists a request detail row together with its extracted
// media rows. Media records are expected to be already de-duplicated within the
// request by the sanitizer. Failures are non-fatal to the relay request and are
// only logged by the caller.
func SaveRequestDetail(detail *RequestDetail, mediaList []*RequestMedia) error {
	if detail == nil {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(detail).Error; err != nil {
			return err
		}
		if len(mediaList) == 0 {
			return nil
		}
		return tx.CreateInBatches(mediaList, 50).Error
	})
}

// RequestDetailView is the metadata + bodies returned to the admin viewer,
// without the heavy media binary.
type RequestDetailView struct {
	RequestDetail
	Media []RequestMedia `json:"media" gorm:"-"`
}

// GetRequestDetailByRequestId returns the sanitized bodies and media metadata
// (excluding binary data) for a given request id.
func GetRequestDetailByRequestId(requestId string) (*RequestDetailView, error) {
	if requestId == "" {
		return nil, nil
	}
	var detail RequestDetail
	err := DB.Where("request_id = ?", requestId).First(&detail).Error
	if err != nil {
		return nil, err
	}
	var media []RequestMedia
	// Explicitly omit the binary Data column from the metadata query.
	if err := DB.Model(&RequestMedia{}).
		Omit("data").
		Where("request_id = ?", requestId).
		Find(&media).Error; err != nil {
		return nil, err
	}
	return &RequestDetailView{RequestDetail: detail, Media: media}, nil
}

// GetRequestMediaData returns a single media record (including binary data) by
// media id, scoped to a request id to avoid cross-request leakage.
func GetRequestMediaData(requestId string, mediaId string) (*RequestMedia, error) {
	if mediaId == "" || requestId == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var media RequestMedia
	if err := DB.
		Where("media_id = ?", mediaId).
		Where("request_id = ?", requestId).
		First(&media).Error; err != nil {
		return nil, err
	}
	return &media, nil
}

// DeleteOldRequestDetailBatch removes one batch of request-detail rows and one
// batch of their media using the same cutoff as the log-cleanup system task.
func DeleteOldRequestDetailBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if targetTimestamp <= 0 {
		return 0, nil
	}
	if limit <= 0 {
		limit = 100
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	mediaResult := DB.WithContext(ctx).
		Where("created_at < ?", targetTimestamp).
		Limit(limit).
		Delete(&RequestMedia{})
	if mediaResult.Error != nil {
		return 0, mediaResult.Error
	}
	detailResult := DB.WithContext(ctx).
		Where("created_at < ?", targetTimestamp).
		Limit(limit).
		Delete(&RequestDetail{})
	if detailResult.Error != nil {
		return mediaResult.RowsAffected, detailResult.Error
	}
	return mediaResult.RowsAffected + detailResult.RowsAffected, nil
}
