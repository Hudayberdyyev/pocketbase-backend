package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
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
)

func main() {
	_ = godotenv.Load()
	app := pocketbase.New()
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{})

	streamClient := mustStreamClient()

	app.OnRecordAfterUpdateRequest("proposals").Add(func(e *core.RecordUpdateEvent) error {
		return handleProposalAcceptance(app, streamClient, e)
	})

	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
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
