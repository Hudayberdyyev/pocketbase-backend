package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/models/schema"
)

func init() {
	migrations.Register(func(db dbx.Builder) error {
		dao := daos.New(db)

		usersCol, err := dao.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		maxSelectOption := 1

		usersCol.Schema.AddField(&schema.SchemaField{
			Name: "didit_session_id",
			Type: schema.FieldTypeText,
		})
		usersCol.Schema.AddField(&schema.SchemaField{
			Name: "verification_status",
			Type: schema.FieldTypeSelect,
			Options: &schema.SelectOptions{
				Values:    []string{"pending", "approved", "rejected"},
				MaxSelect: maxSelectOption,
			},
		})
		usersCol.Schema.AddField(&schema.SchemaField{
			Name: "verification_reason",
			Type: schema.FieldTypeText,
		})

		return dao.SaveCollection(usersCol)
	}, func(db dbx.Builder) error {
		dao := daos.New(db)

		usersCol, err := dao.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		usersCol.Schema.RemoveField("didit_session_id")
		usersCol.Schema.RemoveField("verification_status")
		usersCol.Schema.RemoveField("verification_reason")

		return dao.SaveCollection(usersCol)
	})
}

