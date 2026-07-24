package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec ui-localization Req5: error code contract — server response body is
// {"code":"E_XXX"}, with no Chinese/English message text; responses carrying
// detail keep the technical info verbatim.
// The existing cancel_integration_test covers HTTP status codes; here we cover
// the response body code contract format.

func TestWriteErr_CodeOnlyNoMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusUnauthorized, "E_UNAUTHORIZED")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rec.Body.String())
	}
	if body["code"] != "E_UNAUTHORIZED" {
		t.Fatalf("code = %q, want E_UNAUTHORIZED", body["code"])
	}
	if _, hasMessage := body["error"]; hasMessage {
		t.Fatalf("response contains language text error field (should only have code): %v", body)
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
		t.Fatalf("detail = %q (technical detail must be preserved verbatim, untranslated)", body["detail"])
	}
}

// TestWriteErr_AllCodes_NoLinguisticLeak iterates all 15 codes, ensuring the
// response body contains no language text.
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
			t.Fatalf("%s: response is not JSON: %v", c.code, err)
		}
		if body["code"] != c.code {
			t.Fatalf("%s: code field = %q", c.code, body["code"])
		}
		if _, has := body["error"]; has {
			t.Fatalf("%s: response contains error field (language text leak, violates code contract)", c.code)
		}
	}
}

// TestHandlerErrorContract_Endpoints drives real handler endpoints (rather than
// only testing the writeErr helper), asserting each error path is routed through
// writeErr, the response body is {"code":"E_XXX"}, and contains no language text.
// Covers review round 3 finding: helper-level tests cannot catch regressions
// where a handler bypasses writeErr and writes a bare string directly.
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
			t.Fatalf("%s: response is not JSON: %v (body=%q)", name, err, rec.Body.String())
		}
		if body["code"] != wantCode {
			t.Fatalf("%s: code=%q want %q", name, body["code"], wantCode)
		}
		if _, has := body["error"]; has {
			t.Fatalf("%s: response contains language text error field (violates code contract): %v", name, body)
		}
	}

	// handleAuth: wrong PIN → 403 E_PIN_INVALID
	authReq := httptest.NewRequest(http.MethodPost, "/api/auth", strings.NewReader(`{"pin":"0000"}`))
	assertCode("auth wrong pin", s.handleAuth, authReq, http.StatusForbidden, "E_PIN_INVALID")

	// handleCancel: POST {} (no batch_id) → 400 E_MISSING_BATCH_ID
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/cancel", strings.NewReader(`{}`))
	assertCode("cancel no batch_id", s.handleCancel, cancelReq, http.StatusBadRequest, "E_MISSING_BATCH_ID")

	// handleProgressPoll: no token → 401 E_UNAUTHORIZED
	pollNoToken := httptest.NewRequest(http.MethodGet, "/api/progress-poll", nil)
	assertCode("progress-poll no token", s.handleProgressPoll, pollNoToken, http.StatusUnauthorized, "E_UNAUTHORIZED")

	// handleProgressPoll: valid token but no batch param → 400 E_MISSING_BATCH_PARAM
	pollNoBatch := httptest.NewRequest(http.MethodGet, "/api/progress-poll?token=tok", nil)
	assertCode("progress-poll no batch", s.handleProgressPoll, pollNoBatch, http.StatusBadRequest, "E_MISSING_BATCH_PARAM")
}
