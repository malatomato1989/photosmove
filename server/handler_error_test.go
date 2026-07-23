package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec ui-localization Req5: error code 契约 — 服务端响应体为 {"code":"E_XXX"},
// 不含任何中英文 message 文案; 带 detail 的原样保留技术信息.
// 现有 cancel_integration_test 覆盖 HTTP 状态码; 这里覆盖响应体 code 契约格式.

func TestWriteErr_CodeOnlyNoMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusUnauthorized, "E_UNAUTHORIZED")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("响应非 JSON: %v (body=%q)", err, rec.Body.String())
	}
	if body["code"] != "E_UNAUTHORIZED" {
		t.Fatalf("code = %q, want E_UNAUTHORIZED", body["code"])
	}
	if _, hasMessage := body["error"]; hasMessage {
		t.Fatalf("响应含语言文案 error 字段 (应只有 code): %v", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func TestWriteErrDetail_PreservesTechnicalDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrDetail(rec, http.StatusInternalServerError, "E_SCAN_FAILED", "open /x: permission denied")

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "E_SCAN_FAILED" {
		t.Fatalf("code = %q", body["code"])
	}
	if body["detail"] != "open /x: permission denied" {
		t.Fatalf("detail = %q (应原样保留技术细节, 不翻译)", body["detail"])
	}
}

// TestWriteErr_AllCodes_NoLinguisticLeak 遍历全部 15 个 code, 确保响应体不含语言文案.
func TestWriteErr_AllCodes_NoLinguisticLeak(t *testing.T) {
	codes := []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, "E_UNAUTHORIZED"},
		{http.StatusMethodNotAllowed, "E_METHOD_NOT_ALLOWED"},
		{http.StatusTooManyRequests, "E_RATE_LIMITED"},
		{http.StatusBadRequest, "E_BAD_REQUEST"},
		{http.StatusForbidden, "E_PIN_INVALID"},
		{http.StatusConflict, "E_DOWNLOAD_IN_PROGRESS"},
		{http.StatusInternalServerError, "E_SCAN_FAILED"},
		{http.StatusNotFound, "E_BATCH_NOT_FOUND"},
		{http.StatusGone, "E_DOWNLOAD_CANCELLED"},
		{http.StatusBadRequest, "E_INVALID_ID"},
		{http.StatusNotFound, "E_NOT_FOUND"},
		{http.StatusNotFound, "E_NO_THUMBNAIL"},
		{http.StatusNotFound, "E_NO_VIDEO_THUMBNAIL"},
		{http.StatusBadRequest, "E_MISSING_BATCH_PARAM"},
		{http.StatusBadRequest, "E_MISSING_BATCH_ID"},
	}
	for _, c := range codes {
		rec := httptest.NewRecorder()
		writeErr(rec, c.status, c.code)
		if rec.Code != c.status {
			t.Fatalf("%s: status = %d, want %d", c.code, rec.Code, c.status)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("%s: 响应非 JSON: %v", c.code, err)
		}
		if body["code"] != c.code {
			t.Fatalf("%s: code 字段 = %q", c.code, body["code"])
		}
		if _, has := body["error"]; has {
			t.Fatalf("%s: 响应含 error 字段 (语言文案泄漏, 违反 code 契约)", c.code)
		}
	}
}

// TestHandlerErrorContract_Endpoints 驱动真实 handler 端点 (而非只测 writeErr helper),
// 断言每个错误路径经 writeErr 路由、响应体为 {"code":"E_XXX"} 且不含语言文案.
// 覆盖 review 第三轮 finding: helper 级测试无法捕获 handler 绕过 writeErr 直接写裸字符串的回归.
func TestHandlerErrorContract_Endpoints(t *testing.T) {
	s := &server{pin: "1234", token: "tok"}

	assertCode := func(name string, h http.HandlerFunc, req *http.Request, wantStatus int, wantCode string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("%s: status=%d want %d (body=%q)", name, rec.Code, wantStatus, rec.Body.String())
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("%s: 响应非 JSON: %v (body=%q)", name, err, rec.Body.String())
		}
		if body["code"] != wantCode {
			t.Fatalf("%s: code=%q want %q", name, body["code"], wantCode)
		}
		if _, has := body["error"]; has {
			t.Fatalf("%s: 响应含语言文案 error 字段 (违反 code 契约): %v", name, body)
		}
	}

	// handleAuth: 错误 PIN → 403 E_PIN_INVALID
	authReq := httptest.NewRequest(http.MethodPost, "/api/auth", strings.NewReader(`{"pin":"0000"}`))
	assertCode("auth wrong pin", s.handleAuth, authReq, http.StatusForbidden, "E_PIN_INVALID")

	// handleCancel: POST {} (无 batch_id) → 400 E_MISSING_BATCH_ID
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/cancel", strings.NewReader(`{}`))
	assertCode("cancel no batch_id", s.handleCancel, cancelReq, http.StatusBadRequest, "E_MISSING_BATCH_ID")

	// handleProgressPoll: 无 token → 401 E_UNAUTHORIZED
	pollNoToken := httptest.NewRequest(http.MethodGet, "/api/progress-poll", nil)
	assertCode("progress-poll no token", s.handleProgressPoll, pollNoToken, http.StatusUnauthorized, "E_UNAUTHORIZED")

	// handleProgressPoll: 有效 token 但无 batch 参数 → 400 E_MISSING_BATCH_PARAM
	pollNoBatch := httptest.NewRequest(http.MethodGet, "/api/progress-poll?token=tok", nil)
	assertCode("progress-poll no batch", s.handleProgressPoll, pollNoBatch, http.StatusBadRequest, "E_MISSING_BATCH_PARAM")
}
