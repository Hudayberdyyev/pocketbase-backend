package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "pocketbase-backend/migrations"

	stream "github.com/GetStream/stream-chat-go/v5"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/checkout/session"
)

func main() {
	_ = godotenv.Load()
	app := pocketbase.New()
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{})

	streamClient := mustStreamClient()
	stripeCfg := mustStripeConfig()
	stripe.Key = stripeCfg.SecretKey

	app.OnRecordAfterUpdateRequest("proposals").Add(func(e *core.RecordUpdateEvent) error {
		return handleProposalAcceptance(app, streamClient, e)
	})

	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		diditCfg, err := loadDiditConfig(app)
		if err != nil {
			return err
		}
		diditClient := NewDiditClient(diditCfg)

		limiterStore := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:      50,
			Burst:     50,
			ExpiresIn: 1 * time.Minute,
		})
		e.Router.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store: limiterStore,
			IdentifierExtractor: func(c echo.Context) (string, error) {
				record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
				if ok && record != nil {
					return record.Id, nil
				}
				return c.RealIP(), nil
			},
		}))

		e.Router.POST("/stripe/checkout", func(c echo.Context) error {
			record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
			if !ok || record == nil {
				return apis.NewUnauthorizedError("unauthorized", nil)
			}
			if record.GetString("role") != "client" {
				return apis.NewForbiddenError("only clients can create checkout sessions", nil)
			}

			var payload stripeCheckoutRequest
			if err := c.Bind(&payload); err != nil {
				return apis.NewBadRequestError("invalid request body", err)
			}
			if payload.Amount <= 0 {
				return apis.NewBadRequestError("amount must be positive (in cents)", nil)
			}
			if payload.ProjectID == "" || payload.FreelancerID == "" {
				return apis.NewBadRequestError("project_id and freelancer_id are required", nil)
			}
			if payload.Currency == "" {
				payload.Currency = "usd"
			}
			payload.Currency = strings.ToLower(payload.Currency)

			project, err := app.Dao().FindRecordById("projects", payload.ProjectID)
			if err != nil {
				return apis.NewNotFoundError("project not found", err)
			}
			if project.GetBool("is_deleted") || project.GetString("client_id") != record.Id {
				return apis.NewForbiddenError("not allowed to pay for this project", nil)
			}

			freelancer, err := app.Dao().FindRecordById("users", payload.FreelancerID)
			if err != nil {
				return apis.NewNotFoundError("freelancer not found", err)
			}
			if freelancer.GetBool("is_deleted") || freelancer.GetString("role") != "freelancer" {
				return apis.NewBadRequestError("invalid freelancer", nil)
			}

			platformFee := calculatePlatformFee(payload.Amount, stripeCfg.PlatformFeePercent)

			paymentsCol, err := app.Dao().FindCollectionByNameOrId("payments")
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "payments collection not found", err)
			}

			payment := models.NewRecord(paymentsCol)
			payment.Set("client_id", record.Id)
			payment.Set("freelancer_id", freelancer.Id)
			payment.Set("amount", payload.Amount)
			payment.Set("currency", payload.Currency)
			payment.Set("stripe_checkout_session_id", "")
			payment.Set("stripe_payment_intent_id", "")
			payment.Set("status", "created")
			payment.Set("is_deleted", false)
			payment.Set("created_at", time.Now())

			if err := app.Dao().SaveRecord(payment); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "failed to create payment record", err)
			}

			sessionParams := &stripe.CheckoutSessionParams{
				Mode:               stripe.String(string(stripe.CheckoutSessionModePayment)),
				PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
				SuccessURL:         stripe.String(stripeCfg.SuccessURL),
				CancelURL:          stripe.String(stripeCfg.CancelURL),
				LineItems: []*stripe.CheckoutSessionLineItemParams{
					{
						PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
							Currency:   stripe.String(payload.Currency),
							UnitAmount: stripe.Int64(payload.Amount),
							ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
								Name: stripe.String(project.GetString("title")),
							},
						},
						Quantity: stripe.Int64(1),
					},
				},
				Metadata: map[string]string{
					"payment_id":           payment.Id,
					"client_id":            record.Id,
					"freelancer_id":        freelancer.Id,
					"project_id":           project.Id,
					"platform_fee_percent": formatPercent(stripeCfg.PlatformFeePercent),
					"platform_fee_amount":  strconv.FormatInt(platformFee, 10),
					"currency":             payload.Currency,
					"amount":               strconv.FormatInt(payload.Amount, 10),
				},
				PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{
					Metadata: map[string]string{
						"payment_id": payment.Id,
					},
				},
			}

			checkoutSession, err := session.New(sessionParams)
			if err != nil {
				payment.Set("status", "failed")
				_ = app.Dao().SaveRecord(payment)
				return apis.NewApiError(http.StatusInternalServerError, "failed to create checkout session", err)
			}

			payment.Set("stripe_checkout_session_id", checkoutSession.ID)
			if err := app.Dao().SaveRecord(payment); err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "failed to update payment record", err)
			}

			return c.JSON(http.StatusOK, map[string]any{
				"checkout_url": checkoutSession.URL,
				"payment_id":   payment.Id,
			})
		}, apis.RequireRecordAuth())

		e.Router.POST("/didit/verify", diditStartVerificationHandler(app, diditClient, diditCfg), apis.RequireRecordAuth())
		e.Router.POST("/didit/webhook", diditWebhookHandler(app, diditCfg))

		e.Router.POST("/stripe/webhook", func(c echo.Context) error {
			payload, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return apis.NewApiError(http.StatusBadRequest, "invalid payload", err)
			}

			event := stripe.Event{}

			if err := json.Unmarshal(payload, &event); err != nil {
				return apis.NewApiError(http.StatusBadRequest, "failed to parse webhook body json", err)
			}

			switch event.Type {
			case "checkout.session.completed":
				var session stripe.CheckoutSession
				if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
					return apis.NewApiError(http.StatusBadRequest, "invalid session payload", err)
				}
				paymentID := session.Metadata["payment_id"]
				if paymentID == "" {
					return apis.NewApiError(http.StatusBadRequest, "missing payment metadata", nil)
				}
				if err := updatePaymentFromWebhook(app, paymentID, "paid", session.PaymentIntent.ID); err != nil {
					return err
				}
				return c.NoContent(http.StatusOK)
			case "payment_intent.succeeded":
				var paymentIntent stripe.PaymentIntent
				err := json.Unmarshal(event.Data.Raw, &paymentIntent)
				if err != nil {
					return apis.NewApiError(http.StatusBadRequest, "error parsing webhook JSON", err)
				}
				paymentID := paymentIntent.Metadata["payment_id"]
				if paymentID == "" {
					return apis.NewApiError(http.StatusBadRequest, "missing payment metadata", nil)
				}
				if err := updatePaymentFromWebhook(app, paymentID, "paid", paymentIntent.ID); err != nil {
					return err
				}
				return c.NoContent(http.StatusOK)
			case "payment_intent.payment_failed":
				var intent stripe.PaymentIntent
				if err := json.Unmarshal(event.Data.Raw, &intent); err != nil {
					return apis.NewApiError(http.StatusBadRequest, "invalid payment intent payload", err)
				}
				paymentID := intent.Metadata["payment_id"]
				if paymentID == "" {
					return apis.NewApiError(http.StatusBadRequest, "missing payment metadata", nil)
				}
				if err := updatePaymentFromWebhook(app, paymentID, "failed", intent.ID); err != nil {
					return err
				}
				return c.NoContent(http.StatusOK)
			default:
				return apis.NewApiError(http.StatusBadRequest, fmt.Sprintf("unhandled event type: %s", event.Type), nil)
			}
		})

		e.Router.POST("/chat/token", func(c echo.Context) error {
			record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
			if !ok || record == nil {
				return apis.NewUnauthorizedError("unauthorized", nil)
			}

			token, err := streamClient.CreateToken(record.Id, time.Time{})
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "failed to generate token", err)
			}

			return c.JSON(http.StatusOK, map[string]any{
				"user_id": record.Id,
				"token":   token,
			})
		}, apis.RequireRecordAuth())

		e.Router.GET("/chat/conversations", func(c echo.Context) error {
			record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
			if !ok || record == nil {
				return apis.NewUnauthorizedError("unauthorized", nil)
			}

			proposals, err := app.Dao().FindRecordsByFilter(
				"proposals",
				"status = 'accepted' && is_deleted = false && (client_id = {:uid} || freelancer_id = {:uid})",
				"-created",
				200,
				0,
				dbx.Params{"uid": record.Id},
			)
			if err != nil {
				return apis.NewApiError(http.StatusInternalServerError, "failed to load proposals", err)
			}

			response := make([]map[string]any, 0, len(proposals))
			for _, proposal := range proposals {
				conversation, err := app.Dao().FindFirstRecordByFilter(
					"conversations",
					"proposal_id = {:pid} && is_deleted = false",
					dbx.Params{"pid": proposal.Id},
				)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					return apis.NewApiError(http.StatusInternalServerError, "failed to load conversation", err)
				}

				project, err := app.Dao().FindRecordById("projects", proposal.GetString("project_id"))
				if err != nil {
					return apis.NewApiError(http.StatusInternalServerError, "failed to load project", err)
				}

				counterpartId := proposal.GetString("freelancer_id")
				if counterpartId == record.Id {
					counterpartId = proposal.GetString("client_id")
				}

				counterpart, err := app.Dao().FindRecordById("users", counterpartId)
				if err != nil {
					return apis.NewApiError(http.StatusInternalServerError, "failed to load counterpart", err)
				}

				response = append(response, map[string]any{
					"conversation_id":   conversation.Id,
					"stream_channel_id": conversation.GetString("stream_channel_id"),
					"project": map[string]any{
						"id":     project.Id,
						"title":  project.GetString("title"),
						"status": project.GetString("status"),
					},
					"counterpart": map[string]any{
						"id":   counterpart.Id,
						"name": counterpart.GetString("name"),
						"role": counterpart.GetString("role"),
					},
					"proposal_id": proposal.Id,
				})
			}

			return c.JSON(http.StatusOK, response)
		}, apis.RequireRecordAuth())

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func updatePaymentFromWebhook(app *pocketbase.PocketBase, paymentID string, status string, paymentIntentID string) error {
	payment, err := app.Dao().FindRecordById("payments", paymentID)
	if err != nil {
		return apis.NewApiError(http.StatusNotFound, "payment not found", err)
	}

	currentStatus := payment.GetString("status")
	if currentStatus == status {
		return nil
	}

	if paymentIntentID != "" {
		payment.Set("stripe_payment_intent_id", paymentIntentID)
	}
	payment.Set("status", status)

	if err := app.Dao().SaveRecord(payment); err != nil {
		return apis.NewApiError(http.StatusInternalServerError, "failed to update payment", err)
	}

	return nil
}

