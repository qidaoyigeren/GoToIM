package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	mysql "github.com/go-sql-driver/mysql"
)

var (
	// ErrNotFound is returned when a stored resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrOrderStateChanged means another transaction changed the order status before this transaction committed.
	ErrOrderStateChanged = errors.New("order status changed")
)

// IsDuplicateKey reports whether err is a MySQL duplicate-key violation.
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "duplicate key")
}

// SQLStore persists Notify Server business state in a MySQL database.
type SQLStore struct {
	db      *sql.DB
	startAt time.Time
}

// Open opens a MySQL database and initializes the Notify schema.
func Open(dsn string) (*SQLStore, error) {
	return OpenMySQL(dsn)
}

// OpenMySQL opens a MySQL database and initializes the Notify schema.
func OpenMySQL(dsn string) (*SQLStore, error) {
	if strings.TrimSpace(dsn) == "" {
		dsn = "goim:goim@tcp(127.0.0.1:3306)/goim_notify?charset=utf8mb4&parseTime=true&loc=Local"
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)

	s := &SQLStore{db: db, startAt: time.Now()}
	if err := s.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database handle.
func (s *SQLStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLStore) initSchema(ctx context.Context) error {
	stmts := s.schemaStatements()
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init notify %s schema: %w", "mysql", err)
		}
	}
	if err := s.migrateSchema(ctx); err != nil {
		return fmt.Errorf("migrate notify %s schema: %w", "mysql", err)
	}
	return nil
}

