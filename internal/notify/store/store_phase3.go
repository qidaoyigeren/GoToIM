package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// ============================================================================
// Campaign Lifecycle (Phase 3.4)
// ============================================================================

// GetCampaign returns a campaign by ID.
func (s *SQLStore) GetCampaign(campaignID string) (*model.Campaign, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT campaign_id, title, COALESCE(description,''), business_type,
		target_count, sent_count, failed_count, COALESCE(status,'active'), COALESCE(rate_limit,0),
		COALESCE(paused_at,''), COALESCE(cancelled_at,''), COALESCE(completed_at,''),
		COALESCE(idempotency_key,''), created_at, updated_at
		FROM campaigns WHERE campaign_id = ?`, campaignID)
	var c model.Campaign
	var pausedAt, cancelledAt, completedAt, createdAt, updatedAt sql.NullString
	if err := row.Scan(&c.CampaignID, &c.Title, &c.Description, &c.BusinessType,
		&c.TargetCount, &c.SentCount, &c.FailedCount, &c.Status, &c.RateLimit,
		&pausedAt, &cancelledAt, &completedAt, &c.IdempotencyKey, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if pausedAt.Valid && pausedAt.String != "" {
		t, _ := parseTime(pausedAt.String)
		c.PausedAt = &t
	}
	if cancelledAt.Valid && cancelledAt.String != "" {
		t, _ := parseTime(cancelledAt.String)
		c.CancelledAt = &t
	}
	if completedAt.Valid && completedAt.String != "" {
		t, _ := parseTime(completedAt.String)
		c.CompletedAt = &t
	}
	c.CreatedAt = parseNullTime(createdAt)
	c.UpdatedAt = parseNullTime(updatedAt)
	return &c, nil
}

// PauseCampaign sets a campaign status to paused.
func (s *SQLStore) PauseCampaign(campaignID string) error {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(context.Background(), `UPDATE campaigns SET status='paused', paused_at=?, updated_at=? WHERE campaign_id=? AND status='active'`,
		now, now, campaignID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("campaign not found or not active")
	}
	return nil
}

// ResumeCampaign sets a campaign status back to active.
func (s *SQLStore) ResumeCampaign(campaignID string) error {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(context.Background(), `UPDATE campaigns SET status='active', paused_at=NULL, updated_at=? WHERE campaign_id=? AND status='paused'`,
		now, campaignID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("campaign not found or not paused")
	}
	return nil
}

// CancelCampaign marks a campaign cancelled and cancels pending outbox rows.
func (s *SQLStore) CancelCampaign(campaignID string) error {
	now := time.Now()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(context.Background(), `UPDATE campaigns SET status='cancelled', cancelled_at=?, updated_at=? WHERE campaign_id=? AND status IN ('active','paused')`,
		formatTime(now), formatTime(now), campaignID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("campaign not found or in terminal state")
	}
	_, _ = tx.ExecContext(context.Background(), `UPDATE notification_outbox SET status='cancelled', updated_at=? WHERE order_id=? AND status IN ('pending','failed')`,
		formatTime(now), campaignID)
	_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status='cancelled' WHERE campaign_id=? AND status='pending'`, campaignID)
	return tx.Commit()
}

// CompleteCampaign marks a campaign as completed.
func (s *SQLStore) CompleteCampaign(campaignID string) error {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(context.Background(), `UPDATE campaigns SET status='completed', completed_at=?, updated_at=? WHERE campaign_id=? AND status='active'`,
		now, now, campaignID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("campaign not found or not active")
	}
	return nil
}

// UpdateCampaignRateLimit updates a campaign's rate limit.
func (s *SQLStore) UpdateCampaignRateLimit(campaignID string, rateLimit int) error {
	res, err := s.db.ExecContext(context.Background(), `UPDATE campaigns SET rate_limit=?, updated_at=? WHERE campaign_id=?`,
		rateLimit, formatTime(time.Now()), campaignID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ============================================================================
// Compensation Strategy Terminal States (Phase 3.2)
// ============================================================================

// MarkOutboxDropped marks an outbox as dropped (retry_then_drop terminal state).
func (s *SQLStore) MarkOutboxDropped(outboxID, lastError string, attempt *model.NotificationAttempt) error {
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
		SET status='dropped', locked_by=NULL, locked_until=NULL, last_error=?, updated_at=?
		WHERE outbox_id=?`, lastError, formatTime(time.Now()), outboxID); err != nil {
		return err
	}
	if attempt != nil {
		_, _ = tx.ExecContext(context.Background(), `UPDATE notifications SET status='dropped', updated_at=? WHERE notify_id=?`,
			formatTime(time.Now()), attempt.NotifyID)
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status='dropped' WHERE notify_id=?`, attempt.NotifyID)
	}
	return tx.Commit()
}