func handleProposalAcceptance(app *pocketbase.PocketBase, client *stream.Client, e *core.RecordUpdateEvent) error {
	if e.Record == nil {
		return nil
	}

	if e.Record.GetBool("is_deleted") {
		return nil
	}

	if e.Record.GetString("status") != "accepted" {
		return nil
	}

	_, err := app.Dao().FindFirstRecordByFilter(
		"conversations",
		"proposal_id = {:pid} && is_deleted = false",
		dbx.Params{"pid": e.Record.Id},
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	project, err := app.Dao().FindRecordById("projects", e.Record.GetString("project_id"))
	if err != nil {
		return err
	}

	clientId := e.Record.GetString("client_id")
	freelancerId := e.Record.GetString("freelancer_id")
	channelId := "project_" + project.Id

	_, err = client.UpsertUsers(context.Background(),
		&stream.User{ID: clientId},
		&stream.User{ID: freelancerId},
	)
	if err != nil {
		return err
	}

	_, err = client.CreateChannelWithMembers(context.Background(), "messaging", channelId, clientId, freelancerId)
	if err != nil {
		return err
	}

	collection, err := app.Dao().FindCollectionByNameOrId("conversations")
	if err != nil {
		return err
	}

	conversation := models.NewRecord(collection)
	conversation.Set("project_id", project.Id)
	conversation.Set("proposal_id", e.Record.Id)
	conversation.Set("stream_channel_id", channelId)
	conversation.Set("is_deleted", false)

	return app.Dao().SaveRecord(conversation)
}

func calculatePlatformFee(amount int64, percent float64) int64 {
	return int64(math.Round(float64(amount) * percent / 100))
}

func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func mustStreamClient() *stream.Client {
	apiKey := os.Getenv("STREAM_API_KEY")
	apiSecret := os.Getenv("STREAM_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		log.Fatal("STREAM_API_KEY and STREAM_API_SECRET are required")
	}

	client, err := stream.NewClient(apiKey, apiSecret)
	if err != nil {
		log.Fatal(err)
	}

	return client
}

func mustStripeConfig() stripeConfig {
	secret := os.Getenv("STRIPE_SECRET_KEY")
	successURL := os.Getenv("STRIPE_SUCCESS_URL")
	cancelURL := os.Getenv("STRIPE_CANCEL_URL")
	feeStr := os.Getenv("STRIPE_PLATFORM_FEE_PERCENT")

	if secret == "" || successURL == "" || cancelURL == "" || feeStr == "" {
		log.Fatal("STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET, STRIPE_PLATFORM_FEE_PERCENT, STRIPE_SUCCESS_URL, STRIPE_CANCEL_URL are required")
	}

	feePercent, err := strconv.ParseFloat(feeStr, 64)
	if err != nil || feePercent < 0 || feePercent > 100 {
		log.Fatal("STRIPE_PLATFORM_FEE_PERCENT must be a valid number between 0 and 100")
	}

	return stripeConfig{
		SecretKey:          secret,
		PlatformFeePercent: feePercent,
		SuccessURL:         successURL,
		CancelURL:          cancelURL,
	}
}