func (s *SQLStore) schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS orders (
				order_id VARCHAR(64) PRIMARY KEY,
				user_id VARCHAR(64) NOT NULL,
				status VARCHAR(32) NOT NULL,
				items_json TEXT NOT NULL,
				total DOUBLE NOT NULL,
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				INDEX idx_orders_user_created (user_id, created_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS order_status_events (
				event_id VARCHAR(64) PRIMARY KEY,
				order_id VARCHAR(64) NOT NULL,
				from_status VARCHAR(32) NOT NULL,
				to_status VARCHAR(32) NOT NULL,
				extra_json TEXT NOT NULL,
				idempotency_key VARCHAR(255),
				created_at VARCHAR(40) NOT NULL,
				INDEX idx_order_status_events_order (order_id, created_at),
				CONSTRAINT fk_order_status_events_order FOREIGN KEY(order_id) REFERENCES orders(order_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notifications (
				notify_id VARCHAR(64) PRIMARY KEY,
				user_id VARCHAR(64) NOT NULL,
				type VARCHAR(32) NOT NULL,
				business_type VARCHAR(64) NOT NULL DEFAULT '',
				event_type VARCHAR(64) NOT NULL DEFAULT '',
				title VARCHAR(255) NOT NULL,
				content TEXT NOT NULL,
				order_id VARCHAR(64),
				status VARCHAR(32) NOT NULL,
				priority VARCHAR(32) NOT NULL DEFAULT 'normal',
				ttl_seconds BIGINT NOT NULL DEFAULT 600,
				ack_policy VARCHAR(32) NOT NULL DEFAULT 'any_device',
				expected_ack_count BIGINT NOT NULL DEFAULT 1,
				acked_count BIGINT NOT NULL DEFAULT 0,
				business_ack_status VARCHAR(32) NOT NULL DEFAULT 'pending',
				target_devices_json TEXT,
				primary_device_id VARCHAR(255),
				scenario_run_id VARCHAR(64),
				idempotency_key VARCHAR(255),
				trace_id VARCHAR(128),
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				INDEX idx_notifications_user_created (user_id, created_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_attempts (
				attempt_id VARCHAR(64) PRIMARY KEY,
				notify_id VARCHAR(64) NOT NULL,
				channel VARCHAR(32) NOT NULL,
				target VARCHAR(255) NOT NULL,
				status VARCHAR(32) NOT NULL,
				path VARCHAR(32) NOT NULL DEFAULT 'unknown',
				target_node VARCHAR(255),
				error_code VARCHAR(64),
				error_message TEXT,
				latency_ms DOUBLE NOT NULL DEFAULT 0,
				attempt_no BIGINT NOT NULL DEFAULT 1,
				trace_id VARCHAR(128),
				started_at VARCHAR(40) NOT NULL,
				finished_at VARCHAR(40),
				INDEX idx_notification_attempts_notify (notify_id, started_at),
				CONSTRAINT fk_notification_attempts_notify FOREIGN KEY(notify_id) REFERENCES notifications(notify_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_acks (
				ack_id VARCHAR(64) PRIMARY KEY,
				notify_id VARCHAR(64) NOT NULL,
				user_id VARCHAR(64) NOT NULL,
				msg_id VARCHAR(255),
				device_id VARCHAR(255),
				session_id VARCHAR(255),
				ack_key VARCHAR(255) NOT NULL DEFAULT 'legacy',
				latency_ms DOUBLE NOT NULL,
				policy_satisfied_at VARCHAR(40),
				trace_id VARCHAR(128),
				created_at VARCHAR(40) NOT NULL,
				UNIQUE KEY idx_notification_acks_notify_key (notify_id, ack_key),
				CONSTRAINT fk_notification_acks_notify FOREIGN KEY(notify_id) REFERENCES notifications(notify_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_outbox (
				outbox_id VARCHAR(64) PRIMARY KEY,
				notify_id VARCHAR(64) NOT NULL,
				user_id VARCHAR(64) NOT NULL,
				order_id VARCHAR(64),
				business_type VARCHAR(64) NOT NULL,
				event_type VARCHAR(64) NOT NULL,
				payload_json MEDIUMTEXT NOT NULL,
				priority VARCHAR(32) NOT NULL,
				ttl_seconds BIGINT NOT NULL,
				status VARCHAR(32) NOT NULL,
				retry_count BIGINT NOT NULL DEFAULT 0,
				next_retry_at VARCHAR(40),
				locked_by VARCHAR(255),
				locked_until VARCHAR(40),
				last_error TEXT,
				scenario_run_id VARCHAR(64),
				trace_id VARCHAR(128),
				compensation_strategy VARCHAR(64) NOT NULL DEFAULT 'retry_then_dlq',
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				INDEX idx_notification_outbox_due (status, next_retry_at, locked_until, priority, created_at),
				CONSTRAINT fk_notification_outbox_notify FOREIGN KEY(notify_id) REFERENCES notifications(notify_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_dlq (
				dlq_id VARCHAR(64) PRIMARY KEY,
				notify_id VARCHAR(64) NOT NULL,
				outbox_id VARCHAR(64) NOT NULL,
				user_id VARCHAR(64) NOT NULL,
				order_id VARCHAR(64),
				business_type VARCHAR(64),
				reason VARCHAR(64) NOT NULL,
				last_error TEXT NOT NULL,
				payload_json MEDIUMTEXT NOT NULL,
				retry_count BIGINT NOT NULL,
				compensation_strategy VARCHAR(64) NOT NULL DEFAULT 'retry_then_dlq',
				trace_id VARCHAR(128),
				created_at VARCHAR(40) NOT NULL,
				resolved_at VARCHAR(40),
				resolved_by VARCHAR(255),
				resolution TEXT,
				UNIQUE KEY idx_notification_dlq_outbox (outbox_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS campaigns (
				campaign_id VARCHAR(64) PRIMARY KEY,
				title VARCHAR(255) NOT NULL,
				description TEXT,
				business_type VARCHAR(64) NOT NULL,
				target_count BIGINT NOT NULL,
				sent_count BIGINT NOT NULL DEFAULT 0,
				failed_count BIGINT NOT NULL DEFAULT 0,
				status VARCHAR(32) NOT NULL DEFAULT 'active',
				rate_limit BIGINT NOT NULL DEFAULT 0,
				paused_at VARCHAR(40),
				cancelled_at VARCHAR(40),
				completed_at VARCHAR(40),
				idempotency_key VARCHAR(255),
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS campaign_targets (
				campaign_id VARCHAR(64) NOT NULL,
				user_id VARCHAR(64) NOT NULL,
				notify_id VARCHAR(64),
				status VARCHAR(32) NOT NULL,
				created_at VARCHAR(40) NOT NULL,
				PRIMARY KEY(campaign_id, user_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS campaign_audiences (
				audience_id VARCHAR(64) PRIMARY KEY,
				campaign_id VARCHAR(64) NOT NULL,
				name VARCHAR(255) NOT NULL,
				definition_json MEDIUMTEXT NOT NULL,
				target_count BIGINT NOT NULL DEFAULT 0,
				created_at VARCHAR(40) NOT NULL,
				INDEX idx_campaign_audiences_campaign (campaign_id, created_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS campaign_audience_batches (
				batch_id VARCHAR(64) PRIMARY KEY,
				audience_id VARCHAR(64) NOT NULL,
				campaign_id VARCHAR(64) NOT NULL,
				status VARCHAR(32) NOT NULL,
				start_offset BIGINT NOT NULL,
				end_offset BIGINT NOT NULL,
				target_count BIGINT NOT NULL DEFAULT 0,
				success_count BIGINT NOT NULL DEFAULT 0,
				failed_count BIGINT NOT NULL DEFAULT 0,
				locked_by VARCHAR(255),
				locked_until VARCHAR(40),
				last_error TEXT,
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				INDEX idx_campaign_audience_batches_audience (audience_id, status)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS campaign_audience_targets (
				audience_id VARCHAR(64) NOT NULL,
				campaign_id VARCHAR(64) NOT NULL,
				user_id VARCHAR(64) NOT NULL,
				batch_id VARCHAR(64),
				notify_id VARCHAR(64),
				status VARCHAR(32) NOT NULL,
				last_error TEXT,
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				PRIMARY KEY(audience_id, user_id),
				INDEX idx_campaign_audience_targets_batch (batch_id, status)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS scenario_runs (
				run_id VARCHAR(64) PRIMARY KEY,
				mode VARCHAR(64) NOT NULL,
				status VARCHAR(32) NOT NULL,
				qps INTEGER NOT NULL,
				users INTEGER NOT NULL,
				generated_orders BIGINT NOT NULL DEFAULT 0,
				generated_notifications BIGINT NOT NULL DEFAULT 0,
				sent_count BIGINT NOT NULL DEFAULT 0,
				acked_count BIGINT NOT NULL DEFAULT 0,
				failed_count BIGINT NOT NULL DEFAULT 0,
				dlq_count BIGINT NOT NULL DEFAULT 0,
				latency_p99_ms DOUBLE NOT NULL DEFAULT 0,
				started_at VARCHAR(40) NOT NULL,
				finished_at VARCHAR(40),
				last_error TEXT
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS scenario_events (
				event_id VARCHAR(64) PRIMARY KEY,
				run_id VARCHAR(64) NOT NULL,
				type VARCHAR(64) NOT NULL,
				payload_json MEDIUMTEXT NOT NULL,
				created_at VARCHAR(40) NOT NULL,
				CONSTRAINT fk_scenario_events_run FOREIGN KEY(run_id) REFERENCES scenario_runs(run_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_recovery_audit (
				audit_id VARCHAR(64) PRIMARY KEY,
				action VARCHAR(32) NOT NULL,
				operator VARCHAR(255) NOT NULL,
				dlq_id VARCHAR(64) NOT NULL,
				notify_id VARCHAR(64) NOT NULL,
				outbox_id VARCHAR(64) NOT NULL,
				business_type VARCHAR(64),
				reason VARCHAR(64),
				resolution TEXT,
				note TEXT,
				before_status VARCHAR(32),
				after_status VARCHAR(32),
				created_at VARCHAR(40) NOT NULL,
				INDEX idx_notification_recovery_audit_dlq (dlq_id, created_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS notification_replay_requests (
				request_id VARCHAR(64) PRIMARY KEY,
				action VARCHAR(32) NOT NULL,
				status VARCHAR(32) NOT NULL,
				operator VARCHAR(255) NOT NULL,
				approver VARCHAR(255),
				filter_json MEDIUMTEXT NOT NULL,
				matched_count BIGINT NOT NULL DEFAULT 0,
				threshold BIGINT NOT NULL DEFAULT 0,
				resolution TEXT,
				note TEXT,
				throttle_per_sec BIGINT NOT NULL DEFAULT 0,
				result_json MEDIUMTEXT,
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				decided_at VARCHAR(40),
				executed_at VARCHAR(40),
				INDEX idx_notification_replay_requests_status (status, created_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS chat_conversations (
				conversation_id VARCHAR(64) PRIMARY KEY,
				order_id VARCHAR(64) NOT NULL,
				customer_uid BIGINT NOT NULL,
				merchant_uid BIGINT NOT NULL,
				room_id VARCHAR(128) NOT NULL,
				last_message_id VARCHAR(64),
				last_message_at VARCHAR(40),
				created_at VARCHAR(40) NOT NULL,
				updated_at VARCHAR(40) NOT NULL,
				UNIQUE KEY idx_chat_conversations_order_pair (order_id, customer_uid, merchant_uid),
				INDEX idx_chat_conversations_customer (customer_uid, updated_at),
				INDEX idx_chat_conversations_merchant (merchant_uid, updated_at)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS chat_messages (
				message_id VARCHAR(64) PRIMARY KEY,
				conversation_id VARCHAR(64) NOT NULL,
				order_id VARCHAR(64) NOT NULL,
				sender_uid BIGINT NOT NULL,
				receiver_uid BIGINT NOT NULL,
				sender_role VARCHAR(32) NOT NULL,
				body TEXT NOT NULL,
				status VARCHAR(32) NOT NULL,
				delivery_path VARCHAR(64),
				created_at VARCHAR(40) NOT NULL,
				delivered_at VARCHAR(40),
				read_at VARCHAR(40),
				INDEX idx_chat_messages_conversation_created (conversation_id, created_at),
				INDEX idx_chat_messages_receiver_status (receiver_uid, status, created_at),
				CONSTRAINT fk_chat_messages_conversation FOREIGN KEY(conversation_id) REFERENCES chat_conversations(conversation_id)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
				` + "`key`" + ` VARCHAR(255) NOT NULL,
				scope VARCHAR(64) NOT NULL,
				resource_type VARCHAR(64) NOT NULL,
				resource_id VARCHAR(64) NOT NULL,
				response_snapshot MEDIUMTEXT NOT NULL,
				created_at VARCHAR(40) NOT NULL,
				PRIMARY KEY(scope, ` + "`key`" + `)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
}
func (s *SQLStore) migrateSchema(ctx context.Context) error {
	notificationColumns := map[string]string{
		"business_type":       "VARCHAR(64) NOT NULL DEFAULT ''",
		"event_type":          "VARCHAR(64) NOT NULL DEFAULT ''",
		"priority":            "VARCHAR(32) NOT NULL DEFAULT 'normal'",
		"ttl_seconds":         "BIGINT NOT NULL DEFAULT 600",
		"ack_policy":          "VARCHAR(32) NOT NULL DEFAULT 'any_device'",
		"expected_ack_count":  "BIGINT NOT NULL DEFAULT 1",
		"acked_count":         "BIGINT NOT NULL DEFAULT 0",
		"business_ack_status": "VARCHAR(32) NOT NULL DEFAULT 'pending'",
		"target_devices_json": "TEXT",
		"primary_device_id":   "VARCHAR(255)",
		"scenario_run_id":     "VARCHAR(64)",
		"trace_id":            "VARCHAR(128)",
	}
	for name, decl := range notificationColumns {
		if err := s.addColumnIfMissing(ctx, "notifications", name, decl); err != nil {
			return err
		}
	}
	ackKeyDecl := "VARCHAR(255) NOT NULL DEFAULT 'legacy'"
	if err := s.addColumnIfMissing(ctx, "notification_acks", "ack_key", ackKeyDecl); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "notification_acks", "policy_satisfied_at", "VARCHAR(40)"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "notification_acks", "trace_id", "VARCHAR(128)"); err != nil {
		return err
	}
	attemptColumns := map[string]string{
		"path":        "VARCHAR(32) NOT NULL DEFAULT 'unknown'",
		"target_node": "VARCHAR(255)",
		"error_code":  "VARCHAR(64)",
		"latency_ms":  "DOUBLE NOT NULL DEFAULT 0",
		"attempt_no":  "BIGINT NOT NULL DEFAULT 1",
		"trace_id":    "VARCHAR(128)",
	}
	for name, decl := range attemptColumns {
		if err := s.addColumnIfMissing(ctx, "notification_attempts", name, decl); err != nil {
			return err
		}
	}
	outboxScenarioDecl := "VARCHAR(64)"
	if err := s.addColumnIfMissing(ctx, "notification_outbox", "scenario_run_id", outboxScenarioDecl); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "notification_outbox", "trace_id", "VARCHAR(128)"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing(ctx, "notification_outbox", "compensation_strategy", "VARCHAR(64) NOT NULL DEFAULT 'retry_then_dlq'"); err != nil {
		return err
	}
	for name, decl := range map[string]string{
		"business_type":         "VARCHAR(64)",
		"compensation_strategy": "VARCHAR(64) NOT NULL DEFAULT 'retry_then_dlq'",
		"trace_id":              "VARCHAR(128)",
	} {
		if err := s.addColumnIfMissing(ctx, "notification_dlq", name, decl); err != nil {
			return err
		}
	}
	for name, decl := range map[string]string{
		"status":       "VARCHAR(32) NOT NULL DEFAULT 'active'",
		"rate_limit":   "BIGINT NOT NULL DEFAULT 0",
		"paused_at":    "VARCHAR(40)",
		"cancelled_at": "VARCHAR(40)",
		"completed_at": "VARCHAR(40)",
	} {
		if err := s.addColumnIfMissing(ctx, "campaigns", name, decl); err != nil {
			return err
		}
	}
	if err := s.migrateDateTimeCompatibility(ctx); err != nil {
		return err
	}
	for name, decl := range map[string]string{
		"locked_by":    "VARCHAR(255)",
		"locked_until": "VARCHAR(40)",
		"last_error":   "TEXT",
	} {
		if err := s.addColumnIfMissing(ctx, "campaign_audience_batches", name, decl); err != nil {
			return err
		}
	}
	if err := s.addColumnIfMissing(ctx, "campaign_audience_targets", "last_error", "TEXT"); err != nil {
		return err
	}
	return nil
}

func (s *SQLStore) addColumnIfMissing(ctx context.Context, table, column, decl string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, column)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Err()
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl))
	return err
}

type dateTimeMirrorColumn struct {
	table  string
	source string
	target string
}

func (s *SQLStore) migrateDateTimeCompatibility(ctx context.Context) error {
	mirrors := []dateTimeMirrorColumn{
		{table: "orders", source: "created_at", target: "created_at_dt"},
		{table: "orders", source: "updated_at", target: "updated_at_dt"},
		{table: "order_status_events", source: "created_at", target: "created_at_dt"},
		{table: "notifications", source: "created_at", target: "created_at_dt"},
		{table: "notifications", source: "updated_at", target: "updated_at_dt"},
		{table: "notification_attempts", source: "started_at", target: "started_at_dt"},
		{table: "notification_attempts", source: "finished_at", target: "finished_at_dt"},
		{table: "notification_acks", source: "created_at", target: "created_at_dt"},
		{table: "notification_acks", source: "policy_satisfied_at", target: "policy_satisfied_at_dt"},
		{table: "notification_outbox", source: "created_at", target: "created_at_dt"},
		{table: "notification_outbox", source: "updated_at", target: "updated_at_dt"},
		{table: "notification_outbox", source: "next_retry_at", target: "next_retry_at_dt"},
		{table: "notification_outbox", source: "locked_until", target: "locked_until_dt"},
		{table: "notification_dlq", source: "created_at", target: "created_at_dt"},
		{table: "notification_dlq", source: "resolved_at", target: "resolved_at_dt"},
		{table: "campaigns", source: "created_at", target: "created_at_dt"},
		{table: "campaigns", source: "updated_at", target: "updated_at_dt"},
		{table: "campaigns", source: "paused_at", target: "paused_at_dt"},
		{table: "campaigns", source: "cancelled_at", target: "cancelled_at_dt"},
		{table: "campaigns", source: "completed_at", target: "completed_at_dt"},
		{table: "campaign_audience_batches", source: "locked_until", target: "locked_until_dt"},
		{table: "idempotency_keys", source: "created_at", target: "created_at_dt"},
	}
	for _, mirror := range mirrors {
		if err := s.addColumnIfMissing(ctx, mirror.table, mirror.target, "DATETIME(3) NULL"); err != nil {
			return err
		}
		if err := s.backfillDateTimeMirror(ctx, mirror); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) backfillDateTimeMirror(ctx context.Context, mirror dateTimeMirrorColumn) error {
	stmt := fmt.Sprintf(`UPDATE %s
		SET %s = COALESCE(
			STR_TO_DATE(LEFT(REPLACE(REPLACE(%s, 'T', ' '), 'Z', ''), 26), '%%Y-%%m-%%d %%H:%%i:%%s.%%f'),
			STR_TO_DATE(LEFT(REPLACE(REPLACE(%s, 'T', ' '), 'Z', ''), 26), '%%Y-%%m-%%d %%H:%%i:%%s')
		)
		WHERE %s IS NULL AND %s IS NOT NULL AND %s <> ''`,
		quoteIdent(mirror.table), quoteIdent(mirror.target), quoteIdent(mirror.source),
		quoteIdent(mirror.source), quoteIdent(mirror.target), quoteIdent(mirror.source), quoteIdent(mirror.source))
	_, err := s.db.ExecContext(ctx, stmt)
	return err
}

func quoteIdent(v string) string {
	return "`" + strings.ReplaceAll(v, "`", "``") + "`"
}

// SaveIdempotency stores the first successful response for a scope/key pair.
func (s *SQLStore) SaveIdempotency(scope, key, resourceType, resourceID string, snapshot []byte) error {
	if key == "" {
		return nil
	}
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO idempotency_keys
		(`+"`key`"+`, scope, resource_type, resource_id, response_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key, scope, resourceType, resourceID, string(snapshot), formatTime(time.Now()))
	return err
}

// GetIdempotency returns a previous response snapshot for a scope/key pair.
func (s *SQLStore) GetIdempotency(scope, key string) ([]byte, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	var snapshot string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT response_snapshot FROM idempotency_keys WHERE scope = ? AND `+"`key`"+` = ?`,
		scope, key).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(snapshot), true, nil
}

// CreateOrderNotificationOutbox atomically creates the order, notification,
// outbox row, and optional idempotency replay snapshot.
func (s *SQLStore) CreateOrderNotificationOutbox(order *model.Order, n *model.Notification, outbox *model.NotificationOutbox, scope, key, resourceType, resourceID string, snapshot []byte) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertOrderTx(tx, order); err != nil {
		return err
	}
	if err := insertNotificationTx(tx, n); err != nil {
		return err
	}
	if err := insertOutboxTx(tx, outbox); err != nil {
		return err
	}
	if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangeOrderNotificationOutbox atomically updates the order, appends the
// status event, creates the notification/outbox, and saves idempotency.
func (s *SQLStore) ChangeOrderNotificationOutbox(order *model.Order, event *model.OrderStatusEvent, n *model.Notification, outbox *model.NotificationOutbox, scope, key, resourceType, resourceID string, snapshot []byte) error {
	extra, err := json.Marshal(emptyMap(event.Extra))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(context.Background(), `UPDATE orders SET status = ?, updated_at = ? WHERE order_id = ? AND status = ?`,
		string(order.Status), formatTime(order.UpdatedAt), order.OrderID, string(event.FromStatus))
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return ErrOrderStateChanged
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO order_status_events
		(event_id, order_id, from_status, to_status, extra_json, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.OrderID, string(event.FromStatus), string(event.ToStatus), string(extra),
		event.IdempotencyKey, formatTime(event.CreatedAt)); err != nil {
		return err
	}
	if err := insertNotificationTx(tx, n); err != nil {
		return err
	}
	if err := insertOutboxTx(tx, outbox); err != nil {
		return err
	}
	if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateNotificationOutbox atomically creates a custom notification and outbox row.
func (s *SQLStore) CreateNotificationOutbox(n *model.Notification, outbox *model.NotificationOutbox, scope, key, resourceType, resourceID string, snapshot []byte) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertNotificationTx(tx, n); err != nil {
		return err
	}
	if err := insertOutboxTx(tx, outbox); err != nil {
		return err
	}
	if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

func saveIdempotencyTx(tx *sql.Tx, scope, key, resourceType, resourceID string, snapshot []byte) error {
	if key == "" {
		return nil
	}
	_, err := tx.ExecContext(context.Background(), `INSERT INTO idempotency_keys
		(`+"`key`"+`, scope, resource_type, resource_id, response_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key, scope, resourceType, resourceID, string(snapshot), formatTime(time.Now()))
	return err
}

func insertOrderTx(tx *sql.Tx, order *model.Order) error {
	items, err := json.Marshal(order.Items)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(context.Background(), `INSERT INTO orders
		(order_id, user_id, status, items_json, total, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		order.OrderID, order.UserID, string(order.Status), string(items), order.Total,
		formatTime(order.CreatedAt), formatTime(order.UpdatedAt))
	return err
}

func insertNotificationTx(tx *sql.Tx, n *model.Notification) error {
	targetDevices, err := json.Marshal(n.TargetDeviceIDs)
	if err != nil {
		return err
	}
	normalizeNotificationAckDefaults(n)
	_, err = tx.ExecContext(context.Background(), `INSERT INTO notifications
		(notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		 priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		 target_devices_json, primary_device_id, scenario_run_id, idempotency_key, trace_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.NotifyID, n.UserID, string(n.Type), n.BusinessType, n.EventType, n.Title, n.Content, n.OrderID, n.Status,
		n.Priority, n.TTLSeconds, n.AckPolicy, n.ExpectedAckCount, n.AckedCount, n.BusinessAckStatus,
		string(targetDevices), n.PrimaryDeviceID, emptyToNil(n.ScenarioRunID), n.IdempotencyKey, nonEmpty(n.TraceID, n.NotifyID), formatTime(n.CreatedAt), formatTime(n.UpdatedAt))
	return err
}

func insertOutboxTx(tx *sql.Tx, outbox *model.NotificationOutbox) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO notification_outbox
		(outbox_id, notify_id, user_id, order_id, business_type, event_type, payload_json, priority, ttl_seconds,
		 status, retry_count, next_retry_at, locked_by, locked_until, last_error, scenario_run_id, trace_id, compensation_strategy, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outbox.OutboxID, outbox.NotifyID, outbox.UserID, outbox.OrderID, outbox.BusinessType, outbox.EventType,
		outbox.PayloadJSON, outbox.Priority, outbox.TTLSeconds, outbox.Status, outbox.RetryCount,
		nullableTime(outbox.NextRetryAt), emptyToNil(outbox.LockedBy), nullableTime(outbox.LockedUntil),
		emptyToNil(outbox.LastError), emptyToNil(outbox.ScenarioRunID), nonEmpty(outbox.TraceID, outbox.NotifyID),
		nonEmpty(outbox.CompensationStrategy, "retry_then_dlq"), formatTime(outbox.CreatedAt), formatTime(outbox.UpdatedAt))
	return err
}

// InsertOrder creates a persisted order.
func (s *SQLStore) InsertOrder(order *model.Order) error {
	items, err := json.Marshal(order.Items)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(context.Background(), `INSERT INTO orders
		(order_id, user_id, status, items_json, total, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		order.OrderID, order.UserID, string(order.Status), string(items), order.Total,
		formatTime(order.CreatedAt), formatTime(order.UpdatedAt))
	return err
}

// GetOrder returns an order by id.
func (s *SQLStore) GetOrder(orderID string) (*model.Order, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT order_id, user_id, status, items_json, total, created_at, updated_at
		FROM orders WHERE order_id = ?`, orderID)
	return scanOrder(row)
}

// ListUserOrders returns all persisted orders for a user.
func (s *SQLStore) ListUserOrders(userID string) ([]*model.Order, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT order_id, user_id, status, items_json, total, created_at, updated_at
		FROM orders WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*model.Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

// UpdateOrderStatusAndInsertEvent updates an order and appends a status event.
func (s *SQLStore) UpdateOrderStatusAndInsertEvent(order *model.Order, event *model.OrderStatusEvent) error {
	extra, err := json.Marshal(emptyMap(event.Extra))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(context.Background(), `UPDATE orders SET status = ?, updated_at = ? WHERE order_id = ? AND status = ?`,
		string(order.Status), formatTime(order.UpdatedAt), order.OrderID, string(event.FromStatus))
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return ErrOrderStateChanged
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO order_status_events
		(event_id, order_id, from_status, to_status, extra_json, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.OrderID, string(event.FromStatus), string(event.ToStatus), string(extra),
		event.IdempotencyKey, formatTime(event.CreatedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// InsertNotification creates a notification row.
func (s *SQLStore) InsertNotification(n *model.Notification) error {
	targetDevices, err := json.Marshal(n.TargetDeviceIDs)
	if err != nil {
		return err
	}
	normalizeNotificationAckDefaults(n)
	_, err = s.db.ExecContext(context.Background(), `INSERT INTO notifications
		(notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		 priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		 target_devices_json, primary_device_id, scenario_run_id, idempotency_key, trace_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.NotifyID, n.UserID, string(n.Type), n.BusinessType, n.EventType, n.Title, n.Content, n.OrderID, n.Status,
		n.Priority, n.TTLSeconds, n.AckPolicy, n.ExpectedAckCount, n.AckedCount, n.BusinessAckStatus,
		string(targetDevices), n.PrimaryDeviceID, emptyToNil(n.ScenarioRunID), n.IdempotencyKey, nonEmpty(n.TraceID, n.NotifyID), formatTime(n.CreatedAt), formatTime(n.UpdatedAt))
	return err
}

// GetNotification returns a notification by id.
func (s *SQLStore) GetNotification(notifyID string) (*model.Notification, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		COALESCE(target_devices_json, ''), COALESCE(primary_device_id, ''), COALESCE(scenario_run_id, ''), idempotency_key, COALESCE(trace_id, notify_id), created_at, updated_at
		FROM notifications WHERE notify_id = ?`, notifyID)
	return scanNotification(row)
}

// ListUserNotifications returns notification history for a user.
func (s *SQLStore) ListUserNotifications(userID string) ([]*model.Notification, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		COALESCE(target_devices_json, ''), COALESCE(primary_device_id, ''), COALESCE(scenario_run_id, ''), idempotency_key, COALESCE(trace_id, notify_id), created_at, updated_at
		FROM notifications WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []*model.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

// UpdateNotificationStatus updates a notification's delivery status.
func (s *SQLStore) UpdateNotificationStatus(notifyID, status string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE notifications SET status = ?, updated_at = ? WHERE notify_id = ?`,
		status, formatTime(updatedAt), notifyID)
	return err
}

// InsertAttempt records a notification delivery attempt.
func (s *SQLStore) InsertAttempt(a *model.NotificationAttempt) error {
	var finished any
	if a.FinishedAt != nil {
		finished = formatTime(*a.FinishedAt)
	}
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO notification_attempts
		(attempt_id, notify_id, channel, target, status, path, target_node, error_code, error_message, latency_ms, attempt_no, trace_id, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, a.NotifyID, a.Channel, a.Target, a.Status, nonEmpty(a.Path, "unknown"), a.TargetNode, a.ErrorCode, a.ErrorMessage,
		a.LatencyMs, nonZeroInt(a.AttemptNo, 1), nonEmpty(a.TraceID, a.NotifyID),
		formatTime(a.StartedAt), finished)
	return err
}

// ListAttemptsByNotifyID returns all delivery attempts for a notification, ordered by attempt number and time.
func (s *SQLStore) ListAttemptsByNotifyID(notifyID string) ([]*model.NotificationAttempt, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT attempt_id, notify_id, channel, target, status,
		COALESCE(path, ''), COALESCE(target_node, ''), COALESCE(error_code, ''), COALESCE(error_message, ''),
		latency_ms, attempt_no, COALESCE(trace_id, notify_id), started_at, COALESCE(finished_at, '')
		FROM notification_attempts WHERE notify_id = ? ORDER BY attempt_no ASC, started_at ASC`, notifyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []*model.NotificationAttempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

// OutboxCount returns the count for an outbox status.
func (s *SQLStore) OutboxCount(status string) (int64, error) {
	return s.count(`SELECT COUNT(*) FROM notification_outbox WHERE status = '` + status + `'`)
}

// GetOutbox returns one outbox row by id.
func (s *SQLStore) GetOutbox(outboxID string) (*model.NotificationOutbox, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT outbox_id, notify_id, user_id, order_id, business_type, event_type,
		payload_json, priority, ttl_seconds, status, retry_count, next_retry_at, locked_by, locked_until, last_error, COALESCE(scenario_run_id, ''), COALESCE(trace_id, notify_id), COALESCE(compensation_strategy, 'retry_then_dlq'), created_at, updated_at
		FROM notification_outbox WHERE outbox_id = ?`, outboxID)
	return scanOutbox(row)
}

// ClaimPublishedOutbox atomically claims an MQ-published outbox row for IM delivery.
func (s *SQLStore) ClaimPublishedOutbox(outboxID, workerID string, lockTTL time.Duration) (*model.NotificationOutbox, bool, error) {
	now := time.Now()
	nowS := formatTime(now)
	lockedUntil := formatTime(now.Add(lockTTL))
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'delivering', locked_by = ?, locked_until = ?, updated_at = ?
		WHERE outbox_id = ?
		  AND (
		    status = 'published'
		    OR (status = 'delivering' AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?))
		  )`,
		workerID, lockedUntil, nowS, outboxID, nowS)
	if err != nil {
		return nil, false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if rows == 0 {
		return nil, false, tx.Commit()
	}
	row := tx.QueryRowContext(context.Background(), `SELECT outbox_id, notify_id, user_id, order_id, business_type, event_type,
		payload_json, priority, ttl_seconds, status, retry_count, next_retry_at, locked_by, locked_until, last_error, COALESCE(scenario_run_id, ''), COALESCE(trace_id, notify_id), COALESCE(compensation_strategy, 'retry_then_dlq'), created_at, updated_at
		FROM notification_outbox WHERE outbox_id = ?`, outboxID)
	outbox, err := scanOutbox(row)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return outbox, true, nil
}

// ClaimOutbox locks due outbox rows for one worker.
func (s *SQLStore) ClaimOutbox(workerID string, batchSize int, lockTTL time.Duration) ([]*model.NotificationOutbox, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	now := time.Now()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	nowS := formatTime(now)
	rows, err := tx.QueryContext(context.Background(), `SELECT outbox_id FROM notification_outbox
		WHERE (
			(status IN ('pending','failed') AND (next_retry_at IS NULL OR next_retry_at = '' OR next_retry_at <= ?))
			OR (status = 'processing' AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?))
		  )
		  AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?)
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, created_at
		LIMIT ? FOR UPDATE SKIP LOCKED`, nowS, nowS, nowS, batchSize)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}

	lockedUntil := now.Add(lockTTL)
	claimedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		res, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
			SET status = 'processing', locked_by = ?, locked_until = ?, updated_at = ?
			WHERE outbox_id = ?
			  AND status IN ('pending','failed','processing')
			  AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?)`,
			workerID, formatTime(lockedUntil), nowS, id, nowS)
		if err != nil {
			return nil, err
		}
		if rowsAffected, err := res.RowsAffected(); err == nil && rowsAffected == 1 {
			claimedIDs = append(claimedIDs, id)
		}
	}
	ids = claimedIDs
	if len(ids) == 0 {
		return nil, tx.Commit()
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	claimedRows, err := tx.QueryContext(context.Background(), `SELECT outbox_id, notify_id, user_id, order_id, business_type, event_type,
		payload_json, priority, ttl_seconds, status, retry_count, next_retry_at, locked_by, locked_until, last_error, COALESCE(scenario_run_id, ''), COALESCE(trace_id, notify_id), COALESCE(compensation_strategy, 'retry_then_dlq'), created_at, updated_at
		FROM notification_outbox WHERE outbox_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer claimedRows.Close()
	var outboxes []*model.NotificationOutbox
	for claimedRows.Next() {
		outbox, err := scanOutbox(claimedRows)
		if err != nil {
			return nil, err
		}
		outboxes = append(outboxes, outbox)
	}
	if err := claimedRows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return outboxes, nil
}

// MarkOutboxSent records a successful delivery attempt and marks the outbox sent.
func (s *SQLStore) MarkOutboxSent(outboxID string, attempt *model.NotificationAttempt, at time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertAttemptTx(tx, attempt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'sent', locked_by = NULL, locked_until = NULL, last_error = NULL, updated_at = ?
		WHERE outbox_id = ?`, formatTime(at), outboxID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notifications
		SET status = 'delivered', updated_at = ? WHERE notify_id = ? AND status <> 'acked'`,
		formatTime(at), attempt.NotifyID); err != nil {
		return err
	}
	_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'sent' WHERE notify_id = ?`, attempt.NotifyID)
	return tx.Commit()
}

// MarkOutboxPublished records a successful MQ publish and releases the claim.
func (s *SQLStore) MarkOutboxPublished(outboxID, workerID string, attempt *model.NotificationAttempt, at time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if attempt != nil {
		if err := insertAttemptTx(tx, attempt); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'published', locked_by = NULL, locked_until = NULL, last_error = NULL, updated_at = ?
		WHERE outbox_id = ? AND status = 'processing' AND locked_by = ?`,
		formatTime(at), outboxID, workerID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	if attempt != nil {
		if _, err := tx.ExecContext(context.Background(), `UPDATE notifications
			SET status = 'queued', updated_at = ? WHERE notify_id = ? AND status NOT IN ('acked','delivered')`,
			formatTime(at), attempt.NotifyID); err != nil {
			return err
		}
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'queued' WHERE notify_id = ?`, attempt.NotifyID)
	}
	return tx.Commit()
}

// MarkOutboxPublishRetry records a failed MQ publish and schedules relay retry.
func (s *SQLStore) MarkOutboxPublishRetry(outboxID, workerID string, retryCount int64, nextRetryAt time.Time, lastError string, attempt *model.NotificationAttempt) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if attempt != nil {
		if err := insertAttemptTx(tx, attempt); err != nil {
			return err
		}
	}
	now := time.Now()
	res, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'failed', retry_count = ?, next_retry_at = ?, locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
		WHERE outbox_id = ? AND status = 'processing' AND locked_by = ?`,
		retryCount, formatTime(nextRetryAt), lastError, formatTime(now), outboxID, workerID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	if attempt != nil {
		_, _ = tx.ExecContext(context.Background(), `UPDATE notifications SET status = 'retrying', updated_at = ? WHERE notify_id = ? AND status <> 'acked'`,
			formatTime(now), attempt.NotifyID)
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'retrying' WHERE notify_id = ?`, attempt.NotifyID)
	}
	return tx.Commit()
}

// MarkOutboxExpired marks an MQ-published notification as expired without DLQ.
func (s *SQLStore) MarkOutboxExpired(outboxID, notifyID, lastError string, at time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if outboxID != "" {
		if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
			SET status = 'expired', locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
			WHERE outbox_id = ? AND status NOT IN ('sent','dlq','expired')`,
			lastError, formatTime(at), outboxID); err != nil {
			return err
		}
	} else if notifyID != "" {
		if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
			SET status = 'expired', locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
			WHERE notify_id = ? AND status NOT IN ('sent','dlq','expired')`,
			lastError, formatTime(at), notifyID); err != nil {
			return err
		}
	}
	if notifyID != "" {
		if _, err := tx.ExecContext(context.Background(), `UPDATE notifications SET status = 'expired', updated_at = ? WHERE notify_id = ? AND status <> 'acked'`,
			formatTime(at), notifyID); err != nil {
			return err
		}
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'expired' WHERE notify_id = ?`, notifyID)
	}
	return tx.Commit()
}

// MarkOutboxRetry records a failed attempt and schedules the next retry.
func (s *SQLStore) MarkOutboxRetry(outboxID string, retryCount int64, nextRetryAt time.Time, lastError string, attempt *model.NotificationAttempt) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertAttemptTx(tx, attempt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'failed', retry_count = ?, next_retry_at = ?, locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
		WHERE outbox_id = ?`,
		retryCount, formatTime(nextRetryAt), lastError, formatTime(time.Now()), outboxID); err != nil {
		return err
	}
	if attempt != nil {
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'retrying' WHERE notify_id = ?`, attempt.NotifyID)
	}
	return tx.Commit()
}

// MoveOutboxToDLQ records a terminal failure.
func (s *SQLStore) MoveOutboxToDLQ(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ, attempt *model.NotificationAttempt, status string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if attempt != nil {
		if err := insertAttemptTx(tx, attempt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = ?, locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
		WHERE outbox_id = ?`, status, dlq.LastError, formatTime(dlq.CreatedAt), outbox.OutboxID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notifications SET status = ?, updated_at = ? WHERE notify_id = ?`,
		status, formatTime(dlq.CreatedAt), outbox.NotifyID); err != nil {
		return err
	}
	targetStatus := "failed"
	if status == "dlq" {
		targetStatus = "dlq"
	}
	_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = ? WHERE notify_id = ?`, targetStatus, outbox.NotifyID)
	if status == "dlq" {
		if _, err := tx.ExecContext(context.Background(), s.insertIgnoreDLQSQL(), dlq.DLQID, dlq.NotifyID, dlq.OutboxID, dlq.UserID,
			dlq.OrderID, outbox.BusinessType, dlq.Reason, dlq.LastError, dlq.PayloadJSON, dlq.RetryCount,
			nonEmpty(dlq.CompensationStrategy, outbox.CompensationStrategy), nonEmpty(dlq.TraceID, outbox.TraceID), formatTime(dlq.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertAttemptTx(tx *sql.Tx, a *model.NotificationAttempt) error {
	var finished any
	if a.FinishedAt != nil {
		finished = formatTime(*a.FinishedAt)
	}
	_, err := tx.ExecContext(context.Background(), `INSERT INTO notification_attempts
		(attempt_id, notify_id, channel, target, status, path, target_node, error_code, error_message, latency_ms, attempt_no, trace_id, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, a.NotifyID, a.Channel, a.Target, a.Status, nonEmpty(a.Path, "unknown"), a.TargetNode, a.ErrorCode, a.ErrorMessage,
		a.LatencyMs, nonZeroInt(a.AttemptNo, 1), nonEmpty(a.TraceID, a.NotifyID),
		formatTime(a.StartedAt), finished)
	return err
}

// RecordAck inserts an ACK and updates the notification business ACK state.
func (s *SQLStore) RecordAck(ack *model.NotificationAck) (bool, error) {
	return s.RecordAckWithIdempotency(ack, "", "", "", "", nil)
}

// RecordAckWithIdempotency records ACK and replay snapshot in one transaction.
func (s *SQLStore) RecordAckWithIdempotency(ack *model.NotificationAck, scope, key, resourceType, resourceID string, snapshot []byte) (bool, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var status, ackPolicy, businessAckStatus, targetDevicesJSON, primaryDeviceID, notifTraceID string
	var expected, acked int64
	if err := tx.QueryRowContext(context.Background(), `SELECT status, ack_policy, expected_ack_count, acked_count, business_ack_status,
		COALESCE(target_devices_json, ''), COALESCE(primary_device_id, ''), COALESCE(trace_id, notify_id)
		FROM notifications WHERE notify_id = ?`, ack.NotifyID).Scan(&status, &ackPolicy, &expected, &acked, &businessAckStatus, &targetDevicesJSON, &primaryDeviceID, &notifTraceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if ack.TraceID == "" {
		ack.TraceID = notifTraceID
	}
	var targetDevices []string
	_ = json.Unmarshal([]byte(targetDevicesJSON), &targetDevices)
	if businessAckStatus == "satisfied" && (ackPolicy == "any_device" || ackPolicy == "none" || ackPolicy == "best_effort" || ackPolicy == "") {
		if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, ackSnapshot(false, snapshot)); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	ackKey := normalizeAckKey(ack)
	res, err := tx.ExecContext(context.Background(), s.insertIgnoreAckSQL(),
		ack.AckID, ack.NotifyID, ack.UserID, ack.MsgID, ack.DeviceID, ack.SessionID, ackKey, ack.LatencyMs, nil, ack.TraceID, formatTime(ack.CreatedAt))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, ackSnapshot(false, snapshot)); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if ackPolicy != "all_devices" || len(targetDevices) == 0 || stringInSlice(ack.DeviceID, targetDevices) {
		acked++
	}
	satisfied := ackPolicySatisfied(ackPolicy, ack.DeviceID, primaryDeviceID, targetDevices, acked, expected)
	nextBusiness := "pending"
	nextStatus := status
	var policySatisfiedAt any
	if satisfied {
		nextBusiness = "satisfied"
		nextStatus = "acked"
		policySatisfiedAt = formatTime(ack.CreatedAt)
		if _, err := tx.ExecContext(context.Background(), `UPDATE notification_acks SET policy_satisfied_at = ? WHERE ack_id = ?`,
			policySatisfiedAt, ack.AckID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notifications
		SET status = ?, acked_count = ?, business_ack_status = ?, updated_at = ? WHERE notify_id = ?`,
		nextStatus, acked, nextBusiness, formatTime(ack.CreatedAt), ack.NotifyID); err != nil {
		return false, err
	}
	if satisfied {
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status = 'acked' WHERE notify_id = ?`, ack.NotifyID)
	}
	if err := saveIdempotencyTx(tx, scope, key, resourceType, resourceID, ackSnapshot(true, snapshot)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// AckCount returns persisted ACK row count.
func (s *SQLStore) AckCount() (int64, error) {
	return s.count(`SELECT COUNT(*) FROM notification_acks`)
}

// NotificationCount returns persisted notification row count.
func (s *SQLStore) NotificationCount() (int64, error) {
	return s.count(`SELECT COUNT(*) FROM notifications`)
}

// StatusEventCount returns persisted order status event count.
func (s *SQLStore) StatusEventCount() (int64, error) {
	return s.count(`SELECT COUNT(*) FROM order_status_events`)
}

func (s *SQLStore) count(query string) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *SQLStore) insertIgnoreAckSQL() string {
	return `INSERT IGNORE INTO notification_acks
		(ack_id, notify_id, user_id, msg_id, device_id, session_id, ack_key, latency_ms, policy_satisfied_at, trace_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (s *SQLStore) insertIgnoreDLQSQL() string {
	return `INSERT IGNORE INTO notification_dlq
		(dlq_id, notify_id, outbox_id, user_id, order_id, business_type, reason, last_error, payload_json, retry_count, compensation_strategy, trace_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (s *SQLStore) insertIgnoreCampaignTargetSQL() string {
	return `INSERT IGNORE INTO campaign_targets
		(campaign_id, user_id, notify_id, status, created_at) VALUES (?, ?, ?, ?, ?)`
}

// ListDLQ returns unresolved DLQ rows newest first.
func (s *SQLStore) ListDLQ(limit int) ([]*model.NotificationDLQ, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT dlq_id, notify_id, outbox_id, user_id, order_id, reason,
		last_error, payload_json, retry_count, COALESCE(business_type, ''), COALESCE(compensation_strategy, 'retry_then_dlq'), COALESCE(trace_id, notify_id), created_at, resolved_at, resolved_by, resolution
		FROM notification_dlq ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*model.NotificationDLQ
	for rows.Next() {
		item, err := scanDLQ(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetDLQ returns one DLQ row.
func (s *SQLStore) GetDLQ(id string) (*model.NotificationDLQ, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT dlq_id, notify_id, outbox_id, user_id, order_id, reason,
		last_error, payload_json, retry_count, COALESCE(business_type, ''), COALESCE(compensation_strategy, 'retry_then_dlq'), COALESCE(trace_id, notify_id), created_at, resolved_at, resolved_by, resolution
		FROM notification_dlq WHERE dlq_id = ?`, id)
	return scanDLQ(row)
}

// ReplayDLQ marks the outbox pending again and resolves the DLQ row.
func (s *SQLStore) ReplayDLQ(id, resolvedBy string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var outboxID string
	if err := tx.QueryRowContext(context.Background(), `SELECT outbox_id FROM notification_dlq WHERE dlq_id = ?`, id).Scan(&outboxID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
		SET status = 'pending', next_retry_at = NULL, locked_by = NULL, locked_until = NULL, last_error = NULL, updated_at = ?
		WHERE outbox_id = ?`, formatTime(now), outboxID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notification_dlq
		SET resolved_at = ?, resolved_by = ?, resolution = 'replay' WHERE dlq_id = ?`,
		formatTime(now), resolvedBy, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveDLQ closes a DLQ item without replay.
func (s *SQLStore) ResolveDLQ(id, resolvedBy, resolution string) error {
	res, err := s.db.ExecContext(context.Background(), `UPDATE notification_dlq
		SET resolved_at = ?, resolved_by = ?, resolution = ? WHERE dlq_id = ?`,
		formatTime(time.Now()), resolvedBy, resolution, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertCampaign records a flash-sale campaign summary.
func (s *SQLStore) InsertCampaign(campaignID, title, description, businessType string, targetCount int, idempotencyKey string, createdAt time.Time) error {
	return s.InsertCampaignWithRateLimit(campaignID, title, description, businessType, targetCount, idempotencyKey, 0, createdAt)
}

// InsertCampaignWithRateLimit records a campaign summary with a configured token-bucket rate.
func (s *SQLStore) InsertCampaignWithRateLimit(campaignID, title, description, businessType string, targetCount int, idempotencyKey string, rateLimit int, createdAt time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO campaigns
		(campaign_id, title, description, business_type, target_count, sent_count, failed_count, status, rate_limit, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, 'active', ?, ?, ?, ?)`,
		campaignID, title, description, businessType, targetCount, rateLimit, idempotencyKey, formatTime(createdAt), formatTime(createdAt))
	return err
}

// InsertCampaignTarget records one targeted campaign recipient.
func (s *SQLStore) InsertCampaignTarget(campaignID, userID, notifyID, status string, createdAt time.Time) error {
	_, err := s.db.ExecContext(context.Background(), s.insertIgnoreCampaignTargetSQL(),
		campaignID, userID, notifyID, status, formatTime(createdAt))
	return err
}

// GetCampaignTargetStatus returns the persisted delivery state for one campaign target.
func (s *SQLStore) GetCampaignTargetStatus(campaignID, userID string) (string, error) {
	var status string
	err := s.db.QueryRowContext(context.Background(), `SELECT status FROM campaign_targets WHERE campaign_id = ? AND user_id = ?`,
		campaignID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

// ImportCampaignAudience stores an imported campaign audience snapshot and batch rows.
func (s *SQLStore) ImportCampaignAudience(campaignID, name string, definition map[string]string, targetUIDs []string, batchSize int) (*model.CampaignAudience, []*model.CampaignAudienceBatch, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	now := time.Now()
	audience := &model.CampaignAudience{
		AudienceID:  fmt.Sprintf("aud_%d", now.UnixNano()),
		CampaignID:  campaignID,
		Name:        nonEmpty(name, "imported"),
		Definition:  definition,
		TargetCount: int64(len(targetUIDs)),
		CreatedAt:   now,
	}
	defJSON, err := json.Marshal(emptyStringMap(definition))
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO campaign_audiences
		(audience_id, campaign_id, name, definition_json, target_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		audience.AudienceID, campaignID, audience.Name, string(defJSON), audience.TargetCount, formatTime(now)); err != nil {
		return nil, nil, err
	}
	var batches []*model.CampaignAudienceBatch
	for start := 0; start < len(targetUIDs); start += batchSize {
		end := start + batchSize
		if end > len(targetUIDs) {
			end = len(targetUIDs)
		}
		batch := &model.CampaignAudienceBatch{
			BatchID:     fmt.Sprintf("audb_%d_%d", now.UnixNano(), len(batches)+1),
			AudienceID:  audience.AudienceID,
			CampaignID:  campaignID,
			Status:      "pending",
			StartOffset: start,
			EndOffset:   end,
			TargetCount: int64(end - start),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.insertCampaignAudienceBatch(batch, targetUIDs[start:end], now); err != nil {
			return audience, batches, err
		}
		batches = append(batches, batch)
	}
	return audience, batches, nil
}

func (s *SQLStore) insertCampaignAudienceBatch(batch *model.CampaignAudienceBatch, targetUIDs []string, now time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO campaign_audience_batches
			(batch_id, audience_id, campaign_id, status, start_offset, end_offset, target_count, success_count, failed_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		batch.BatchID, batch.AudienceID, batch.CampaignID, batch.Status, batch.StartOffset, batch.EndOffset, batch.TargetCount, formatTime(now), formatTime(now)); err != nil {
		return err
	}
	for _, uid := range targetUIDs {
		if _, err := tx.ExecContext(context.Background(), `INSERT IGNORE INTO campaign_audience_targets
				(audience_id, campaign_id, user_id, batch_id, notify_id, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, NULL, 'pending', ?, ?)`,
			batch.AudienceID, batch.CampaignID, uid, batch.BatchID, formatTime(now), formatTime(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListCampaignAudienceTargets returns imported audience targets.
func (s *SQLStore) ListCampaignAudienceTargets(audienceID string, statuses []string, limit int) ([]*model.CampaignAudienceTarget, error) {
	if limit <= 0 {
		limit = 10000
	}
	query := `SELECT audience_id, campaign_id, user_id, COALESCE(batch_id, ''), COALESCE(notify_id, ''), status, created_at, updated_at
		FROM campaign_audience_targets WHERE audience_id = ?`
	args := []any{audienceID}
	if len(statuses) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
		query += " AND status IN (" + placeholders + ")"
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	query += fmt.Sprintf(" ORDER BY created_at ASC LIMIT %d", limit)
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []*model.CampaignAudienceTarget
	for rows.Next() {
		target, err := scanCampaignAudienceTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// ListCampaignAudienceTargetsByBatch returns targets in one imported audience batch.
func (s *SQLStore) ListCampaignAudienceTargetsByBatch(batchID string, statuses []string, limit int) ([]*model.CampaignAudienceTarget, error) {
	if limit <= 0 {
		limit = 1000
	}
	query := `SELECT audience_id, campaign_id, user_id, COALESCE(batch_id, ''), COALESCE(notify_id, ''), status, created_at, updated_at
		FROM campaign_audience_targets WHERE batch_id = ?`
	args := []any{batchID}
	if len(statuses) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
		query += " AND status IN (" + placeholders + ")"
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	query += fmt.Sprintf(" ORDER BY created_at ASC LIMIT %d", limit)
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []*model.CampaignAudienceTarget
	for rows.Next() {
		target, err := scanCampaignAudienceTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// ListCampaignAudienceBatches returns batches for an audience.
func (s *SQLStore) ListCampaignAudienceBatches(audienceID string) ([]*model.CampaignAudienceBatch, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT batch_id, audience_id, campaign_id, status, start_offset,
		end_offset, target_count, success_count, failed_count, created_at, updated_at
		FROM campaign_audience_batches WHERE audience_id = ? ORDER BY start_offset ASC`, audienceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []*model.CampaignAudienceBatch
	for rows.Next() {
		batch, err := scanCampaignAudienceBatch(rows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

// MarkCampaignAudienceTargetCreated links an audience target to a notification.
func (s *SQLStore) MarkCampaignAudienceTargetCreated(audienceID, userID, notifyID string) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET notify_id = ?, status = 'created', updated_at = ? WHERE audience_id = ? AND user_id = ?`,
		notifyID, formatTime(time.Now()), audienceID, userID)
	return err
}

// ClaimCampaignAudienceBatches locks pending audience batches for background notification generation.
func (s *SQLStore) ClaimCampaignAudienceBatches(workerID string, batchSize int, lockTTL time.Duration) ([]*model.CampaignAudienceBatch, error) {
	if batchSize <= 0 {
		batchSize = 10
	}
	now := time.Now()
	nowS := formatTime(now)
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(context.Background(), `SELECT batch_id FROM campaign_audience_batches
		WHERE status IN ('pending','retrying')
		  AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?)
		ORDER BY created_at ASC LIMIT ? FOR UPDATE SKIP LOCKED`, nowS, batchSize)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	lockedUntil := formatTime(now.Add(lockTTL))
	claimedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		res, err := tx.ExecContext(context.Background(), `UPDATE campaign_audience_batches
			SET status = 'processing', locked_by = ?, locked_until = ?, updated_at = ?
			WHERE batch_id = ? AND status IN ('pending','retrying')
			  AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?)`,
			workerID, lockedUntil, nowS, id, nowS)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err == nil && n == 1 {
			claimedIDs = append(claimedIDs, id)
		}
	}
	if len(claimedIDs) == 0 {
		return nil, tx.Commit()
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(claimedIDs)), ",")
	args := make([]any, 0, len(claimedIDs))
	for _, id := range claimedIDs {
		args = append(args, id)
	}
	claimedRows, err := tx.QueryContext(context.Background(), `SELECT batch_id, audience_id, campaign_id, status, start_offset,
		end_offset, target_count, success_count, failed_count, created_at, updated_at
		FROM campaign_audience_batches WHERE batch_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer claimedRows.Close()
	var batches []*model.CampaignAudienceBatch
	for claimedRows.Next() {
		batch, err := scanCampaignAudienceBatch(claimedRows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	if err := claimedRows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return batches, nil
}

// CreateCampaignAudienceTargetNotification atomically links one audience target to a notification/outbox.
func (s *SQLStore) CreateCampaignAudienceTargetNotification(target *model.CampaignAudienceTarget, n *model.Notification, outbox *model.NotificationOutbox) error {
	if target == nil || n == nil || outbox == nil {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	res, err := tx.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET notify_id = ?, status = 'creating', last_error = NULL, updated_at = ?
		WHERE audience_id = ? AND user_id = ? AND batch_id = ?
		  AND status IN ('pending','retrying','failed')
		  AND (notify_id IS NULL OR notify_id = '')`,
		n.NotifyID, formatTime(now), target.AudienceID, target.UserID, target.BatchID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	if err := insertNotificationTx(tx, n); err != nil {
		return err
	}
	if err := insertOutboxTx(tx, outbox); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), s.insertIgnoreCampaignTargetSQL(),
		target.CampaignID, target.UserID, n.NotifyID, "pending", formatTime(now)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET status = 'created', updated_at = ? WHERE audience_id = ? AND user_id = ? AND notify_id = ?`,
		formatTime(time.Now()), target.AudienceID, target.UserID, n.NotifyID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkCampaignAudienceTargetFailed records a target-level generation failure.
func (s *SQLStore) MarkCampaignAudienceTargetFailed(audienceID, userID, lastError string) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET status = 'failed', last_error = ?, updated_at = ? WHERE audience_id = ? AND user_id = ? AND status <> 'created'`,
		lastError, formatTime(time.Now()), audienceID, userID)
	return err
}

// MarkCampaignAudienceTargetExpired marks a target expired before notification generation.
func (s *SQLStore) MarkCampaignAudienceTargetExpired(audienceID, userID string) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET status = 'expired', last_error = 'ttl_expired', updated_at = ? WHERE audience_id = ? AND user_id = ? AND status <> 'created'`,
		formatTime(time.Now()), audienceID, userID)
	return err
}

// MarkCampaignAudienceBatchResult releases a claimed batch with final counters.
func (s *SQLStore) MarkCampaignAudienceBatchResult(batchID, workerID, status, lastError string, successCount, failedCount int64) error {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(context.Background(), `UPDATE campaign_audience_batches
		SET status = ?, success_count = success_count + ?, failed_count = failed_count + ?,
		    locked_by = NULL, locked_until = NULL, last_error = ?, updated_at = ?
		WHERE batch_id = ? AND status = 'processing' AND locked_by = ?`,
		status, successCount, failedCount, emptyToNil(lastError), now, batchID, workerID)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// RetryCampaignAudienceBatch moves failed batch targets back to pending.
func (s *SQLStore) RetryCampaignAudienceBatch(batchID string) error {
	now := formatTime(time.Now())
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), `UPDATE campaign_audience_targets
		SET status = 'pending', notify_id = NULL, last_error = NULL, updated_at = ? WHERE batch_id = ? AND status IN ('failed','dlq')`, now, batchID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE campaign_audience_batches
		SET status = 'retrying', failed_count = 0, locked_by = NULL, locked_until = NULL, last_error = NULL, updated_at = ? WHERE batch_id = ?`, now, batchID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateScenarioRun persists a scenario run.
func (s *SQLStore) CreateScenarioRun(run *model.ScenarioRun) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO scenario_runs
		(run_id, mode, status, qps, users, generated_orders, generated_notifications, sent_count, acked_count,
		 failed_count, dlq_count, latency_p99_ms, started_at, finished_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.Mode, run.Status, run.QPS, run.Users, run.GeneratedOrders, run.GeneratedNotifications,
		run.SentCount, run.AckedCount, run.FailedCount, run.DLQCount, run.P99LatencyMs, formatTime(run.StartedAt),
		nullableTimePtr(run.FinishedAt), emptyToNil(run.LastError))
	return err
}

// GetScenarioRun returns a scenario run.
func (s *SQLStore) GetScenarioRun(runID string) (*model.ScenarioRun, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT run_id, mode, status, qps, users, generated_orders,
		generated_notifications, sent_count, acked_count, failed_count, dlq_count, latency_p99_ms, started_at, finished_at, last_error
		FROM scenario_runs WHERE run_id = ?`, runID)
	run, err := scanScenarioRun(row)
	if err != nil {
		return nil, err
	}
	events, _ := s.ListScenarioEvents(runID, 20)
	run.RecentEvents = events
	_ = s.hydrateScenarioRunStats(run)
	return run, nil
}

func (s *SQLStore) hydrateScenarioRunStats(run *model.ScenarioRun) error {
	if run == nil || run.RunID == "" {
		return nil
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT a.latency_ms
		FROM notification_acks a
		JOIN notifications n ON n.notify_id = a.notify_id
		WHERE n.scenario_run_id = ?
		ORDER BY a.latency_ms ASC`, run.RunID)
	if err != nil {
		return err
	}
	var latencies []float64
	for rows.Next() {
		var latency float64
		if err := rows.Scan(&latency); err != nil {
			rows.Close()
			return err
		}
		latencies = append(latencies, latency)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Float64s(latencies)
	if len(latencies) > 0 {
		n := len(latencies)
		run.P95LatencyMs = latencies[n*95/100]
		run.P99LatencyMs = latencies[n*99/100]
	}

	var grpcDirect, fallback, offlineStored, failed, logicPush, unknown int64
	attemptRows, err := s.db.QueryContext(context.Background(), `SELECT COALESCE(NULLIF(a.path, ''), a.channel), COUNT(*)
		FROM notification_attempts a
		JOIN notifications n ON n.notify_id = a.notify_id
		WHERE n.scenario_run_id = ?
		GROUP BY COALESCE(NULLIF(a.path, ''), a.channel)`, run.RunID)
	if err != nil {
		return err
	}
	for attemptRows.Next() {
		var path string
		var count int64
		if err := attemptRows.Scan(&path, &count); err != nil {
			attemptRows.Close()
			return err
		}
		switch path {
		case "grpc_direct":
			grpcDirect += count
		case "kafka_fallback":
			fallback += count
		case "offline_stored":
			offlineStored += count
		case "failed":
			failed += count
		case "logic_push":
			logicPush += count
		default:
			unknown += count
		}
	}
	if err := attemptRows.Close(); err != nil {
		return err
	}
	if err := attemptRows.Err(); err != nil {
		return err
	}
	total := grpcDirect + fallback + offlineStored + failed + logicPush + unknown
	run.DeliveryPathDetail = model.DeliveryPathDetail{
		GrpcDirect:    ratio(grpcDirect, total),
		KafkaFallback: ratio(fallback, total),
		OfflineStored: ratio(offlineStored, total),
		Failed:        ratio(failed, total),
		LogicPush:     ratio(logicPush, total),
		Unknown:       ratio(unknown, total),
	}
	return nil
}

// IncrementScenarioRunCounters adds counters to the current scenario run.
func (s *SQLStore) IncrementScenarioRunCounters(runID string, generatedOrders, generatedNotifications, sent, acked, failed, dlq int64) error {
	if runID == "" {
		return nil
	}
	_, err := s.db.ExecContext(context.Background(), `UPDATE scenario_runs SET
		generated_orders = generated_orders + ?,
		generated_notifications = generated_notifications + ?,
		sent_count = sent_count + ?,
		acked_count = acked_count + ?,
		failed_count = failed_count + ?,
		dlq_count = dlq_count + ?
		WHERE run_id = ?`, generatedOrders, generatedNotifications, sent, acked, failed, dlq, runID)
	return err
}

// FinishScenarioRun marks a scenario as stopped or failed.
func (s *SQLStore) FinishScenarioRun(runID, status, lastError string, finishedAt time.Time) error {
	res, err := s.db.ExecContext(context.Background(), `UPDATE scenario_runs SET status = ?, finished_at = ?, last_error = ? WHERE run_id = ?`,
		status, formatTime(finishedAt), emptyToNil(lastError), runID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertScenarioEvent appends an event for a scenario run.
func (s *SQLStore) InsertScenarioEvent(event *model.ScenarioEvent) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO scenario_events
		(event_id, run_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?)`,
		event.EventID, event.RunID, event.Type, event.PayloadJSON, formatTime(event.CreatedAt))
	return err
}

// ListScenarioEvents lists events for a scenario run.
func (s *SQLStore) ListScenarioEvents(runID string, limit int) ([]*model.ScenarioEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT event_id, run_id, type, payload_json, created_at
		FROM scenario_events WHERE run_id = ? ORDER BY created_at ASC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*model.ScenarioEvent
	for rows.Next() {
		var event model.ScenarioEvent
		var created string
		if err := rows.Scan(&event.EventID, &event.RunID, &event.Type, &event.PayloadJSON, &created); err != nil {
			return nil, err
		}
		t, err := parseTime(created)
		if err != nil {
			return nil, err
		}
		event.CreatedAt = t
		events = append(events, &event)
	}
	return events, rows.Err()
}

// FirstAttemptStart returns the first attempt start time for latency calculation.
func (s *SQLStore) FirstAttemptStart(notifyID string) (time.Time, bool, error) {
	var started string
	err := s.db.QueryRowContext(context.Background(), `SELECT started_at FROM notification_attempts
		WHERE notify_id = ? ORDER BY started_at ASC LIMIT 1`, notifyID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := parseTime(started)
	return t, true, err
}

// PlatformStats aggregates persisted notification metrics.
func (s *SQLStore) PlatformStats(activeConns, onlineUsers, offlinePending int64) (model.PlatformStats, error) {
	var total, acked int64
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notifications`).Scan(&total); err != nil {
		return model.PlatformStats{}, err
	}
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notifications WHERE business_ack_status = 'satisfied' OR status = 'acked'`).Scan(&acked); err != nil {
		return model.PlatformStats{}, err
	}
	if offlinePending == 0 {
		if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notifications WHERE business_ack_status <> 'satisfied' AND status <> 'acked'`).Scan(&offlinePending); err != nil {
			return model.PlatformStats{}, err
		}
	}

	rows, err := s.db.QueryContext(context.Background(), `SELECT latency_ms FROM notification_acks ORDER BY latency_ms ASC`)
	if err != nil {
		return model.PlatformStats{}, err
	}
	defer rows.Close()
	var latencies []float64
	for rows.Next() {
		var latency float64
		if err := rows.Scan(&latency); err != nil {
			return model.PlatformStats{}, err
		}
		latencies = append(latencies, latency)
	}
	if err := rows.Err(); err != nil {
		return model.PlatformStats{}, err
	}
	sort.Float64s(latencies)

	var grpcDirect, fallback, offlineStored, failed, logicPush, unknown int64
	attemptRows, err := s.db.QueryContext(context.Background(), `SELECT COALESCE(NULLIF(path, ''), channel), COUNT(*) FROM notification_attempts GROUP BY COALESCE(NULLIF(path, ''), channel)`)
	if err != nil {
		return model.PlatformStats{}, err
	}
	defer attemptRows.Close()
	for attemptRows.Next() {
		var channel string
		var count int64
		if err := attemptRows.Scan(&channel, &count); err != nil {
			return model.PlatformStats{}, err
		}
		if channel == "grpc_direct" {
			grpcDirect += count
		} else if channel == "kafka_fallback" {
			fallback += count
		} else if channel == "offline_stored" {
			offlineStored += count
		} else if channel == "failed" {
			failed += count
		} else if channel == "logic_push" {
			logicPush += count
		} else {
			unknown += count
		}
	}
	if err := attemptRows.Err(); err != nil {
		return model.PlatformStats{}, err
	}

	var recent int64
	_ = s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notifications WHERE created_at >= ?`, formatTime(time.Now().Add(-60*time.Second))).Scan(&recent)
	var retryCount, outboxPending, outboxFailed, dlqCount int64
	_ = s.db.QueryRowContext(context.Background(), `SELECT COALESCE(SUM(retry_count), 0) FROM notification_outbox`).Scan(&retryCount)
	_ = s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notification_outbox WHERE status = 'pending'`).Scan(&outboxPending)
	_ = s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notification_outbox WHERE status = 'failed'`).Scan(&outboxFailed)
	_ = s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notification_dlq WHERE resolved_at IS NULL OR resolved_at = ''`).Scan(&dlqCount)
	var oldestDLQ sql.NullString
	_ = s.db.QueryRowContext(context.Background(), `SELECT MIN(created_at) FROM notification_dlq WHERE resolved_at IS NULL OR resolved_at = ''`).Scan(&oldestDLQ)
	var oldestAge int64
	if oldestDLQ.Valid && oldestDLQ.String != "" {
		if t, err := parseTime(oldestDLQ.String); err == nil {
			oldestAge = int64(time.Since(t).Seconds())
		}
	}
	byType := make(map[string]int64)
	typeRows, err := s.db.QueryContext(context.Background(), `SELECT type, COUNT(*) FROM notifications GROUP BY type`)
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var typ string
			var count int64
			if err := typeRows.Scan(&typ, &count); err == nil {
				byType[typ] = count
			}
		}
	}
	detailTotal := grpcDirect + fallback + offlineStored + failed + logicPush + unknown
	stats := model.PlatformStats{
		PushRatePerSec: float64(recent) / 60.0,
		TotalPushed:    total,
		AckRate:        ratio(acked, total),
		ActiveConns:    activeConns,
		DeliveryPath: model.DeliveryPathRatio{
			GrpcDirect:    ratio(grpcDirect, grpcDirect+fallback),
			KafkaFallback: ratio(fallback, grpcDirect+fallback),
		},
		DeliveryPathDetail: model.DeliveryPathDetail{
			GrpcDirect:    ratio(grpcDirect, detailTotal),
			KafkaFallback: ratio(fallback, detailTotal),
			OfflineStored: ratio(offlineStored, detailTotal),
			Failed:        ratio(failed, detailTotal),
			LogicPush:     ratio(logicPush, detailTotal),
			Unknown:       ratio(unknown, detailTotal),
		},
		OnlineUsers:            onlineUsers,
		OfflinePending:         offlinePending,
		RetryCount:             retryCount,
		DLQCount:               dlqCount,
		OldestDLQAgeSeconds:    oldestAge,
		OutboxPending:          outboxPending,
		OutboxFailed:           outboxFailed,
		NotificationsByType:    byType,
		AckPolicySatisfiedRate: ratio(acked, total),
	}
	if len(latencies) > 0 {
		n := len(latencies)
		stats.LatencyP50Ms = latencies[n*50/100]
		stats.LatencyP95Ms = latencies[n*95/100]
		stats.LatencyP99Ms = latencies[n*99/100]
		stats.LatencyMaxMs = latencies[n-1]
	}
	return stats, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAttempt(row scanner) (*model.NotificationAttempt, error) {
	var a model.NotificationAttempt
	var startedAt, finishedAt string
	if err := row.Scan(&a.AttemptID, &a.NotifyID, &a.Channel, &a.Target, &a.Status,
		&a.Path, &a.TargetNode, &a.ErrorCode, &a.ErrorMessage,
		&a.LatencyMs, &a.AttemptNo, &a.TraceID, &startedAt, &finishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	started, err := parseTime(startedAt)
	if err != nil {
		return nil, err
	}
	a.StartedAt = started
	if finishedAt != "" {
		ft, err := parseTime(finishedAt)
		if err == nil {
			a.FinishedAt = &ft
		}
	}
	return &a, nil
}

func scanOutbox(row scanner) (*model.NotificationOutbox, error) {
	var o model.NotificationOutbox
	var nextRetryAt, lockedUntil, createdAt, updatedAt sql.NullString
	var lockedBy, lastError sql.NullString
	if err := row.Scan(&o.OutboxID, &o.NotifyID, &o.UserID, &o.OrderID, &o.BusinessType, &o.EventType,
		&o.PayloadJSON, &o.Priority, &o.TTLSeconds, &o.Status, &o.RetryCount, &nextRetryAt,
		&lockedBy, &lockedUntil, &lastError, &o.ScenarioRunID, &o.TraceID, &o.CompensationStrategy, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	o.NextRetryAt = parseNullTime(nextRetryAt)
	o.LockedBy = lockedBy.String
	o.LockedUntil = parseNullTime(lockedUntil)
	o.LastError = lastError.String
	o.CreatedAt = parseNullTime(createdAt)
	o.UpdatedAt = parseNullTime(updatedAt)
	return &o, nil
}

func scanDLQ(row scanner) (*model.NotificationDLQ, error) {
	var d model.NotificationDLQ
	var createdAt string
	var resolvedAt, resolvedBy, resolution sql.NullString
	if err := row.Scan(&d.DLQID, &d.NotifyID, &d.OutboxID, &d.UserID, &d.OrderID, &d.Reason,
		&d.LastError, &d.PayloadJSON, &d.RetryCount, &d.BusinessType, &d.CompensationStrategy, &d.TraceID, &createdAt, &resolvedAt, &resolvedBy, &resolution); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t, err := parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	d.CreatedAt = t
	if resolvedAt.Valid && resolvedAt.String != "" {
		rt, err := parseTime(resolvedAt.String)
		if err != nil {
			return nil, err
		}
		d.ResolvedAt = &rt
	}
	d.ResolvedBy = resolvedBy.String
	d.Resolution = resolution.String
	return &d, nil
}

func scanScenarioRun(row scanner) (*model.ScenarioRun, error) {
	var run model.ScenarioRun
	var started string
	var finished, lastError sql.NullString
	if err := row.Scan(&run.RunID, &run.Mode, &run.Status, &run.QPS, &run.Users, &run.GeneratedOrders,
		&run.GeneratedNotifications, &run.SentCount, &run.AckedCount, &run.FailedCount, &run.DLQCount,
		&run.P99LatencyMs, &started, &finished, &lastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t, err := parseTime(started)
	if err != nil {
		return nil, err
	}
	run.StartedAt = t
	if finished.Valid && finished.String != "" {
		ft, err := parseTime(finished.String)
		if err != nil {
			return nil, err
		}
		run.FinishedAt = &ft
	}
	run.LastError = lastError.String
	return &run, nil
}

func scanOrder(row scanner) (*model.Order, error) {
	var order model.Order
	var status, itemsJSON, createdAt, updatedAt string
	if err := row.Scan(&order.OrderID, &order.UserID, &status, &itemsJSON, &order.Total, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(itemsJSON), &order.Items); err != nil {
		return nil, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	order.Status = model.OrderStatus(status)
	order.CreatedAt = created
	order.UpdatedAt = updated
	return &order, nil
}

func scanNotification(row scanner) (*model.Notification, error) {
	var n model.Notification
	var typ, targetDevicesJSON, createdAt, updatedAt string
	if err := row.Scan(&n.NotifyID, &n.UserID, &typ, &n.BusinessType, &n.EventType, &n.Title, &n.Content, &n.OrderID, &n.Status,
		&n.Priority, &n.TTLSeconds, &n.AckPolicy, &n.ExpectedAckCount, &n.AckedCount, &n.BusinessAckStatus,
		&targetDevicesJSON, &n.PrimaryDeviceID, &n.ScenarioRunID, &n.IdempotencyKey, &n.TraceID, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	n.Type = model.NotifyType(typ)
	if targetDevicesJSON != "" {
		_ = json.Unmarshal([]byte(targetDevicesJSON), &n.TargetDeviceIDs)
	}
	n.CreatedAt = created
	n.UpdatedAt = updated
	return &n, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(v string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
	}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.Parse(layout, v)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	for _, layout := range layouts[1:] {
		t, err := time.ParseInLocation(layout, v, time.Local)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func parseNullTime(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, _ := parseTime(v.String)
	return t
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

func nullableTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

func emptyToNil(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nonZeroInt(v, fallback int64) int64 {
	if v == 0 {
		return fallback
	}
	return v
}

func normalizeNotificationAckDefaults(n *model.Notification) {
	if n.AckPolicy == "" {
		n.AckPolicy = "any_device"
	}
	if len(n.TargetDeviceIDs) > 0 {
		n.ExpectedAckCount = int64(len(n.TargetDeviceIDs))
	}
	if n.ExpectedAckCount <= 0 && n.AckPolicy != "none" && n.AckPolicy != "best_effort" {
		n.ExpectedAckCount = 1
	}
	if n.AckPolicy == "none" || n.AckPolicy == "best_effort" {
		n.BusinessAckStatus = "satisfied"
	}
	if n.BusinessAckStatus == "" {
		n.BusinessAckStatus = "pending"
	}
}

func normalizeAckKey(ack *model.NotificationAck) string {
	if ack.AckKey != "" {
		return ack.AckKey
	}
	if ack.DeviceID != "" || ack.SessionID != "" {
		return ack.MsgID + "|" + ack.DeviceID + "|" + ack.SessionID
	}
	if ack.MsgID != "" {
		return ack.MsgID
	}
	return "legacy"
}

func ackPolicySatisfied(policyName, ackDeviceID, primaryDeviceID string, targetDevices []string, acked, expected int64) bool {
	switch policyName {
	case "none", "best_effort":
		return true
	case "primary_device":
		if primaryDeviceID == "" {
			return acked > 0
		}
		return ackDeviceID == primaryDeviceID
	case "all_devices":
		if len(targetDevices) > 0 {
			return acked >= int64(len(targetDevices))
		}
		return expected > 0 && acked >= expected
	case "any_device", "":
		return acked > 0
	default:
		return expected > 0 && acked >= expected
	}
}

func stringInSlice(v string, values []string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

func ackSnapshot(recorded bool, fallback []byte) []byte {
	if len(fallback) > 0 {
		return fallback
	}
	b, _ := json.Marshal(struct {
		Recorded bool `json:"recorded"`
	}{Recorded: recorded})
	return b
}

func emptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func emptyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func ratio(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Phase 3 store methods are in store_phase3.go (GetCampaign, PauseCampaign, etc.)

// ============================================================================
// Operator / Audit / Trace / Timeline / SLA (Phase 1-2 bridge)
// ============================================================================

// GetNotificationTrace returns the full operator trace for one notification.
func (s *SQLStore) GetNotificationTrace(notifyID string) (*model.NotificationTrace, error) {
	n, err := s.GetNotification(notifyID)
	if err != nil {
		return nil, err
	}
	outbox, _ := s.getOutboxByNotifyID(notifyID)
	attempts, _ := s.ListAttemptsByNotifyID(notifyID)
	acks, _ := s.ListACKsByNotifyID(notifyID)
	deliveryPath := "unknown"
	for _, a := range attempts {
		if a.Path != "" && a.Path != "unknown" {
			deliveryPath = a.Path
			break
		}
	}
	retryCount := int64(0)
	if outbox != nil {
		retryCount = outbox.RetryCount
	}
	satisfied := false
	var satisfiedAt *time.Time
	if n.AckPolicy == "none" || n.AckPolicy == "best_effort" {
		satisfied = true
	} else if n.AckedCount >= n.ExpectedAckCount && n.ExpectedAckCount > 0 {
		satisfied = true
	}
	for _, ack := range acks {
		if ack.PolicySatisfiedAt != nil {
			satisfiedAt = ack.PolicySatisfiedAt
		}
	}
	return &model.NotificationTrace{
		Notification: n,
		TraceID:      n.TraceID,
		BusinessRef:  model.BusinessRef{Type: "order", ID: n.OrderID},
		Outbox:       outbox,
		Attempts:     attempts,
		DeliveryPath: deliveryPath,
		RetryCount:   retryCount,
		ACKs:         acks,
		ACKPolicyStatus: model.AckPolicyStatus{
			Policy:            n.AckPolicy,
			Status:            n.BusinessAckStatus,
			ExpectedAckCount:  n.ExpectedAckCount,
			AckedCount:        n.AckedCount,
			Satisfied:         satisfied,
			PolicySatisfiedAt: satisfiedAt,
		},
	}, nil
}

func (s *SQLStore) getOutboxByNotifyID(notifyID string) (*model.NotificationOutbox, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT outbox_id, notify_id, user_id, COALESCE(order_id, ''),
		business_type, event_type, payload_json, priority, ttl_seconds, status, retry_count,
		COALESCE(next_retry_at, ''), COALESCE(locked_by, ''), COALESCE(locked_until, ''),
		COALESCE(last_error, ''), COALESCE(scenario_run_id, ''), COALESCE(trace_id, notify_id), COALESCE(compensation_strategy, 'retry_then_dlq'), created_at, updated_at
		FROM notification_outbox WHERE notify_id = ? LIMIT 1`, notifyID)
	o, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

// GetOrderTimeline returns the full timeline for an order.
func (s *SQLStore) GetOrderTimeline(orderID string) (*model.OrderTimeline, error) {
	order, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	events, _ := s.ListOrderStatusEvents(orderID)
	notifs, _ := s.ListNotificationsByOrderID(orderID)
	var attempts []*model.NotificationAttempt
	var acks []*model.NotificationAck
	var dlqs []*model.NotificationDLQ
	for _, n := range notifs {
		if a, _ := s.ListAttemptsByNotifyID(n.NotifyID); a != nil {
			attempts = append(attempts, a...)
		}
		if ac, _ := s.ListACKsByNotifyID(n.NotifyID); ac != nil {
			acks = append(acks, ac...)
		}
	}
	timeline := buildTimeline(events, notifs, attempts, acks)
	return &model.OrderTimeline{
		Order:         order,
		StatusEvents:  events,
		Notifications: notifs,
		Attempts:      attempts,
		ACKs:          acks,
		DLQEvents:     dlqs,
		Timeline:      timeline,
	}, nil
}

func buildTimeline(events []*model.OrderStatusEvent, notifs []*model.Notification, attempts []*model.NotificationAttempt, acks []*model.NotificationAck) []model.TimelineEvent {
	var t []model.TimelineEvent
	for _, ev := range events {
		t = append(t, model.TimelineEvent{
			ID: ev.EventID, Type: "status_change", Label: string(ev.FromStatus) + " to " + string(ev.ToStatus),
			OrderID: ev.OrderID, OccurredAt: ev.CreatedAt,
		})
	}
	for _, n := range notifs {
		t = append(t, model.TimelineEvent{
			ID: n.NotifyID, Type: "notification", Label: n.Title, NotifyID: n.NotifyID,
			OrderID: n.OrderID, Status: n.Status, BusinessType: n.BusinessType, TraceID: n.TraceID, OccurredAt: n.CreatedAt,
		})
	}
	for _, a := range attempts {
		t = append(t, model.TimelineEvent{
			ID: a.AttemptID, Type: "attempt", Label: a.Channel + ":" + a.Status, NotifyID: a.NotifyID,
			Status: a.Status, DeliveryPath: a.Path, TraceID: a.TraceID, OccurredAt: a.StartedAt,
		})
	}
	for _, ack := range acks {
		t = append(t, model.TimelineEvent{
			ID: ack.AckID, Type: "ack", Label: "acked by " + ack.DeviceID,
			NotifyID: ack.NotifyID, TraceID: ack.TraceID, OccurredAt: ack.CreatedAt,
		})
	}
	sort.Slice(t, func(i, j int) bool { return t[i].OccurredAt.Before(t[j].OccurredAt) })
	return t
}

// BusinessSLA computes SLA metrics for a time window.
func (s *SQLStore) BusinessSLA(since, until time.Time, businessType, deliveryPath string) (model.BusinessSLAMetrics, error) {
	sinceS, untilS := formatTime(since), formatTime(until)
	m := model.BusinessSLAMetrics{WindowSeconds: int64(until.Sub(since).Seconds()), Since: since, Until: until}

	notificationWhere := []string{"n.created_at >= ?", "n.created_at < ?"}
	notificationArgs := []any{sinceS, untilS}
	if businessType != "" {
		notificationWhere = append(notificationWhere, "n.business_type = ?")
		notificationArgs = append(notificationArgs, businessType)
	}
	if deliveryPath != "" {
		notificationWhere = append(notificationWhere, `EXISTS (
			SELECT 1 FROM notification_attempts a
			WHERE a.notify_id = n.notify_id AND a.path = ?
		)`)
		notificationArgs = append(notificationArgs, deliveryPath)
	}
	notificationFilter := strings.Join(notificationWhere, " AND ")

	total, _ := s.countWhere(`SELECT COUNT(*) FROM notifications n WHERE `+notificationFilter, notificationArgs...)
	m.TotalNotifications = total

	successArgs := append([]any(nil), notificationArgs...)
	successful, _ := s.countWhere(`SELECT COUNT(*) FROM notifications n WHERE `+notificationFilter+` AND n.status IN ('delivered','acked')`, successArgs...)
	m.SuccessfulNotifications = successful
	if total > 0 {
		m.NotificationSuccessRate = float64(successful) / float64(total)
	}

	ackSatisfied, _ := s.countWhere(`SELECT COUNT(*) FROM notifications n WHERE `+notificationFilter+` AND n.business_ack_status = 'satisfied'`, notificationArgs...)
	m.ACKSatisfiedCount = ackSatisfied
	if total > 0 {
		m.ACKSatisfactionRate = float64(ackSatisfied) / float64(total)
	}

	dlqWhere := []string{"d.created_at >= ?", "d.created_at < ?"}
	dlqArgs := []any{sinceS, untilS}
	if businessType != "" {
		dlqWhere = append(dlqWhere, "d.business_type = ?")
		dlqArgs = append(dlqArgs, businessType)
	}
	dlq, _ := s.countWhere(`SELECT COUNT(*) FROM notification_dlq d WHERE `+strings.Join(dlqWhere, " AND "), dlqArgs...)
	m.DLQCount = dlq
	if total > 0 {
		m.DLQRate = float64(dlq) / float64(total)
	}

	attemptWhere := []string{"a.started_at >= ?", "a.started_at < ?"}
	attemptArgs := []any{sinceS, untilS}
	if businessType != "" {
		attemptWhere = append(attemptWhere, "n.business_type = ?")
		attemptArgs = append(attemptArgs, businessType)
	}
	if deliveryPath != "" {
		attemptWhere = append(attemptWhere, "a.path = ?")
		attemptArgs = append(attemptArgs, deliveryPath)
	}
	attemptFilter := strings.Join(attemptWhere, " AND ")
	retried, _ := s.countWhere(`SELECT COUNT(*) FROM notification_attempts a JOIN notifications n ON n.notify_id = a.notify_id WHERE `+attemptFilter+` AND a.attempt_no > 1`, attemptArgs...)
	m.RetriedNotifications = retried
	if total > 0 {
		m.RetryRate = float64(retried) / float64(total)
	}

	deliveryLatencies, err := s.float64Column(`SELECT a.latency_ms
		FROM notification_attempts a JOIN notifications n ON n.notify_id = a.notify_id
		WHERE `+attemptFilter+` AND a.latency_ms > 0
		ORDER BY a.latency_ms ASC`, attemptArgs...)
	if err != nil {
		return m, err
	}
	m.DeliveryLatencyP95Ms = percentile(deliveryLatencies, 0.95)
	m.DeliveryLatencyP99Ms = percentile(deliveryLatencies, 0.99)

	ackWhere := []string{"ack.created_at >= ?", "ack.created_at < ?"}
	ackArgs := []any{sinceS, untilS}
	if businessType != "" {
		ackWhere = append(ackWhere, "n.business_type = ?")
		ackArgs = append(ackArgs, businessType)
	}
	if deliveryPath != "" {
		ackWhere = append(ackWhere, `EXISTS (
			SELECT 1 FROM notification_attempts a
			WHERE a.notify_id = ack.notify_id AND a.path = ?
		)`)
		ackArgs = append(ackArgs, deliveryPath)
	}
	ackLatencies, err := s.float64Column(`SELECT ack.latency_ms
		FROM notification_acks ack JOIN notifications n ON n.notify_id = ack.notify_id
		WHERE `+strings.Join(ackWhere, " AND ")+` AND ack.latency_ms > 0
		ORDER BY ack.latency_ms ASC`, ackArgs...)
	if err != nil {
		return m, err
	}
	m.ACKLatencyP95Ms = percentile(ackLatencies, 0.95)
	m.ACKLatencyP99Ms = percentile(ackLatencies, 0.99)

	return m, nil
}

// ReplayDLQWithNote replays a DLQ item and writes an audit record.
func (s *SQLStore) ReplayDLQWithNote(id, resolvedBy, note string) (*model.NotificationDLQ, error) {
	dlq, err := s.GetDLQ(id)
	if err != nil {
		return nil, err
	}
	if err := s.ReplayDLQ(id, resolvedBy); err != nil {
		return nil, err
	}
	_ = s.insertRecoveryAudit(dlq.DLQID, dlq.NotifyID, dlq.OutboxID, "replay", resolvedBy, dlq.BusinessType, "dlq", "pending", note)
	return dlq, nil
}

// ResolveDLQWithNote resolves a DLQ item and writes an audit record.
func (s *SQLStore) ResolveDLQWithNote(id, resolvedBy, resolution, note string) (*model.NotificationDLQ, error) {
	dlq, err := s.GetDLQ(id)
	if err != nil {
		return nil, err
	}
	if err := s.ResolveDLQ(id, resolvedBy, resolution); err != nil {
		return nil, err
	}
	_ = s.insertRecoveryAudit(dlq.DLQID, dlq.NotifyID, dlq.OutboxID, "resolve", resolvedBy, dlq.BusinessType, "dlq", "resolved", note)
	return dlq, nil
}

func (s *SQLStore) insertRecoveryAudit(dlqID, notifyID, outboxID, action, operator, businessType, beforeStatus, afterStatus, note string) error {
	auditID := fmt.Sprintf("aud_%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO notification_recovery_audit
		(audit_id, action, operator, dlq_id, notify_id, outbox_id, business_type, reason, resolution, note, before_status, after_status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		auditID, action, operator, dlqID, notifyID, outboxID, businessType, "", "", note, beforeStatus, afterStatus, formatTime(time.Now()))
	return err
}

// LatestRecoveryAuditOperator returns the operator of the most recent recovery audit.
func (s *SQLStore) LatestRecoveryAuditOperator() (string, error) {
	var operator string
	err := s.db.QueryRowContext(context.Background(), `SELECT operator FROM notification_recovery_audit ORDER BY created_at DESC LIMIT 1`).Scan(&operator)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return operator, err
}

// BulkReplayDLQ replays multiple DLQ items matching a filter.
func (s *SQLStore) BulkReplayDLQ(filter model.DLQBulkFilter, user, note string) (model.DLQBulkResult, error) {
	items, err := s.ListDLQFiltered(filter)
	if err != nil {
		return model.DLQBulkResult{}, err
	}
	result := model.DLQBulkResult{Matched: int64(len(items))}
	for _, item := range items {
		if item.ResolvedAt != nil {
			result.Skipped++
			continue
		}
		if err := s.ReplayDLQ(item.DLQID, user); err != nil {
			result.Skipped++
			continue
		}
		_ = s.insertRecoveryAudit(item.DLQID, item.NotifyID, item.OutboxID, "replay", user, item.BusinessType, "dlq", "pending", note)
		result.Replayed++
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// BulkResolveDLQ resolves multiple DLQ items matching a filter.
func (s *SQLStore) BulkResolveDLQ(filter model.DLQBulkFilter, user, resolution, note string) (model.DLQBulkResult, error) {
	items, err := s.ListDLQFiltered(filter)
	if err != nil {
		return model.DLQBulkResult{}, err
	}
	result := model.DLQBulkResult{Matched: int64(len(items))}
	for _, item := range items {
		if item.ResolvedAt != nil {
			result.Skipped++
			continue
		}
		if err := s.ResolveDLQ(item.DLQID, user, resolution); err != nil {
			result.Skipped++
			continue
		}
		_ = s.insertRecoveryAudit(item.DLQID, item.NotifyID, item.OutboxID, "resolve", user, item.BusinessType, "dlq", "resolved", note)
		result.Resolved++
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// BulkReplayDLQThrottled replays matching DLQ items with optional rate limiting.
func (s *SQLStore) BulkReplayDLQThrottled(filter model.DLQBulkFilter, user, note string, throttlePerSec int) (model.DLQBulkResult, error) {
	return s.bulkDLQThrottled(filter, user, "", note, throttlePerSec, "replay")
}

// BulkResolveDLQThrottled resolves matching DLQ items with optional rate limiting.
func (s *SQLStore) BulkResolveDLQThrottled(filter model.DLQBulkFilter, user, resolution, note string, throttlePerSec int) (model.DLQBulkResult, error) {
	return s.bulkDLQThrottled(filter, user, resolution, note, throttlePerSec, "resolve")
}

func (s *SQLStore) bulkDLQThrottled(filter model.DLQBulkFilter, user, resolution, note string, throttlePerSec int, action string) (model.DLQBulkResult, error) {
	items, err := s.ListDLQFiltered(filter)
	if err != nil {
		return model.DLQBulkResult{}, err
	}
	result := model.DLQBulkResult{Matched: int64(len(items))}
	var delay time.Duration
	if throttlePerSec > 0 {
		delay = time.Second / time.Duration(throttlePerSec)
	}
	for i, item := range items {
		if item.ResolvedAt != nil {
			result.Skipped++
			continue
		}
		if action == "resolve" {
			if err := s.ResolveDLQ(item.DLQID, user, resolution); err != nil {
				result.Skipped++
				continue
			}
			_ = s.insertRecoveryAudit(item.DLQID, item.NotifyID, item.OutboxID, "resolve", user, item.BusinessType, "dlq", "resolved", note)
			result.Resolved++
		} else {
			if err := s.ReplayDLQ(item.DLQID, user); err != nil {
				result.Skipped++
				continue
			}
			_ = s.insertRecoveryAudit(item.DLQID, item.NotifyID, item.OutboxID, "replay", user, item.BusinessType, "dlq", "pending", note)
			result.Replayed++
		}
		result.Items = append(result.Items, item)
		if delay > 0 && i < len(items)-1 {
			time.Sleep(delay)
		}
	}
	return result, nil
}

// CreateReplayApprovalRequest stores a pending bulk recovery approval request.
func (s *SQLStore) CreateReplayApprovalRequest(req *model.ReplayApprovalRequest) error {
	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("rpr_%d", time.Now().UnixNano())
	}
	if req.Status == "" {
		req.Status = model.ReplayRequestPending
	}
	now := time.Now()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	filterJSON, err := json.Marshal(req.Filter)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(context.Background(), `INSERT INTO notification_replay_requests
		(request_id, action, status, operator, approver, filter_json, matched_count, threshold, resolution, note, throttle_per_sec, result_json, created_at, updated_at, decided_at, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.RequestID, req.Action, req.Status, req.Operator, emptyToNil(req.Approver), string(filterJSON), req.MatchedCount, req.Threshold,
		emptyToNil(req.Resolution), emptyToNil(req.Note), req.ThrottlePerSec, nil, formatTime(req.CreatedAt), formatTime(req.UpdatedAt), nullableTimePtr(req.DecidedAt), nullableTimePtr(req.ExecutedAt))
	return err
}

// GetReplayApprovalRequest returns one approval request.
func (s *SQLStore) GetReplayApprovalRequest(id string) (*model.ReplayApprovalRequest, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT request_id, action, status, operator, COALESCE(approver, ''), filter_json,
		matched_count, threshold, COALESCE(resolution, ''), COALESCE(note, ''), throttle_per_sec, COALESCE(result_json, ''),
		created_at, updated_at, COALESCE(decided_at, ''), COALESCE(executed_at, '')
		FROM notification_replay_requests WHERE request_id = ?`, id)
	return scanReplayApprovalRequest(row)
}

// ListReplayApprovalRequests returns recent approval requests.
func (s *SQLStore) ListReplayApprovalRequests(status string, limit int) ([]*model.ReplayApprovalRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT request_id, action, status, operator, COALESCE(approver, ''), filter_json,
		matched_count, threshold, COALESCE(resolution, ''), COALESCE(note, ''), throttle_per_sec, COALESCE(result_json, ''),
		created_at, updated_at, COALESCE(decided_at, ''), COALESCE(executed_at, '')
		FROM notification_replay_requests`
	var args []any
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []*model.ReplayApprovalRequest
	for rows.Next() {
		req, err := scanReplayApprovalRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, req)
	}
	return requests, rows.Err()
}

// UpdateReplayApprovalStatus moves a request through its approval states.
func (s *SQLStore) UpdateReplayApprovalStatus(id, status, approver, note string) (*model.ReplayApprovalRequest, error) {
	now := time.Now()
	_, err := s.db.ExecContext(context.Background(), `UPDATE notification_replay_requests
		SET status = ?, approver = ?, note = COALESCE(NULLIF(?, ''), note), decided_at = ?, updated_at = ?
		WHERE request_id = ? AND status = ?`,
		status, emptyToNil(approver), note, formatTime(now), formatTime(now), id, model.ReplayRequestPending)
	if err != nil {
		return nil, err
	}
	return s.GetReplayApprovalRequest(id)
}

// ExecuteReplayApprovalRequest executes an approved request and records the result.
func (s *SQLStore) ExecuteReplayApprovalRequest(id, operator string) (*model.ReplayApprovalRequest, error) {
	req, err := s.GetReplayApprovalRequest(id)
	if err != nil {
		return nil, err
	}
	if req.Status != model.ReplayRequestApproved {
		return nil, fmt.Errorf("replay request must be approved before execution")
	}
	if operator == "" {
		operator = req.Operator
	}
	var result model.DLQBulkResult
	if req.Action == "resolve" {
		result, err = s.BulkResolveDLQThrottled(req.Filter, operator, req.Resolution, req.Note, req.ThrottlePerSec)
	} else {
		result, err = s.BulkReplayDLQThrottled(req.Filter, operator, req.Note, req.ThrottlePerSec)
	}
	if err != nil {
		return nil, err
	}
	resultJSON, _ := json.Marshal(result)
	now := time.Now()
	_, err = s.db.ExecContext(context.Background(), `UPDATE notification_replay_requests
		SET status = ?, result_json = ?, executed_at = ?, updated_at = ? WHERE request_id = ?`,
		model.ReplayRequestExecuted, string(resultJSON), formatTime(now), formatTime(now), id)
	if err != nil {
		return nil, err
	}
	return s.GetReplayApprovalRequest(id)
}

// ListDLQFiltered returns DLQ rows matching a filter.
func (s *SQLStore) ListDLQFiltered(filter model.DLQBulkFilter) ([]*model.NotificationDLQ, error) {
	query := `SELECT dlq_id, notify_id, outbox_id, user_id, COALESCE(order_id, ''), reason, last_error, payload_json, retry_count, COALESCE(business_type, ''), COALESCE(compensation_strategy, 'retry_then_dlq'), COALESCE(trace_id, notify_id), created_at, COALESCE(resolved_at, ''), COALESCE(resolved_by, ''), COALESCE(resolution, '') FROM notification_dlq WHERE 1=1`
	var args []interface{}
	if filter.Reason != "" {
		query += " AND reason = ?"
		args = append(args, filter.Reason)
	}
	if filter.BusinessType != "" {
		query += " AND business_type = ?"
		args = append(args, filter.BusinessType)
	}
	if filter.Resolved == "true" {
		query += " AND resolved_at IS NOT NULL"
	} else if filter.Resolved == "false" {
		query += " AND resolved_at IS NULL"
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*model.NotificationDLQ
	for rows.Next() {
		dlq, err := scanDLQ(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, dlq)
	}
	return items, rows.Err()
}

// ListRecoveryAuditsByDLQ returns audits for a specific DLQ item.
func (s *SQLStore) ListRecoveryAuditsByDLQ(dlqID string) ([]*model.NotificationRecoveryAudit, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT audit_id, action, operator, dlq_id, notify_id, outbox_id, COALESCE(business_type, ''), COALESCE(reason, ''), COALESCE(resolution, ''), COALESCE(note, ''), COALESCE(before_status, ''), COALESCE(after_status, ''), created_at FROM notification_recovery_audit WHERE dlq_id = ? ORDER BY created_at DESC`, dlqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var audits []*model.NotificationRecoveryAudit
	for rows.Next() {
		audit, err := scanRecoveryAudit(rows)
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}

// ListRecoveryAudits returns audits matching a filter.
func (s *SQLStore) ListRecoveryAudits(filter model.RecoveryAuditFilter) ([]*model.NotificationRecoveryAudit, error) {
	query := `SELECT audit_id, action, operator, dlq_id, notify_id, outbox_id, COALESCE(business_type, ''), COALESCE(reason, ''), COALESCE(resolution, ''), COALESCE(note, ''), COALESCE(before_status, ''), COALESCE(after_status, ''), created_at FROM notification_recovery_audit WHERE 1=1`
	var args []interface{}
	if filter.Operator != "" {
		query += " AND operator = ?"
		args = append(args, filter.Operator)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}
	if filter.BusinessType != "" {
		query += " AND business_type = ?"
		args = append(args, filter.BusinessType)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var audits []*model.NotificationRecoveryAudit
	for rows.Next() {
		audit, err := scanRecoveryAudit(rows)
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}

func scanRecoveryAudit(row scanner) (*model.NotificationRecoveryAudit, error) {
	var a model.NotificationRecoveryAudit
	var created string
	if err := row.Scan(&a.AuditID, &a.Action, &a.Operator, &a.DLQID, &a.NotifyID, &a.OutboxID, &a.BusinessType, &a.Reason, &a.Resolution, &a.Note, &a.BeforeStatus, &a.AfterStatus, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t, err := parseTime(created)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = t
	return &a, nil
}

func scanReplayApprovalRequest(row scanner) (*model.ReplayApprovalRequest, error) {
	var req model.ReplayApprovalRequest
	var filterJSON, resultJSON, created, updated, decided, executed string
	if err := row.Scan(&req.RequestID, &req.Action, &req.Status, &req.Operator, &req.Approver, &filterJSON,
		&req.MatchedCount, &req.Threshold, &req.Resolution, &req.Note, &req.ThrottlePerSec, &resultJSON,
		&created, &updated, &decided, &executed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(filterJSON), &req.Filter)
	if resultJSON != "" {
		var result model.DLQBulkResult
		if json.Unmarshal([]byte(resultJSON), &result) == nil {
			req.ExecutionResult = &result
		}
	}
	req.CreatedAt, _ = parseTime(created)
	req.UpdatedAt, _ = parseTime(updated)
	if decided != "" {
		t, _ := parseTime(decided)
		req.DecidedAt = &t
	}
	if executed != "" {
		t, _ := parseTime(executed)
		req.ExecutedAt = &t
	}
	return &req, nil
}

func scanCampaignAudienceTarget(row scanner) (*model.CampaignAudienceTarget, error) {
	var target model.CampaignAudienceTarget
	var created, updated string
	if err := row.Scan(&target.AudienceID, &target.CampaignID, &target.UserID, &target.BatchID, &target.NotifyID, &target.Status, &created, &updated); err != nil {
		return nil, err
	}
	target.CreatedAt, _ = parseTime(created)
	target.UpdatedAt, _ = parseTime(updated)
	return &target, nil
}

func scanCampaignAudienceBatch(row scanner) (*model.CampaignAudienceBatch, error) {
	var batch model.CampaignAudienceBatch
	var created, updated string
	if err := row.Scan(&batch.BatchID, &batch.AudienceID, &batch.CampaignID, &batch.Status, &batch.StartOffset,
		&batch.EndOffset, &batch.TargetCount, &batch.SuccessCount, &batch.FailedCount, &created, &updated); err != nil {
		return nil, err
	}
	batch.CreatedAt, _ = parseTime(created)
	batch.UpdatedAt, _ = parseTime(updated)
	return &batch, nil
}

func (s *SQLStore) countWhere(query string, args ...any) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *SQLStore) float64Column(query string, args ...any) ([]float64, error) {
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(float64(n) * p)
	if idx >= n {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func scanAck(row scanner) (*model.NotificationAck, error) {
	var ack model.NotificationAck
	var ackKey, deviceID, sessionID, msgID, traceID, createdAt, policySatisfiedAt sql.NullString
	if err := row.Scan(&ack.AckID, &ack.NotifyID, &ack.UserID, &msgID, &deviceID, &sessionID,
		&ackKey, &ack.LatencyMs, &policySatisfiedAt, &traceID, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	ack.AckKey = ackKey.String
	ack.DeviceID = deviceID.String
	ack.SessionID = sessionID.String
	ack.MsgID = msgID.String
	ack.TraceID = traceID.String
	t, _ := parseTime(createdAt.String)
	ack.CreatedAt = t
	if ps := policySatisfiedAt.String; ps != "" {
		if pt, err := parseTime(ps); err == nil {
			ack.PolicySatisfiedAt = &pt
		}
	}
	return &ack, nil
}

func scanOrderStatusEvent(row scanner) (*model.OrderStatusEvent, error) {
	var event model.OrderStatusEvent
	var fromStatus, toStatus, extraJSON, createdAt string
	if err := row.Scan(&event.EventID, &event.OrderID, &fromStatus, &toStatus, &extraJSON, &event.IdempotencyKey, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(extraJSON), &event.Extra)
	created, err := parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	event.FromStatus = model.OrderStatus(fromStatus)
	event.ToStatus = model.OrderStatus(toStatus)
	event.CreatedAt = created
	return &event, nil
}

// ListACKsByNotifyID returns ACK records for a notification.
func (s *SQLStore) ListACKsByNotifyID(notifyID string) ([]*model.NotificationAck, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT ack_id, notify_id, user_id, COALESCE(msg_id, ''),
		COALESCE(device_id, ''), COALESCE(session_id, ''), ack_key, latency_ms, COALESCE(policy_satisfied_at, ''), COALESCE(trace_id, notify_id), created_at
		FROM notification_acks WHERE notify_id = ? ORDER BY created_at ASC`, notifyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var acks []*model.NotificationAck
	for rows.Next() {
		ack, err := scanAck(rows)
		if err != nil {
			return nil, err
		}
		acks = append(acks, ack)
	}
	return acks, rows.Err()
}

// ListOrderStatusEvents returns status changes for one order.
func (s *SQLStore) ListOrderStatusEvents(orderID string) ([]*model.OrderStatusEvent, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT event_id, order_id, from_status, to_status,
		extra_json, COALESCE(idempotency_key, ''), created_at
		FROM order_status_events WHERE order_id = ? ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*model.OrderStatusEvent
	for rows.Next() {
		event, err := scanOrderStatusEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// ListNotificationsByOrderID returns notifications generated for an order or campaign id.
func (s *SQLStore) ListNotificationsByOrderID(orderID string) ([]*model.Notification, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		COALESCE(target_devices_json, ''), COALESCE(primary_device_id, ''), COALESCE(scenario_run_id, ''), idempotency_key, COALESCE(trace_id, notify_id), created_at, updated_at
		FROM notifications WHERE order_id = ? ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notifications []*model.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}
