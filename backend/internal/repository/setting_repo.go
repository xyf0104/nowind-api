package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type settingRepository struct {
	client *ent.Client
	db     *sql.DB
}

func NewSettingRepository(client *ent.Client) service.SettingRepository {
	var db *sql.DB
	if client != nil {
		if driver, ok := client.Driver().(*entsql.Driver); ok {
			db = driver.DB()
		}
	}
	return &settingRepository{client: client, db: db}
}

func (r *settingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, service.ErrSettingNotFound
		}
		return nil, err
	}
	return &service.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	return r.client.Setting.
		Create().
		SetKey(key).
		SetValue(value).
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	settings, err := r.client.Setting.Query().Where(setting.KeyIn(keys...)).All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	now := time.Now()
	builders := make([]*ent.SettingCreate, 0, len(settings))
	for key, value := range settings {
		builders = append(builders, r.client.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now))
	}
	return r.client.Setting.
		CreateBulk(builders...).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	settings, err := r.client.Setting.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx)
	return err
}

// EnsureExecutionNodeClusterID creates a stable PostgreSQL-cluster identity
// without trusting a UUID that may have been copied by a logical database
// snapshot. The fallback is retained for non-PostgreSQL test doubles.
func (r *settingRepository) EnsureExecutionNodeClusterID(ctx context.Context, candidate string) (string, error) {
	identity := strings.TrimSpace(candidate)
	if r.db != nil {
		var systemIdentifier string
		if err := r.db.QueryRowContext(ctx, "SELECT system_identifier::text FROM pg_control_system()").Scan(&systemIdentifier); err != nil {
			return "", fmt.Errorf("read PostgreSQL system identifier: %w", err)
		}
		identity = strings.TrimSpace(systemIdentifier)
		if identity == "" {
			return "", fmt.Errorf("PostgreSQL system identifier is empty")
		}
	}
	if identity == "" {
		return "", fmt.Errorf("database cluster identity candidate is empty")
	}
	if err := r.client.Setting.
		Create().
		SetKey(service.SettingKeyExecutionNodeClusterID).
		SetValue(identity).
		SetUpdatedAt(time.Now()).
		OnConflictColumns(setting.FieldKey).
		Ignore().
		Exec(ctx); err != nil {
		return "", err
	}
	item, err := r.client.Setting.Query().Where(setting.KeyEQ(service.SettingKeyExecutionNodeClusterID)).Only(ctx)
	if err != nil {
		return "", err
	}
	// Existing installations may contain the older generated UUID. Replace it
	// with the actual PostgreSQL identity so a cloned settings table cannot keep
	// passing the shared-state check after the upgrade.
	if strings.TrimSpace(item.Value) != identity {
		if err := r.client.Setting.
			UpdateOneID(item.ID).
			SetValue(identity).
			SetUpdatedAt(time.Now()).
			Exec(ctx); err != nil {
			return "", err
		}
		return identity, nil
	}
	return item.Value, nil
}

// IsExecutionNodeJoinTargetEmpty allows a source-authoritative join only on a
// target that has no customer/business state. The target's bootstrap admin and
// migration metadata are intentionally ignored; silently discarding accounts,
// keys, usage, proxies, or groups would create an unrecoverable split ledger.
func (r *settingRepository) IsExecutionNodeJoinTargetEmpty(ctx context.Context) (bool, error) {
	if r.db == nil {
		return false, fmt.Errorf("target database handle is unavailable")
	}
	var hasBusinessData bool
	err := r.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM accounts WHERE deleted_at IS NULL)
			OR EXISTS (SELECT 1 FROM api_keys WHERE deleted_at IS NULL)
			OR EXISTS (SELECT 1 FROM usage_logs)
			OR EXISTS (
				SELECT 1
				FROM proxies
				WHERE deleted_at IS NULL
					AND NOT (
						name LIKE $1 || '%'
						AND protocol = 'socks5'
						AND host = '127.0.0.1'
						AND port = 19080
						AND username = substring(name FROM char_length($1) + 1)
					)
			)
			OR EXISTS (SELECT 1 FROM groups WHERE deleted_at IS NULL)
			OR (SELECT COUNT(*) FROM users WHERE deleted_at IS NULL) > 1
	`, service.ExecutionNodeBuiltinProxyNamePrefix).Scan(&hasBusinessData)
	if err != nil {
		return false, fmt.Errorf("inspect target business data: %w", err)
	}
	return !hasBusinessData, nil
}

// AcceptExecutionNodePairing consumes the single-use invite and publishes both
// sides of the peer relationship atomically. A zero row count means the invite
// was replayed or replaced by a newer one.
func (r *settingRepository) AcceptExecutionNodePairing(ctx context.Context, expectedInvite string, peerSettings map[string]string) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	count, err := tx.Setting.
		Update().
		Where(setting.KeyEQ(service.SettingKeyExecutionNodePairingInvite), setting.ValueEQ(expectedInvite)).
		SetValue("").
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if count != 1 {
		_ = tx.Rollback()
		return false, nil
	}
	builders := make([]*ent.SettingCreate, 0, len(peerSettings))
	for key, value := range peerSettings {
		builders = append(builders, tx.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(time.Now()))
	}
	if len(builders) > 0 {
		if err := tx.Setting.CreateBulk(builders...).OnConflictColumns(setting.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
