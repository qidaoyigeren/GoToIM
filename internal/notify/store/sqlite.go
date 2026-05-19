package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a stored resource does not exist.
var ErrNotFound = errors.New("not found")

// SQLStore persists Notify Server business state in a SQL database.
type SQLStore struct {
	db      *sql.DB
	startAt time.Time
	dialect string
}

// SQLiteStore is kept as a compatibility alias for existing service tests.
type SQLiteStore = SQLStore

// Open opens a SQL database and initializes the Notify schema.
func Open(driver, dsn string) (*SQLStore, error) {
	switch driver {
	case "", "sqlite":
		return OpenSQLite(dsn)
	case "mysql":
		return OpenMySQL(dsn)
	default:
		return nil, fmt.Errorf("unsupported notify storage driver: %s", driver)
	}
}

// OpenSQLite opens a SQLite database and initializes the Notify schema.
func OpenSQLite(dsn string) (*SQLStore, error) {
	if strings.TrimSpace(dsn) == "" {
		dsn = "target/notify.db"
	}
	if !strings.Contains(dsn, ":memory:") && !strings.HasPrefix(dsn, "file:") {
		if dir := filepath.Dir(dsn); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite directory: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &SQLStore{db: db, startAt: time.Now(), dialect: "sqlite"}
	if err := s.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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

	s := &SQLStore{db: db, startAt: time.Now(), dialect: "mysql"}
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
			return fmt.Errorf("init notify %s schema: %w", s.dialect, err)
		}
	}
	if err := s.migrateSchema(ctx); err != nil {
		return fmt.Errorf("migrate notify %s schema: %w", s.dialect, err)
	}
	return nil
}

