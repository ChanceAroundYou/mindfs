package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 上传体超过上限时必须回 413 且说清原因 —— 早前是 400 "invalid multipart
// form"，用户看着像格式错，实际是文件太大。
func TestHandleUploadRejectsOversizedBodyWith413(t *testing.T) {
	original := maxUploadFileRequestBytes
	maxUploadFileRequestBytes = 1024
	defer func() { maxUploadFileRequestBytes = original }()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files", "big.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("a"), 4096)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/upload?root=missing-root", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	(&HTTPHandler{}).handleUpload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}
