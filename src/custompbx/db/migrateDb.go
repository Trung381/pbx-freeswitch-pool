package db

import (
	"context"
	"custompbx/mainStruct"
	"database/sql"
	"errors"
	"fmt"
	"log"
)

func InitCustomDB() {
	createCustomSettingsTable(db)
}

func createCustomSettingsTable(db *sql.DB) {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS web_settings(
		param_name VARCHAR NOT NULL,
		param_value VARCHAR NOT NULL DEFAULT '',
		instance_id bigint NOT NULL REFERENCES fs_instances (id) ON DELETE CASCADE,
		UNIQUE(param_name, instance_id)
	)
	WITH (OIDS=FALSE);`,
	)
	panicErr(err)
}

func UpdateVersionRequest(instanceId int64, tx *sql.Tx) error {
	var err error
	if instanceId == 0 {
		return errors.New("no instance id")
	}
	if tx == nil {
		return errors.New("no transaction")
	}
	_, err = tx.Exec("INSERT INTO web_settings(param_name, param_value, instance_id) VALUES($1, $2, $3) ON CONFLICT(param_name, instance_id) DO UPDATE SET param_value = $2", mainStruct.CustomPBXVersion, mainStruct.Version, instanceId)
	if err != nil {
		return err
	}

	return err
}

func UpdateVersion(instanceId int64) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	err = UpdateVersionRequest(instanceId, tx)
	if err != nil {
		tx.Rollback()
		return err
	}
	tx.Commit()

	return nil
}

func GetVersion(instanceId int64) string {
	var version string
	var instanceIdExists string
	tx, err := db.Begin()
	if err != nil {
		log.Println(err)
		return ""
	}
	err = tx.QueryRow("SELECT column_name FROM information_schema.columns WHERE table_name='web_settings' and column_name='instance_id'").Scan(&instanceIdExists)
	if err != nil && err != sql.ErrNoRows {
		log.Println(err)
		tx.Rollback()
		return ""
	}
	if instanceIdExists == "" {
		err = tx.QueryRow("SELECT param_value FROM web_settings WHERE param_name = $1", mainStruct.CustomPBXVersion).Scan(&version)
	} else {
		err = tx.QueryRow("SELECT param_value FROM web_settings WHERE param_name = $1 AND instance_id = $2", mainStruct.CustomPBXVersion, instanceId).Scan(&version)
	}
	if err != nil {
		log.Println(err)
		tx.Rollback()
		return ""
	}
	tx.Commit()

	return version
}

func Migrate(switchName string) (bool, error) {
	var err error
	updated := false
	instanceId := getInstanceId(switchName)
	vertoUpdated, err := migrateVertoProfileParameterSecureUniqueness()
	if err != nil {
		return false, err
	}
	updated = updated || vertoUpdated
	switch GetVersion(instanceId) {
	case "":
		//return updated, nil
		//fallthrough
	case "0.0.1":
		fallthrough
	case "0.0.101":
		log.Println("Updating schema from 0.0.101")
		err = migrateForV0v0v102(instanceId)
		if err != nil {
			return false, err
		}
		updated = true
		fallthrough
	case "0.0.105":
		log.Println("Updating schema from 0.0.105")
		err = migrateForV0v0v106(instanceId)
		if err != nil {
			return false, err
		}
		updated = true
		fallthrough
	case "0.3.1":
		log.Println("Updating schema from 0.3.1")
		err = migrateForV0v3v2(instanceId)
		if err != nil {
			return false, err
		}
		updated = true
		fallthrough
	case "0.3.2":
		log.Println("Updating schema from 0.3.2")
		err = migrateForV0v3v3(instanceId)
		if err != nil {
			return false, err
		}
		updated = true
		fallthrough
	case "0.3.3":
		log.Println("Updating schema from 0.3.3")
		err = migrateForV0v3v4(instanceId)
		if err != nil {
			return false, err
		}
		updated = true
		fallthrough
	case mainStruct.Version:
		return updated, nil
	}

	err = UpdateVersion(instanceId)
	return updated, err
}

// migrateForV0v3v4 enables FreeSWITCH multi-registration on the internal
// Sofia profile served through XML-CURL. The profile is database-backed, so
// changing only /etc/freeswitch/sip_profiles/internal.xml has no effect when
// CustomPBX is the active configuration provider.
func migrateForV0v3v4(instanceId int64) error {
	if instanceId == 0 {
		return errors.New("no id")
	}

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO config_sofia_profile_parameters
  (parent_id, position, enabled, param_name, param_value, description)
SELECT
  profile.id,
  COALESCE((
    SELECT MAX(parameter.position) + 1
    FROM config_sofia_profile_parameters parameter
    WHERE parameter.parent_id = profile.id
  ), 1),
  TRUE,
  'multiple-registrations',
  'contact',
  'Allow one extension to ring all registered devices'
FROM config_sofia_profiles profile
WHERE profile.param_name = 'internal'
  AND profile.enabled = TRUE
ON CONFLICT (param_name, parent_id) DO UPDATE
SET param_value = EXCLUDED.param_value,
    enabled = TRUE,
    description = EXCLUDED.description`)
	if err != nil {
		tx.Rollback()
		return err
	}

	if err = UpdateVersionRequest(instanceId, tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func migrateForV0v3v3(instanceId int64) error {
	if instanceId == 0 {
		return errors.New("no id")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS linked_id varchar"); err != nil {
		tx.Rollback()
		return err
	}
	if err = UpdateVersionRequest(instanceId, tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func migrateForV0v3v2(instanceId int64) error {
	if instanceId == 0 {
		return errors.New("no id")
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	statements := []string{
		"ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS recording_status varchar NOT NULL DEFAULT 'local'",
		"ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS recording_object_key varchar",
		"ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS recording_size_bytes bigint",
		"ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS recording_uploaded_at timestamp with time zone",
		"ALTER TABLE IF EXISTS cdr ADD COLUMN IF NOT EXISTS recording_error varchar",
		"CREATE INDEX IF NOT EXISTS cdr_recording_object_key_idx ON cdr(recording_object_key) WHERE recording_object_key IS NOT NULL",
	}
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err = UpdateVersionRequest(instanceId, tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func migrateVertoProfileParameterSecureUniqueness() (bool, error) {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	var tableExists bool
	err = tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM information_schema.tables
    WHERE table_schema = 'public'
      AND table_name = 'config_verto_profile_parameters'
)`).Scan(&tableExists)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	if !tableExists {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	statements := []string{
		"ALTER TABLE IF EXISTS config_verto_profile_parameters ADD COLUMN IF NOT EXISTS secure VARCHAR",
		"ALTER TABLE IF EXISTS config_verto_profile_parameters ALTER COLUMN secure SET DEFAULT ''",
		"UPDATE config_verto_profile_parameters SET secure = '' WHERE secure IS NULL",
		"ALTER TABLE IF EXISTS config_verto_profile_parameters ALTER COLUMN secure SET NOT NULL",
		`
DO $$
DECLARE
    old_constraint text;
BEGIN
    FOR old_constraint IN
        SELECT tc.constraint_name
        FROM information_schema.table_constraints tc
        JOIN information_schema.key_column_usage kcu
          ON tc.constraint_schema = kcu.constraint_schema
         AND tc.constraint_name = kcu.constraint_name
         AND tc.table_name = kcu.table_name
        WHERE tc.table_schema = 'public'
          AND tc.table_name = 'config_verto_profile_parameters'
          AND tc.constraint_type = 'UNIQUE'
        GROUP BY tc.constraint_name
        HAVING array_agg(kcu.column_name::text ORDER BY kcu.column_name::text) = ARRAY['param_name', 'parent_id']
    LOOP
        EXECUTE format('ALTER TABLE config_verto_profile_parameters DROP CONSTRAINT %I', old_constraint);
    END LOOP;
END $$;
`,
		`
CREATE UNIQUE INDEX IF NOT EXISTS config_verto_profile_parameters_param_parent_secure_uq
    ON config_verto_profile_parameters(param_name, parent_id, secure)
`,
	}

	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			tx.Rollback()
			return false, err
		}
	}

	if err = tx.Commit(); err != nil {
		return false, err
	}

	return false, nil
}

func GetWebSettings(settings *mainStruct.WebSettings, instanceId int64) {
	params, err := db.Query(
		`SELECT param_name, param_value FROM web_settings WHERE instance_id = $1`, instanceId,
	)
	if err != nil {
		log.Printf("%+v", err)
		return
	}
	defer params.Close()
	for params.Next() {
		var name string
		var value string
		err := params.Scan(&name, &value)
		if err != nil {
			log.Printf("%+v", err)
			return
		}
		settings.Set(name, value)
	}
}

func AddWebSetting(name, value string, instanceId int64) error {
	_, err := db.Exec(
		`INSERT INTO web_settings(param_name, param_value, instance_id) VALUES($1, $2, $3) ON CONFLICT(param_name, instance_id) DO UPDATE SET param_value = $2`, name, value, instanceId)

	return err
}

func getInstanceId(switchName string) int64 {
	var instanceId int64
	err := db.QueryRow("SELECT id FROM fs_instances WHERE instance_name = $1", switchName).Scan(&instanceId)
	if err != nil {
		return 0
	}

	return instanceId
}

func migrateForV0v0v102(instanceId int64) error {
	if instanceId == 0 {
		return errors.New("no id")
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "ALTER TABLE IF EXISTS web_users ADD COLUMN IF NOT EXISTS instance_id BIGINT NOT NULL DEFAULT 0")
	if err != nil {
		tx.Rollback()
		return err
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
DO $$                  
    BEGIN 
        IF EXISTS
            ( SELECT 1
              FROM   information_schema.tables 
              WHERE  table_schema = 'public'
              AND    table_name = 'web_users'
            )
        THEN
            UPDATE web_users 
            SET instance_id = %d;
        END IF ;
    END
   $$ ;`, instanceId))
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.ExecContext(ctx, "ALTER TABLE IF EXISTS web_users DROP CONSTRAINT IF EXISTS web_users_ipk")
	_, err = tx.ExecContext(ctx, "ALTER TABLE IF EXISTS web_users ADD CONSTRAINT web_users_ipk FOREIGN KEY (instance_id) REFERENCES fs_instances (id) ON DELETE CASCADE ")
	if err != nil {
		tx.Rollback()
		return err
	}
	err = UpdateVersionRequest(instanceId, tx)
	if err != nil {
		tx.Rollback()
		return err
	}

	tx.Commit()

	return nil
}

func migrateForV0v0v106(instanceId int64) error {
	if instanceId == 0 {
		return errors.New("no id")
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "ALTER TABLE IF EXISTS config_cdr_pg_csv_schema ADD COLUMN IF NOT EXISTS quote VARCHAR")
	_, err = tx.ExecContext(ctx, "ALTER TABLE IF EXISTS config_cdr_pg_csv_schema DROP CONSTRAINT IF EXISTS config_cdr_pg_csv_schema_column_name_check")

	if err != nil {
		tx.Rollback()
		return err
	}
	err = UpdateVersionRequest(instanceId, tx)
	if err != nil {
		tx.Rollback()
		return err
	}

	tx.Commit()

	return nil
}
