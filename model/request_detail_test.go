package model

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRequestDetailPersistsInMainDatabaseAndCleansUpWithLogs(t *testing.T) {
	truncateTables(t)

	separateLogDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	previousLogDB := LOG_DB
	LOG_DB = separateLogDB
	t.Cleanup(func() { LOG_DB = previousLogDB })

	oldDetail := &RequestDetail{
		RequestId:    "req-old",
		CreatedAt:    100,
		RequestBody:  `{"prompt":"old"}`,
		ResponseBody: `{"answer":"old"}`,
	}
	oldMedia := &RequestMedia{
		RequestId: "req-old",
		MediaId:   "media-old",
		MimeType:  "image/png",
		Size:      3,
		Role:      "request",
		CreatedAt: 100,
		Data:      []byte{1, 2, 3},
	}
	require.NoError(t, SaveRequestDetail(oldDetail, []*RequestMedia{oldMedia}))
	require.NoError(t, SaveRequestDetail(&RequestDetail{
		RequestId:    "req-new",
		CreatedAt:    300,
		RequestBody:  `{"prompt":"new"}`,
		ResponseBody: `{"answer":"new"}`,
	}, nil))

	view, err := GetRequestDetailByRequestId("req-old")
	require.NoError(t, err)
	require.Len(t, view.Media, 1)
	assert.Equal(t, oldDetail.RequestBody, view.RequestBody)
	assert.Nil(t, view.Media[0].Data)

	media, err := GetRequestMediaData("req-old", "media-old")
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, media.Data)

	deleted, err := DeleteOldRequestDetailBatch(context.Background(), 200, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	_, err = GetRequestDetailByRequestId("req-old")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetRequestMediaData("req-old", "media-old")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	newView, err := GetRequestDetailByRequestId("req-new")
	require.NoError(t, err)
	assert.Equal(t, "req-new", newView.RequestId)
}
