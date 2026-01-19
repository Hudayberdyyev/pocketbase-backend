package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/models"
)

const (
	defaultDiditBaseURL = "https://verification.didit.me"
	diditWebhookPath    = "/didit/webhook"
)

type diditConfig struct {
	APIKey          string
	WorkflowID      string
	WebhookSecret   string
	BaseURL         string
	CallbackBaseURL string
}

type DiditClient struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
}

type DiditCreateSessionRequest struct {
	WorkflowID string `json:"workflow_id"`
	VendorData string `json:"vendor_data"`
	Callback   string `json:"callback"`
}

type DiditCreateSessionResponse struct {
	SessionID       string `json:"session_id"`
	VerificationURL string `json:"url"`
	// TODO: add missing response fields once Didit API schema is confirmed
}

type DiditErrorResponse struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	// TODO: add missing error fields once Didit API schema is confirmed
}

type DiditWebhookPayloadDecision struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type DiditWebhookPayload struct {
	SessionID   string                      `json:"session_id"`
	Status      string                      `json:"status"`
	WebhookType string                      `json:"webhook_type"`
	Timestamp   int64                       `json:"timestamp"`
	Decision    DiditWebhookPayloadDecision `json:"decision"`
	Reason      string                      `json:"reason"`
}

func loadDiditConfig(app *pocketbase.PocketBase) (diditConfig, error) {
	cfg := diditConfig{
		APIKey:          strings.TrimSpace(os.Getenv("DIDIT_API_KEY")),
		WorkflowID:      strings.TrimSpace(os.Getenv("DIDIT_WORKFLOW_ID")),
		WebhookSecret:   strings.TrimSpace(os.Getenv("DIDIT_WEBHOOK_SECRET")),
		BaseURL:         strings.TrimSpace(os.Getenv("DIDIT_API_BASE_URL")),
		CallbackBaseURL: strings.TrimSpace(os.Getenv("DIDIT_CALLBACK_BASE_URL")),
	}

	if cfg.CallbackBaseURL == "" {
		cfg.CallbackBaseURL = strings.TrimSpace(app.Settings().Meta.AppUrl)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultDiditBaseURL
	}

	if cfg.APIKey == "" || cfg.WorkflowID == "" || cfg.WebhookSecret == "" {
		return diditConfig{}, errors.New("DIDIT_API_KEY, DIDIT_WORKFLOW_ID, DIDIT_WEBHOOK_SECRET are required")
	}
	if cfg.CallbackBaseURL == "" {
		return diditConfig{}, errors.New("DIDIT_CALLBACK_BASE_URL or App URL in settings is required")
	}

	return cfg, nil
}

func NewDiditClient(cfg diditConfig) *DiditClient {
	return &DiditClient{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		BaseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		APIKey:     cfg.APIKey,
	}
}

func (c *DiditClient) CreateVerificationSession(ctx context.Context, req DiditCreateSessionRequest) (DiditCreateSessionResponse, error) {
	endpoint := c.BaseURL + "/v2/session/"

	body, err := json.Marshal(req)
	if err != nil {
		return DiditCreateSessionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return DiditCreateSessionResponse{}, err
	}
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return DiditCreateSessionResponse{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return DiditCreateSessionResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr DiditErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		return DiditCreateSessionResponse{}, fmt.Errorf("didit api error: status=%d message=%s body=%s", resp.StatusCode, apiErr.Message, strings.TrimSpace(string(respBody)))
	}

	var result DiditCreateSessionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return DiditCreateSessionResponse{}, err
	}
	log.Printf("didit create session response: %+v", result)

	if result.SessionID == "" || result.VerificationURL == "" {
		return DiditCreateSessionResponse{}, errors.New("didit response missing session_id or verification_url")
	}

	return result, nil
}

func diditStartVerificationHandler(app *pocketbase.PocketBase, client *DiditClient, cfg diditConfig) func(c echo.Context) error {
	return func(c echo.Context) error {
		record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
		if !ok || record == nil {
			return apis.NewUnauthorizedError("unauthorized", nil)
		}

		callbackURL := strings.TrimRight(cfg.CallbackBaseURL, "/") + diditWebhookPath

		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()

		session, err := client.CreateVerificationSession(ctx, DiditCreateSessionRequest{
			WorkflowID: cfg.WorkflowID,
			VendorData: record.Id,
			Callback:   callbackURL,
		})
		if err != nil {
			return apis.NewApiError(http.StatusBadGateway, "failed to create didit verification session", err)
		}

		record.Set("didit_session_id", session.SessionID)
		record.Set("verification_status", "pending")
		record.Set("verification_reason", "")

		if err := app.Dao().SaveRecord(record); err != nil {
			return apis.NewApiError(http.StatusInternalServerError, "failed to save didit verification status", err)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"verification_url": session.VerificationURL,
			"session_id":       session.SessionID,
		})
	}
}

