package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/models/schema"
)

var (
	maxSelectOption = 1
)

func init() {
	migrations.Register(func(db dbx.Builder) error {
		dao := daos.New(db)

		// -----------------------------
		// USERS (auth)
		// -----------------------------
		usersCol, err := dao.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		usersCol.Schema.AddField(&schema.SchemaField{
			Name:     "role",
			Type:     schema.FieldTypeSelect,
			Required: true,
			Options: &schema.SelectOptions{
				Values:    []string{"client", "freelancer"},
				MaxSelect: maxSelectOption,
			},
		})

		usersCol.Schema.AddField(&schema.SchemaField{
			Name: "is_deleted",
			Type: schema.FieldTypeBool,
		})

		usersCol.ListRule = strPtr("@request.auth.id = id && is_deleted = false")
		usersCol.ViewRule = strPtr("@request.auth.id = id && is_deleted = false")
		usersCol.UpdateRule = strPtr("@request.auth.id = id && is_deleted = false")

		if err := dao.SaveCollection(usersCol); err != nil {
			return err
		}

		// -----------------------------
		// PROJECTS
		// -----------------------------
		projects := &models.Collection{
			Name:       "projects",
			Type:       models.CollectionTypeBase,
			System:     false,
			CreateRule: strPtr("@request.auth.role = 'client' && @request.auth.is_deleted = false"),
			ListRule: strPtr(
				"is_deleted = false && @request.auth.id != '' && " +
					"((@request.auth.role = 'client' && client_id = @request.auth.id) || " +
					"(@request.auth.role = 'freelancer' && status = 'open'))",
			),
			ViewRule: strPtr(
				"is_deleted = false && @request.auth.id != '' && " +
					"((@request.auth.role = 'client' && client_id = @request.auth.id) || " +
					"(@request.auth.role = 'freelancer' && status = 'open'))",
			),
			UpdateRule: strPtr("is_deleted = false && @request.auth.role = 'client' && client_id = @request.auth.id"),
			DeleteRule: strPtr("false"),
			Schema: schema.NewSchema(
				&schema.SchemaField{
					Name:     "title",
					Type:     schema.FieldTypeText,
					Required: true,
				},
				&schema.SchemaField{
					Name:     "description",
					Type:     schema.FieldTypeText,
					Required: true,
				},
				&schema.SchemaField{
					Name:     "type",
					Type:     schema.FieldTypeSelect,
					Required: true,
					Options: &schema.SelectOptions{
						MaxSelect: maxSelectOption,
						Values:    []string{"remote", "onsite", "hybrid"},
					},
				},
				&schema.SchemaField{
					Name:     "client_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: usersCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "status",
					Type:     schema.FieldTypeSelect,
					Required: true,
					Options: &schema.SelectOptions{
						Values:    []string{"open", "in_progress", "closed"},
						MaxSelect: maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name: "is_deleted",
					Type: schema.FieldTypeBool,
				},
			),
		}

		if err := dao.SaveCollection(projects); err != nil {
			return err
		}

		projectsCol, err := dao.FindCollectionByNameOrId("projects")
		if err != nil {
			return err
		}

		// -----------------------------
		// PROPOSALS
		// -----------------------------
		proposals := &models.Collection{
			Name:   "proposals",
			Type:   models.CollectionTypeBase,
			System: false,
			CreateRule: strPtr(
				"@request.auth.role = 'freelancer' && @request.auth.is_deleted = false && " +
					"@request.data.project_id.status = 'open' && @request.data.project_id.is_deleted = false",
			),
			ListRule: strPtr("is_deleted = false && @request.auth.id != '' && (client_id = @request.auth.id || freelancer_id = @request.auth.id)"),
			ViewRule: strPtr("is_deleted = false && @request.auth.id != '' && (client_id = @request.auth.id || freelancer_id = @request.auth.id)"),
			UpdateRule: strPtr(
				"is_deleted = false && " +
					"((@request.auth.role = 'freelancer' && freelancer_id = @request.auth.id && status = 'sent') || " +
					"(@request.auth.role = 'client' && client_id = @request.auth.id))",
			),
			DeleteRule: strPtr("false"),
			Indexes: []string{
				"CREATE UNIQUE INDEX idx_proposals_project_freelancer ON proposals (project_id, freelancer_id)",
			},
			Schema: schema.NewSchema(
				&schema.SchemaField{
					Name:     "project_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: projectsCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "freelancer_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: usersCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "client_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: usersCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "message",
					Type:     schema.FieldTypeText,
					Required: true,
				},
				&schema.SchemaField{
					Name:     "status",
					Type:     schema.FieldTypeSelect,
					Required: true,
					Options: &schema.SelectOptions{
						Values:    []string{"sent", "accepted", "rejected"},
						MaxSelect: maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name: "is_deleted",
					Type: schema.FieldTypeBool,
				},
			),
		}

		if err := dao.SaveCollection(proposals); err != nil {
			return err
		}

		proposalsCol, err := dao.FindCollectionByNameOrId("proposals")
		if err != nil {
			return err
		}

		// -----------------------------
		// CONVERSATIONS
		// -----------------------------
		conversations := &models.Collection{
			Name:       "conversations",
			Type:       models.CollectionTypeBase,
			System:     false,
			CreateRule: strPtr("false"),
			ListRule:   strPtr("is_deleted = false && @request.auth.id != '' && (proposal_id.freelancer_id = @request.auth.id || proposal_id.client_id = @request.auth.id)"),
			ViewRule:   strPtr("is_deleted = false && @request.auth.id != '' && (proposal_id.freelancer_id = @request.auth.id || proposal_id.client_id = @request.auth.id)"),
			UpdateRule: strPtr("false"),
			DeleteRule: strPtr("false"),
			Schema: schema.NewSchema(
				&schema.SchemaField{
					Name:     "project_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: projectsCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "proposal_id",
					Type:     schema.FieldTypeRelation,
					Required: true,
					Options: &schema.RelationOptions{
						CollectionId: proposalsCol.Id,
						MaxSelect:    &maxSelectOption,
					},
				},
				&schema.SchemaField{
					Name:     "stream_channel_id",
					Type:     schema.FieldTypeText,
					Required: true,
				},
				&schema.SchemaField{
					Name: "is_deleted",
					Type: schema.FieldTypeBool,
				},
			),
		}

		return dao.SaveCollection(conversations)
	}, func(db dbx.Builder) error {
		dao := daos.New(db)

		collections := []string{
			"conversations",
			"proposals",
			"projects",
			"users",
		}

		for _, name := range collections {
			col, err := dao.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}

			if err := dao.DeleteCollection(col); err != nil {
				return err
			}
		}

		return nil
	})
}

func strPtr(value string) *string {
	return &value
}