// MarkOutboxAlertPending marks an outbox as needing alert (retry_then_alert terminal state).
func (s *SQLStore) MarkOutboxAlertPending(outboxID, lastError string, attempt *model.NotificationAttempt) error {
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
		SET status='alert_pending', locked_by=NULL, locked_until=NULL, last_error=?, updated_at=?
		WHERE outbox_id=?`, lastError, formatTime(time.Now()), outboxID); err != nil {
		return err
	}
	if attempt != nil {
		_, _ = tx.ExecContext(context.Background(), `UPDATE notifications SET status='alert_pending', updated_at=? WHERE notify_id=?`,
			formatTime(time.Now()), attempt.NotifyID)
		_, _ = tx.ExecContext(context.Background(), `UPDATE campaign_targets SET status='alert_pending' WHERE notify_id=?`, attempt.NotifyID)
	}
	return tx.Commit()
}

// ============================================================================
// Notification Templates (Phase 3.3)
// ============================================================================

// NotificationTemplateRow is the DB representation of a notification template.
type NotificationTemplateRow struct {
	ID              int64
	Name            string
	TitleTemplate   string
	ContentTemplate string
	Variables       string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SeedDefaultTemplates inserts built-in templates if they don't exist.
func (s *SQLStore) SeedDefaultTemplates() error {
	defaults := []struct{ name, title, content string }{
		{"default_notification", "通知", "{{.title}}"},
		{"order_created_template", "订单 {{.order_id}} 已创建", "用户 {{.user_id}} 的订单 {{.order_id}} 已创建，金额为 {{.amount}} 元。"},
		{"order_paid_template", "订单 {{.order_id}} 已支付", "用户 {{.user_id}} 的订单 {{.order_id}} 已支付，金额为 {{.amount}} 元。"},
		{"order_shipped_template", "订单 {{.order_id}} 已发货", "用户 {{.user_id}} 的订单 {{.order_id}} 已发货，物流单号 {{.tracking_number}}。"},
		{"flash_sale_template", "限时秒杀活动开始", "{{.campaign_name}} 正在进行，优惠将在 {{.ttl_seconds}} 秒后失效。"},
	}
	for _, tpl := range defaults {
		_, err := s.db.ExecContext(context.Background(), `INSERT IGNORE INTO notification_templates
			(name, title_template, content_template, variables, enabled, created_at, updated_at)
			VALUES (?, ?, ?, '{}', TRUE, ?, ?)`,
			tpl.name, tpl.title, tpl.content, formatTime(time.Now()), formatTime(time.Now()))
		if err != nil {
			return err
		}
	}
	return nil
}

// GetTemplateByName returns a template by name.
func (s *SQLStore) GetTemplateByName(name string) (*NotificationTemplateRow, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT id, name, title_template, content_template, COALESCE(variables,''), enabled, created_at, updated_at
		FROM notification_templates WHERE name=? AND enabled=TRUE`, name)
	var t NotificationTemplateRow
	var createdAt, updatedAt string
	if err := row.Scan(&t.ID, &t.Name, &t.TitleTemplate, &t.ContentTemplate, &t.Variables, &t.Enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt, _ = parseTime(createdAt)
	t.UpdatedAt, _ = parseTime(updatedAt)
	return &t, nil
}

// ============================================================================
// Scenario Report (Phase 3.6)
// ============================================================================