func diditWebhookHandler(app *pocketbase.PocketBase, cfg diditConfig) func(c echo.Context) error {
	return func(c echo.Context) error {
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return apis.NewApiError(http.StatusBadRequest, "invalid payload", err)
		}

		timestampHeader := c.Request().Header.Get("X-Timestamp")
		timestamp, err := parseDiditTimestamp(timestampHeader)
		if err != nil {
			return apis.NewApiError(http.StatusUnauthorized, "invalid timestamp header", err)
		}
		if !isTimestampValid(timestamp, time.Now().Unix(), 300) {
			return apis.NewApiError(http.StatusUnauthorized, "session expired", nil)
		}

		var verifiedBy string
		signatureV2 := c.Request().Header.Get("X-Signature-V2")

		ok, err := verifyDiditSignatureV2(cfg.WebhookSecret, signatureV2, body)
		if err == nil && ok {
			verifiedBy = "v2"
		}
		if verifiedBy == "" {
			return apis.NewApiError(http.StatusUnauthorized, "invalid signature", nil)
		}

		var payload DiditWebhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return apis.NewApiError(http.StatusUnauthorized, "invalid payload", err)
		}

		if payload.SessionID == "" || payload.Status == "" || payload.WebhookType == "" {
			log.Printf("didit webhook processed session=%s type=%s status=%s verified_by=%s", payload.SessionID, payload.WebhookType, payload.Status, verifiedBy)
			return c.JSON(http.StatusOK, map[string]string{"message": "Webhook processed"})
		}

		user, err := app.Dao().FindFirstRecordByData("users", "didit_session_id", payload.SessionID)
		if err != nil {
			log.Printf("didit webhook processed session=%s type=%s status=%s verified_by=%s", payload.SessionID, payload.WebhookType, payload.Status, verifiedBy)
			return c.JSON(http.StatusOK, map[string]string{"message": "Webhook processed"})
		}

		currentStatus := user.GetString("verification_status")
		currentReason := user.GetString("verification_reason")
		currentSessionID := user.GetString("didit_session_id")

		status := strings.ToLower(payload.Status)
		if currentStatus == status && currentReason == payload.Reason && currentSessionID == payload.SessionID {
			log.Printf("didit webhook processed session=%s type=%s status=%s verified_by=%s", payload.SessionID, payload.WebhookType, payload.Status, verifiedBy)
			return c.JSON(http.StatusOK, map[string]string{"message": "Webhook processed"})
		}
		user.Set("verification_status", status)
		if payload.Reason != "" {
			user.Set("verification_reason", payload.Reason)
		}

		if err := app.Dao().SaveRecord(user); err != nil {
			log.Printf("didit webhook processed session=%s type=%s status=%s verified_by=%s", payload.SessionID, payload.WebhookType, payload.Status, verifiedBy)
			return c.JSON(http.StatusOK, map[string]string{"message": "Webhook processed"})
		}

		log.Printf("didit webhook processed session=%s type=%s status=%s verified_by=%s", payload.SessionID, payload.WebhookType, payload.Status, verifiedBy)

		return c.JSON(http.StatusOK, map[string]string{"message": "Webhook processed"})
	}
}

func parseDiditTimestamp(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("missing timestamp")
	}
	return strconv.ParseInt(value, 10, 64)
}

func isTimestampValid(ts int64, now int64, maxSkew int64) bool {
	diff := now - ts
	return diff <= maxSkew && diff >= -maxSkew
}

func verifyDiditSignatureV2(secret string, signature string, body []byte) (bool, error) {
	// 1. Calculate the expected signature (HMAC-SHA256)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	calculatedSignature := hex.EncodeToString(mac.Sum(nil))

	// 2. Compare signatures securely (constant time)
	// The signature from header is usually hex-encoded string, so compare strings
	// For timing safety, compare the byte slices of the hex-encoded strings
	if subtle.ConstantTimeCompare([]byte(calculatedSignature), []byte(signature)) != 1 {
		return false, errors.New("invalid signature")
	}

	return true, nil
}
