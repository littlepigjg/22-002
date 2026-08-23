package firmware_upgrade_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"firmware-upgrade/internal/dto"
	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
)

type RespBody struct {
	Success   bool        `json:"success"`
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

func TestRedGreen(t *testing.T) {
	pass := true
	reason := ""

	t.Run("Scenario1_RequireNotNil_Nil_MessageNotEmpty", func(t *testing.T) {
		err := dto.RequireNotNil(nil)
		if err == nil {
			t.Fatalf("RequireNotNil(nil) should return non-nil error")
		}
		w := httptest.NewRecorder()
		handler.WriteError(w, err)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected HTTP 500, got %d", resp.StatusCode)
			pass = false
		}
		var body RespBody
		if errJ := json.NewDecoder(resp.Body).Decode(&body); errJ != nil {
			t.Fatalf("decode json failed: %v", errJ)
		}
		if body.Code != int(response.CodeInternal) {
			t.Errorf("expected code %d, got %d", int(response.CodeInternal), body.Code)
			pass = false
		}
		if body.Message == "" {
			t.Errorf("message is empty (BUG); expected non-empty message like 'unexpected nil error'")
			pass = false
			reason = fmt.Sprintf("Scenario1 failed: HTTP 500 message is empty but should show cause description. status=%d code=%d body=%+v", resp.StatusCode, body.Code, body)
		} else {
			t.Logf("Scenario1 OK: message=%q", body.Message)
		}
	})

	t.Run("Scenario2_NormalizeBizError_ModelErr_MessageNotEmpty", func(t *testing.T) {
		cases := []struct {
			name     string
			err      error
			httpWant int
			msgWant  string
		}{
			{"NotFound", model.ErrNotFound, http.StatusNotFound, "resource not found"},
			{"DeviceNotFound", model.ErrDeviceNotFound, http.StatusNotFound, "device not found"},
			{"TaskNotFound", model.ErrTaskNotFound, http.StatusNotFound, "task not found"},
			{"ModelNotFound", model.ErrModelNotFound, http.StatusNotFound, "model not found"},
			{"FirmwareNotFound", model.ErrFirmwareNotFound, http.StatusNotFound, "firmware not found"},
			{"Conflict", model.ErrConflict, http.StatusConflict, "resource conflict"},
			{"InvalidParam", model.ErrInvalidParam, http.StatusBadRequest, "invalid param"},
			{"Unauthorized", model.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
			{"Forbidden", model.ErrForbidden, http.StatusForbidden, "forbidden"},
			{"FirmwareNotPublished", model.ErrFirmwareNotPublished, http.StatusBadRequest, "firmware not published"},
			{"AlreadyRegistered", model.ErrAlreadyRegistered, http.StatusConflict, "device already registered"},
			{"UploadTooLarge", model.ErrUploadTooLarge, http.StatusRequestEntityTooLarge, "upload file too large"},
			{"UploadFileEmpty", model.ErrUploadFileEmpty, http.StatusBadRequest, "upload file empty"},
			{"TaskState", model.ErrTaskState, http.StatusBadRequest, "task state transition illegal"},
			{"StrategyInvalid", model.ErrStrategyInvalid, http.StatusBadRequest, "strategy invalid"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				wrapped := dto.NormalizeBizError(tc.err)
				if wrapped == nil {
					t.Fatalf("NormalizeBizError returned nil")
				}
				w := httptest.NewRecorder()
				handler.WriteError(w, tc.err)
				resp := w.Result()
				defer resp.Body.Close()
				var body RespBody
				if errJ := json.NewDecoder(resp.Body).Decode(&body); errJ != nil {
					t.Fatalf("decode json failed: %v", errJ)
				}
				if resp.StatusCode != tc.httpWant {
					t.Errorf("%s: HTTP status want %d got %d", tc.name, tc.httpWant, resp.StatusCode)
					pass = false
				}
				if body.Message == "" {
					t.Errorf("%s: message is empty (BUG); want %q", tc.name, tc.msgWant)
					pass = false
					reason = fmt.Sprintf("Scenario2.%s failed: message empty. status=%d code=%d body=%+v", tc.name, resp.StatusCode, body.Code, body)
				} else {
					t.Logf("Scenario2.%s OK: status=%d message=%q", tc.name, resp.StatusCode, body.Message)
				}
			})
		}
	})

	t.Run("Scenario3_RandomError_500_MessageNotEmpty", func(t *testing.T) {
		randomErr := errors.New("disk i/o failure: sector 0xdeadbeef unreadable")
		w := httptest.NewRecorder()
		handler.WriteError(w, randomErr)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected HTTP 500, got %d", resp.StatusCode)
			pass = false
		}
		var body RespBody
		if errJ := json.NewDecoder(resp.Body).Decode(&body); errJ != nil {
			t.Fatalf("decode json failed: %v", errJ)
		}
		if body.Code != int(response.CodeInternal) {
			t.Errorf("expected code %d, got %d", int(response.CodeInternal), body.Code)
			pass = false
		}
		if body.Message == "" {
			t.Errorf("message is empty (BUG); expected to contain 'disk i/o failure'")
			pass = false
			reason = fmt.Sprintf("Scenario3 failed: HTTP 500 message is empty. status=%d code=%d body=%+v", resp.StatusCode, body.Code, body)
		} else {
			t.Logf("Scenario3 OK: message=%q", body.Message)
		}
	})

	t.Run("Scenario4_ServiceCreate_InvalidParam_MessageNotEmpty", func(t *testing.T) {
		cfg := &model.CreateTaskRequest{Name: ""}
		err := validateCreate(cfg)
		if err == nil {
			t.Fatalf("validation should return error")
		}
		w := httptest.NewRecorder()
		handler.WriteError(w, err)
		resp := w.Result()
		defer resp.Body.Close()
		var body RespBody
		if errJ := json.NewDecoder(resp.Body).Decode(&body); errJ != nil {
			t.Fatalf("decode json failed: %v", errJ)
		}
		if body.Message == "" {
			t.Errorf("validation error message is empty (BUG)")
			pass = false
			reason = fmt.Sprintf("Scenario4 failed: message empty. body=%+v", body)
		} else {
			t.Logf("Scenario4 OK: message=%q", body.Message)
		}
	})

	if pass {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		if reason != "" {
			fmt.Println("reason:", reason)
		}
	}
}

func validateCreate(req *model.CreateTaskRequest) error {
	if req == nil || req.Name == "" || req.ModelID == "" || req.TargetVersion == "" {
		return dto.NormalizeBizError(model.ErrInvalidParam)
	}
	if req.Strategy == model.StrategyDeviceList && len(req.DeviceIDs) == 0 {
		return dto.NormalizeBizError(model.ErrStrategyInvalid)
	}
	return nil
}
