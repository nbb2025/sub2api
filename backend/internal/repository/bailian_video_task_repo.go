package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.BailianVideoTaskStore = (*usageBillingRepository)(nil)

func (r *usageBillingRepository) SaveBailianVideoTask(ctx context.Context, task *service.BailianVideoTask) error {
	if task == nil || task.StorageKey() == "" || task.GroupID <= 0 || task.AccountID <= 0 || task.Pending.BailianPricing == nil {
		return errors.New("invalid Bailian task snapshot")
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO bailian_video_tasks (task_key, payload, next_poll_at)
		VALUES ($1, $2::jsonb, NOW() + INTERVAL '15 seconds') ON CONFLICT (task_key) DO NOTHING`, task.StorageKey(), string(payload))
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	existing, err := r.GetBailianVideoTask(ctx, task.StorageKey())
	if err != nil {
		return err
	}
	if existing == nil || existing.GroupID != task.GroupID || existing.AccountID != task.AccountID || existing.Pending.Model != task.Pending.Model {
		return errors.New("bailian task ID conflicts with a different accepted task")
	}
	return nil
}

func (r *usageBillingRepository) GetBailianVideoTask(ctx context.Context, key string) (*service.BailianVideoTask, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM bailian_video_tasks WHERE task_key=$1`, key).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task service.BailianVideoTask
	if err := json.Unmarshal(payload, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *usageBillingRepository) LeaseBailianVideoTasks(ctx context.Context, limit int) ([]*service.BailianVideoTask, error) {
	if limit < 1 || limit > 64 {
		return nil, errors.New("invalid Bailian worker batch size")
	}
	token := uuid.NewString()
	rows, err := r.db.QueryContext(ctx, `WITH due AS (
		SELECT task_key FROM bailian_video_tasks WHERE state='pending' AND next_poll_at<=NOW()
		AND (lease_until IS NULL OR lease_until<=NOW()) ORDER BY next_poll_at LIMIT $1 FOR UPDATE SKIP LOCKED
	) UPDATE bailian_video_tasks t SET lease_until=NOW()+INTERVAL '60 seconds', lease_token=$2
	FROM due WHERE t.task_key=due.task_key RETURNING t.payload`, limit, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]*service.BailianVideoTask, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var task service.BailianVideoTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, err
		}
		task.LeaseToken = token
		tasks = append(tasks, &task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Only terminal jobs expire. Unsettled authoritative usage is retained for
	// retry rather than silently discarded after a prolonged billing outage.
	_, _ = r.db.ExecContext(ctx, `DELETE FROM bailian_video_tasks WHERE task_key IN (
		SELECT task_key FROM bailian_video_tasks WHERE state<>'pending' AND updated_at<NOW()-INTERVAL '7 days' LIMIT 100)`)
	return tasks, nil
}

func (r *usageBillingRepository) UpdateBailianVideoTask(ctx context.Context, task *service.BailianVideoTask, state string, next time.Time) error {
	if task == nil || task.LeaseToken == "" {
		return errors.New("bailian task lease is required")
	}
	switch state {
	case "pending", "done", "failed", "expired":
	default:
		return errors.New("invalid Bailian task state")
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE bailian_video_tasks SET
		payload=CASE WHEN $1::jsonb->'usage' IS NULL AND payload->'usage' IS NOT NULL
			THEN $1::jsonb || jsonb_build_object('usage',payload->'usage') ELSE $1::jsonb END,
		state=$2, next_poll_at=$3, updated_at=NOW() WHERE task_key=$4 AND lease_token=$5
		AND ($1::jsonb->'usage' IS NULL OR payload->'usage' IS NULL OR payload->'usage'=$1::jsonb->'usage')`, string(payload), state, next, task.StorageKey(), task.LeaseToken)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("bailian task lease was replaced")
	}
	return nil
}

func (r *usageBillingRepository) ObserveBailianVideoUsage(ctx context.Context, key string, usage *domain.VideoUsage) error {
	payload, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE bailian_video_tasks SET payload=jsonb_set(payload,'{usage}',$2::jsonb),
		next_poll_at=LEAST(next_poll_at,NOW()), updated_at=NOW() WHERE task_key=$1
		AND (payload->'usage' IS NULL OR payload->'usage'=$2::jsonb)`, key, string(payload))
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("bailian task usage snapshot is missing or inconsistent")
	}
	return nil
}