// GenerateScenarioReport builds a comprehensive report for a scenario run.
func (s *SQLStore) GenerateScenarioReport(runID string) (*model.ScenarioReport, error) {
	run, err := s.GetScenarioRun(runID)
	if err != nil {
		return nil, err
	}
	report := &model.ScenarioReport{
		ScenarioID:   run.RunID,
		ScenarioName: run.Mode,
		Status:       run.Status,
		StartedAt:    run.StartedAt,
		EndedAt:      run.FinishedAt,
	}
	if run.FinishedAt != nil {
		report.DurationSeconds = run.FinishedAt.Sub(run.StartedAt).Seconds()
	} else {
		report.DurationSeconds = time.Since(run.StartedAt).Seconds()
	}
	total := run.GeneratedNotifications
	if total == 0 {
		total = run.SentCount + run.FailedCount
	}
	report.Summary = model.ScenarioSummary{
		TotalNotifications: total,
		SuccessCount:       run.SentCount,
		FailedCount:        run.FailedCount,
		DLQCount:           run.DLQCount,
		AckCount:           run.AckedCount,
	}
	if total > 0 {
		report.Summary.SuccessRate = float64(run.SentCount) / float64(total)
		report.Summary.AckRate = float64(run.AckedCount) / float64(total)
	}
	var droppedCount int64
	_ = s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM notification_outbox
		WHERE scenario_run_id=? AND status='dropped'`, runID).Scan(&droppedCount)
	report.Summary.DroppedCount = droppedCount

	report.Latency = model.ScenarioLatency{
		P50Ms: run.P50LatencyMs,
		P95Ms: run.P95LatencyMs,
		P99Ms: run.P99LatencyMs,
		MaxMs: run.MaxLatencyMs,
	}
	report.DeliveryPath = model.DeliveryPathBreakdown{
		Direct:        int64(run.DeliveryPathDetail.GrpcDirect * float64(total)),
		KafkaFallback: int64(run.DeliveryPathDetail.KafkaFallback * float64(total)),
		Offline:       int64(run.DeliveryPathDetail.OfflineStored * float64(total)),
	}
	report.ErrorSummary = run.ErrorSummary
	if events, err := s.ListScenarioEvents(runID, 200); err == nil {
		for _, ev := range events {
			report.Timeline = append(report.Timeline, model.TimelineEntry{
				Time:    ev.CreatedAt,
				Phase:   ev.Type,
				Message: ev.PayloadJSON,
			})
		}
	}
	if len(report.Timeline) == 0 {
		report.Timeline = append(report.Timeline, model.TimelineEntry{
			Time:    run.StartedAt,
			Phase:   "started",
			Message: "scenario started",
		})
	}
	if run.Status == "running" {
		report.Timeline = append(report.Timeline, model.TimelineEntry{
			Time:    time.Now(),
			Phase:   "running",
			Message: "scenario still running",
		})
	} else if run.FinishedAt != nil {
		report.Timeline = append(report.Timeline, model.TimelineEntry{
			Time:    *run.FinishedAt,
			Phase:   run.Status,
			Message: "scenario " + run.Status,
		})
	}
	report.Suggestions = generateSuggestions(report)
	return report, nil
}

func generateSuggestions(r *model.ScenarioReport) []string {
	var s []string
	total := r.Summary.TotalNotifications
	if total == 0 {
		return s
	}
	fbRatio := float64(r.DeliveryPath.KafkaFallback) / float64(total)
	if fbRatio > 0.3 {
		s = append(s, "Kafka fallback ratio is high (>"+fmt.Sprintf("%.0f", fbRatio*100)+"%), consider checking Comet capacity.")
	}
	if r.Summary.DLQCount > 0 {
		s = append(s, fmt.Sprintf("DLQ count is %d, check failure reasons in error summary.", r.Summary.DLQCount))
	}
	if r.Latency.P99Ms > 500 {
		s = append(s, "P99 latency exceeds 500ms, consider increasing worker concurrency or checking Kafka lag.")
	}
	if r.Summary.DroppedCount > 0 {
		s = append(s, fmt.Sprintf("Dropped count is %d, review retry_then_drop policies.", r.Summary.DroppedCount))
	}
	rateLimited := int64(0)
	for _, e := range r.ErrorSummary {
		if e.Code == "RATE_LIMITED" {
			rateLimited += e.Count
		}
	}
	if rateLimited > 0 {
		s = append(s, fmt.Sprintf("Rate-limited errors: %d, consider adjusting rate limit configuration.", rateLimited))
	}
	if r.Summary.SuccessRate < 0.95 && total > 0 {
		s = append(s, fmt.Sprintf("Success rate below 95%% (%.1f%%), investigate error summary.", r.Summary.SuccessRate*100))
	}
	return s
}

// CleanupStaleIdempotencyKeys removes keys older than the given duration.
func (s *SQLStore) CleanupStaleIdempotencyKeys(maxAge time.Duration) (int64, error) {
	cutoff := formatTime(time.Now().Add(-maxAge))
	res, err := s.db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FindNotifyIDByMsgID looks up a notification ID by its IM message ID.
func (s *SQLStore) FindNotifyIDByMsgID(msgID string) (string, error) {
	if msgID == "" {
		return "", nil
	}
	var notifyID string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT notify_id FROM notification_acks WHERE msg_id = ? LIMIT 1`, msgID).Scan(&notifyID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return notifyID, nil
}