func (s *SQLStore) schemaStatements() []string {
	if s.dialect == "mysql" {
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
				idempotency_key VARCHAR(255),
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
				error_message TEXT,
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
				reason VARCHAR(64) NOT NULL,
				last_error TEXT NOT NULL,
				payload_json MEDIUMTEXT NOT NULL,
				retry_count BIGINT NOT NULL,
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

	return []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS orders (
			order_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			status TEXT NOT NULL,
			items_json TEXT NOT NULL,
			total REAL NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_user_created ON orders(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS order_status_events (
			event_id TEXT PRIMARY KEY,
			order_id TEXT NOT NULL,
			from_status TEXT NOT NULL,
			to_status TEXT NOT NULL,
			extra_json TEXT NOT NULL,
			idempotency_key TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(order_id) REFERENCES orders(order_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_order_status_events_order ON order_status_events(order_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			notify_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			type TEXT NOT NULL,
			business_type TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			order_id TEXT,
			status TEXT NOT NULL,
			priority TEXT NOT NULL DEFAULT 'normal',
			ttl_seconds INTEGER NOT NULL DEFAULT 600,
			ack_policy TEXT NOT NULL DEFAULT 'any_device',
			expected_ack_count INTEGER NOT NULL DEFAULT 1,
			acked_count INTEGER NOT NULL DEFAULT 0,
			business_ack_status TEXT NOT NULL DEFAULT 'pending',
			idempotency_key TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_created ON notifications(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS notification_attempts (
			attempt_id TEXT PRIMARY KEY,
			notify_id TEXT NOT NULL,
			channel TEXT NOT NULL,
			target TEXT NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			FOREIGN KEY(notify_id) REFERENCES notifications(notify_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_attempts_notify ON notification_attempts(notify_id, started_at)`,
		`CREATE TABLE IF NOT EXISTS notification_acks (
			ack_id TEXT PRIMARY KEY,
			notify_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			msg_id TEXT,
			device_id TEXT,
			session_id TEXT,
			ack_key TEXT NOT NULL DEFAULT 'legacy',
			latency_ms REAL NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(notify_id) REFERENCES notifications(notify_id),
			UNIQUE(notify_id, ack_key)
		)`,
		`CREATE TABLE IF NOT EXISTS notification_outbox (
			outbox_id TEXT PRIMARY KEY,
			notify_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			order_id TEXT,
			business_type TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			priority TEXT NOT NULL,
			ttl_seconds INTEGER NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT,
			locked_by TEXT,
			locked_until TEXT,
			last_error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(notify_id) REFERENCES notifications(notify_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_outbox_due ON notification_outbox(status, next_retry_at, locked_until, priority, created_at)`,
		`CREATE TABLE IF NOT EXISTS notification_dlq (
			dlq_id TEXT PRIMARY KEY,
			notify_id TEXT NOT NULL,
			outbox_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			order_id TEXT,
			reason TEXT NOT NULL,
			last_error TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			resolved_at TEXT,
			resolved_by TEXT,
			resolution TEXT,
			UNIQUE(outbox_id)
		)`,
		`CREATE TABLE IF NOT EXISTS campaigns (
			campaign_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT,
			business_type TEXT NOT NULL,
			target_count INTEGER NOT NULL,
			sent_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			idempotency_key TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS campaign_targets (
			campaign_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			notify_id TEXT,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(campaign_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_runs (
			run_id TEXT PRIMARY KEY,
			mode TEXT NOT NULL,
			status TEXT NOT NULL,
			qps INTEGER NOT NULL,
			users INTEGER NOT NULL,
			generated_orders INTEGER NOT NULL DEFAULT 0,
			generated_notifications INTEGER NOT NULL DEFAULT 0,
			sent_count INTEGER NOT NULL DEFAULT 0,
			acked_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			dlq_count INTEGER NOT NULL DEFAULT 0,
			latency_p99_ms REAL NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			last_error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_events (
			event_id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES scenario_runs(run_id)
		)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			key TEXT NOT NULL,
			scope TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			response_snapshot TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(scope, key)
		)`,
	}
}

func (s *SQLStore) migrateSchema(ctx context.Context) error {
	notificationColumns := map[string]string{
		"business_type":       "TEXT NOT NULL DEFAULT ''",
		"event_type":          "TEXT NOT NULL DEFAULT ''",
		"priority":            "TEXT NOT NULL DEFAULT 'normal'",
		"ttl_seconds":         "INTEGER NOT NULL DEFAULT 600",
		"ack_policy":          "TEXT NOT NULL DEFAULT 'any_device'",
		"expected_ack_count":  "INTEGER NOT NULL DEFAULT 1",
		"acked_count":         "INTEGER NOT NULL DEFAULT 0",
		"business_ack_status": "TEXT NOT NULL DEFAULT 'pending'",
	}
	if s.dialect == "mysql" {
		notificationColumns = map[string]string{
			"business_type":       "VARCHAR(64) NOT NULL DEFAULT ''",
			"event_type":          "VARCHAR(64) NOT NULL DEFAULT ''",
			"priority":            "VARCHAR(32) NOT NULL DEFAULT 'normal'",
			"ttl_seconds":         "BIGINT NOT NULL DEFAULT 600",
			"ack_policy":          "VARCHAR(32) NOT NULL DEFAULT 'any_device'",
			"expected_ack_count":  "BIGINT NOT NULL DEFAULT 1",
			"acked_count":         "BIGINT NOT NULL DEFAULT 0",
			"business_ack_status": "VARCHAR(32) NOT NULL DEFAULT 'pending'",
		}
	}
	for name, decl := range notificationColumns {
		if err := s.addColumnIfMissing(ctx, "notifications", name, decl); err != nil {
			return err
		}
	}
	ackKeyDecl := "TEXT NOT NULL DEFAULT 'legacy'"
	if s.dialect == "mysql" {
		ackKeyDecl = "VARCHAR(255) NOT NULL DEFAULT 'legacy'"
	}
	if err := s.addColumnIfMissing(ctx, "notification_acks", "ack_key", ackKeyDecl); err != nil {
		return err
	}
	if s.dialect == "sqlite" {
		_, _ = s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_acks_notify_key ON notification_acks(notify_id, ack_key)`)
	}
	return nil
}

func (s *SQLStore) addColumnIfMissing(ctx context.Context, table, column, decl string) error {
	if s.dialect == "mysql" {
		rows, err := s.db.QueryContext(ctx, `SHOW COLUMNS FROM `+table+` LIKE ?`, column)
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

	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl))
	return err
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

	if _, err := tx.ExecContext(context.Background(), `UPDATE orders SET status = ?, updated_at = ? WHERE order_id = ?`,
		string(order.Status), formatTime(order.UpdatedAt), order.OrderID); err != nil {
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
	_, err := tx.ExecContext(context.Background(), `INSERT INTO notifications
		(notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		 priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		 idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.NotifyID, n.UserID, string(n.Type), n.BusinessType, n.EventType, n.Title, n.Content, n.OrderID, n.Status,
		n.Priority, n.TTLSeconds, n.AckPolicy, n.ExpectedAckCount, n.AckedCount, n.BusinessAckStatus,
		n.IdempotencyKey, formatTime(n.CreatedAt), formatTime(n.UpdatedAt))
	return err
}

func insertOutboxTx(tx *sql.Tx, outbox *model.NotificationOutbox) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO notification_outbox
		(outbox_id, notify_id, user_id, order_id, business_type, event_type, payload_json, priority, ttl_seconds,
		 status, retry_count, next_retry_at, locked_by, locked_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outbox.OutboxID, outbox.NotifyID, outbox.UserID, outbox.OrderID, outbox.BusinessType, outbox.EventType,
		outbox.PayloadJSON, outbox.Priority, outbox.TTLSeconds, outbox.Status, outbox.RetryCount,
		nullableTime(outbox.NextRetryAt), emptyToNil(outbox.LockedBy), nullableTime(outbox.LockedUntil),
		emptyToNil(outbox.LastError), formatTime(outbox.CreatedAt), formatTime(outbox.UpdatedAt))
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

	if _, err := tx.ExecContext(context.Background(), `UPDATE orders SET status = ?, updated_at = ? WHERE order_id = ?`,
		string(order.Status), formatTime(order.UpdatedAt), order.OrderID); err != nil {
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
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO notifications
		(notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		 priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		 idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.NotifyID, n.UserID, string(n.Type), n.BusinessType, n.EventType, n.Title, n.Content, n.OrderID, n.Status,
		n.Priority, n.TTLSeconds, n.AckPolicy, n.ExpectedAckCount, n.AckedCount, n.BusinessAckStatus,
		n.IdempotencyKey, formatTime(n.CreatedAt), formatTime(n.UpdatedAt))
	return err
}

// GetNotification returns a notification by id.
func (s *SQLStore) GetNotification(notifyID string) (*model.Notification, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status, idempotency_key, created_at, updated_at
		FROM notifications WHERE notify_id = ?`, notifyID)
	return scanNotification(row)
}

// ListUserNotifications returns notification history for a user.
func (s *SQLStore) ListUserNotifications(userID string) ([]*model.Notification, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status, idempotency_key, created_at, updated_at
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
		(attempt_id, notify_id, channel, target, status, error_message, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, a.NotifyID, a.Channel, a.Target, a.Status, a.ErrorMessage,
		formatTime(a.StartedAt), finished)
	return err
}

// OutboxCount returns the count for an outbox status.
func (s *SQLStore) OutboxCount(status string) (int64, error) {
	return s.count(`SELECT COUNT(*) FROM notification_outbox WHERE status = '` + status + `'`)
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

	rows, err := tx.QueryContext(context.Background(), `SELECT outbox_id FROM notification_outbox
		WHERE status IN ('pending','failed')
		  AND (next_retry_at IS NULL OR next_retry_at = '' OR next_retry_at <= ?)
		  AND (locked_until IS NULL OR locked_until = '' OR locked_until <= ?)
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, created_at
		LIMIT ?`, formatTime(now), formatTime(now), batchSize)
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
	for _, id := range ids {
		if _, err := tx.ExecContext(context.Background(), `UPDATE notification_outbox
			SET status = 'processing', locked_by = ?, locked_until = ?, updated_at = ?
			WHERE outbox_id = ?`,
			workerID, formatTime(lockedUntil), formatTime(now), id); err != nil {
			return nil, err
		}
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	claimedRows, err := tx.QueryContext(context.Background(), `SELECT outbox_id, notify_id, user_id, order_id, business_type, event_type,
		payload_json, priority, ttl_seconds, status, retry_count, next_retry_at, locked_by, locked_until, last_error, created_at, updated_at
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
	if status == "dlq" {
		if _, err := tx.ExecContext(context.Background(), s.insertIgnoreDLQSQL(), dlq.DLQID, dlq.NotifyID, dlq.OutboxID, dlq.UserID,
			dlq.OrderID, dlq.Reason, dlq.LastError, dlq.PayloadJSON, dlq.RetryCount, formatTime(dlq.CreatedAt)); err != nil {
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
		(attempt_id, notify_id, channel, target, status, error_message, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.AttemptID, a.NotifyID, a.Channel, a.Target, a.Status, a.ErrorMessage,
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

	var status, ackPolicy, businessAckStatus string
	var expected, acked int64
	if err := tx.QueryRowContext(context.Background(), `SELECT status, ack_policy, expected_ack_count, acked_count, business_ack_status
		FROM notifications WHERE notify_id = ?`, ack.NotifyID).Scan(&status, &ackPolicy, &expected, &acked, &businessAckStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if businessAckStatus == "satisfied" && expected <= 1 {
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
		ack.AckID, ack.NotifyID, ack.UserID, ack.MsgID, ack.DeviceID, ack.SessionID, ackKey, ack.LatencyMs, formatTime(ack.CreatedAt))
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
	acked++
	satisfied := ackPolicy == "none" || ackPolicy == "best_effort" || ackPolicy == "any_device" || acked >= expected
	nextBusiness := "pending"
	nextStatus := status
	if satisfied {
		nextBusiness = "satisfied"
		nextStatus = "acked"
	}
	if _, err := tx.ExecContext(context.Background(), `UPDATE notifications
		SET status = ?, acked_count = ?, business_ack_status = ?, updated_at = ? WHERE notify_id = ?`,
		nextStatus, acked, nextBusiness, formatTime(ack.CreatedAt), ack.NotifyID); err != nil {
		return false, err
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
	if s.dialect == "mysql" {
		return `INSERT IGNORE INTO notification_acks
		(ack_id, notify_id, user_id, msg_id, device_id, session_id, ack_key, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	return `INSERT OR IGNORE INTO notification_acks
		(ack_id, notify_id, user_id, msg_id, device_id, session_id, ack_key, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (s *SQLStore) insertIgnoreDLQSQL() string {
	if s.dialect == "mysql" {
		return `INSERT IGNORE INTO notification_dlq
			(dlq_id, notify_id, outbox_id, user_id, order_id, reason, last_error, payload_json, retry_count, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	return `INSERT OR IGNORE INTO notification_dlq
		(dlq_id, notify_id, outbox_id, user_id, order_id, reason, last_error, payload_json, retry_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (s *SQLStore) insertIgnoreCampaignTargetSQL() string {
	if s.dialect == "mysql" {
		return `INSERT IGNORE INTO campaign_targets
			(campaign_id, user_id, notify_id, status, created_at) VALUES (?, ?, ?, ?, ?)`
	}
	return `INSERT OR IGNORE INTO campaign_targets
		(campaign_id, user_id, notify_id, status, created_at) VALUES (?, ?, ?, ?, ?)`
}

// ListDLQ returns unresolved DLQ rows newest first.
func (s *SQLStore) ListDLQ(limit int) ([]*model.NotificationDLQ, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(context.Background(), `SELECT dlq_id, notify_id, outbox_id, user_id, order_id, reason,
		last_error, payload_json, retry_count, created_at, resolved_at, resolved_by, resolution
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
		last_error, payload_json, retry_count, created_at, resolved_at, resolved_by, resolution
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
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO campaigns
		(campaign_id, title, description, business_type, target_count, sent_count, failed_count, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?, ?)`,
		campaignID, title, description, businessType, targetCount, idempotencyKey, formatTime(createdAt), formatTime(createdAt))
	return err
}

// InsertCampaignTarget records one targeted campaign recipient.
func (s *SQLStore) InsertCampaignTarget(campaignID, userID, notifyID, status string, createdAt time.Time) error {
	_, err := s.db.ExecContext(context.Background(), s.insertIgnoreCampaignTargetSQL(),
		campaignID, userID, notifyID, status, formatTime(createdAt))
	return err
}

// CreateScenarioRun persists a scenario run.
func (s *SQLStore) CreateScenarioRun(run *model.ScenarioRun) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO scenario_runs
		(run_id, mode, status, qps, users, generated_orders, generated_notifications, sent_count, acked_count,
		 failed_count, dlq_count, latency_p99_ms, started_at, finished_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.Mode, run.Status, run.QPS, run.Users, run.GeneratedOrders, run.GeneratedNotifications,
		run.SentCount, run.AckedCount, run.FailedCount, run.DLQCount, run.LatencyP99Ms, formatTime(run.StartedAt),
		nullableTimePtr(run.FinishedAt), emptyToNil(run.LastError))
	return err
}

// GetScenarioRun returns a scenario run.
func (s *SQLStore) GetScenarioRun(runID string) (*model.ScenarioRun, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT run_id, mode, status, qps, users, generated_orders,
		generated_notifications, sent_count, acked_count, failed_count, dlq_count, latency_p99_ms, started_at, finished_at, last_error
		FROM scenario_runs WHERE run_id = ?`, runID)
	return scanScenarioRun(row)
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

	var grpcDirect, fallback, logicPush, unknown int64
	attemptRows, err := s.db.QueryContext(context.Background(), `SELECT channel, COUNT(*) FROM notification_attempts GROUP BY channel`)
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
	detailTotal := grpcDirect + fallback + logicPush + unknown
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
		stats.LatencyP99Ms = latencies[n*99/100]
		stats.LatencyMaxMs = latencies[n-1]
	}
	return stats, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOutbox(row scanner) (*model.NotificationOutbox, error) {
	var o model.NotificationOutbox
	var nextRetryAt, lockedUntil, createdAt, updatedAt sql.NullString
	var lockedBy, lastError sql.NullString
	if err := row.Scan(&o.OutboxID, &o.NotifyID, &o.UserID, &o.OrderID, &o.BusinessType, &o.EventType,
		&o.PayloadJSON, &o.Priority, &o.TTLSeconds, &o.Status, &o.RetryCount, &nextRetryAt,
		&lockedBy, &lockedUntil, &lastError, &createdAt, &updatedAt); err != nil {
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
		&d.LastError, &d.PayloadJSON, &d.RetryCount, &createdAt, &resolvedAt, &resolvedBy, &resolution); err != nil {
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
		&run.LatencyP99Ms, &started, &finished, &lastError); err != nil {
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
	var typ, createdAt, updatedAt string
	if err := row.Scan(&n.NotifyID, &n.UserID, &typ, &n.BusinessType, &n.EventType, &n.Title, &n.Content, &n.OrderID, &n.Status,
		&n.Priority, &n.TTLSeconds, &n.AckPolicy, &n.ExpectedAckCount, &n.AckedCount, &n.BusinessAckStatus,
		&n.IdempotencyKey, &createdAt, &updatedAt); err != nil {
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
	return time.Parse(time.RFC3339Nano, v)
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

func normalizeAckKey(ack *model.NotificationAck) string {
	if ack.DeviceID != "" || ack.SessionID != "" {
		return ack.MsgID + "|" + ack.DeviceID + "|" + ack.SessionID
	}
	if ack.MsgID != "" {
		return ack.MsgID
	}
	return "legacy"
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
