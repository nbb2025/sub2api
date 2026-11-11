//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelRequestMetricsRollingWindow(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "model-metrics", Platform: service.PlatformOpenAI})
	group := mustCreateGroup(t, client, &service.Group{Name: "model-metrics", Platform: service.PlatformOpenAI})
	other := mustCreateGroup(t, client, &service.Group{Name: "other-model-metrics", Platform: service.PlatformOpenAI})
	start := time.Date(2025, 3, 17, 0, 30, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM model_video_outcomes WHERE api_key_id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM bailian_video_tasks WHERE task_key = 'model-metrics-bailian'")
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_error_logs WHERE api_key_id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, other.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	usage := func(at time.Time, groupID int64, id, model string, requested *string, output int, duration *int, video int) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
			(user_id, api_key_id, account_id, group_id, request_id, model, requested_model, output_tokens, duration_ms, video_count, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			user.ID, key.ID, account.ID, groupID, id, model, requested, output, duration, video, at)
		require.NoError(t, err)
	}
	setFirstTokenTiming := func(id string, stream, websocket bool, firstToken int) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `UPDATE usage_logs
			SET stream=$1, openai_ws_mode=$2, first_token_ms=$3 WHERE api_key_id=$4 AND request_id=$5`,
			stream, websocket, firstToken, key.ID, id)
		require.NoError(t, err)
	}
	logError := func(at time.Time, id, clientID, model, path, owner string, status int, limited bool) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO ops_error_logs
			(created_at, group_id, api_key_id, request_id, client_request_id, model, requested_model,
			 request_path, error_owner, error_phase, error_type, status_code, is_business_limited)
			VALUES ($1,$2,$3,$4,$5,'upstream-model',$6,$7,$8,'upstream','api_error',$9,$10)`,
			at, group.ID, key.ID, id, clientID, model, path, owner, status, limited)
		require.NoError(t, err)
	}
	alias := " GPT-ALIAS "
	oneSecond, twoSeconds, fourSeconds := 1000, 2000, 4000
	usage(start, group.ID, "local:a", "upstream-model", &alias, 40, &oneSecond, 0)
	usage(start.Add(time.Hour), group.ID, "local:b", "upstream-model", &alias, 50, &twoSeconds, 0)
	usage(start.Add(time.Hour), group.ID, "local:c", "upstream-model", &alias, 1000, nil, 0)
	usage(start.Add(time.Hour), other.ID, "local:other", "GPT-ALIAS", nil, 100, &fourSeconds, 0)
	usage(start.Add(-time.Millisecond), group.ID, "local:before", "upstream-model", &alias, 2000, &oneSecond, 0)
	usage(end, group.ID, "local:after", "upstream-model", &alias, 2000, &oneSecond, 0)
	usage(start.Add(time.Hour), group.ID, "client:partial", "upstream-model", &alias, 1000, &fourSeconds, 0)
	setFirstTokenTiming("local:a", true, false, 400)
	setFirstTokenTiming("local:b", false, true, 1000)
	setFirstTokenTiming("local:other", false, false, 2000)
	setFirstTokenTiming("client:partial", true, false, 1000)
	usage(start, group.ID, "local:single-token", "no-timing", nil, 1, &oneSecond, 0)
	setFirstTokenTiming("local:single-token", true, false, 500)
	usage(start, group.ID, "local:no-output-interval", "no-timing", nil, 100, &oneSecond, 0)
	setFirstTokenTiming("local:no-output-interval", true, false, oneSecond)
	const chat = "/v1/chat/completions"
	logError(start.Add(time.Hour), "partial", "partial", alias, chat, "provider", 502, false)
	logError(start.Add(time.Hour), "partial", "partial", alias, chat, "provider", 502, false)
	logError(end.Add(-time.Second), "last-hour", "", alias, chat, "provider", 500, false)
	logError(start, "recovered", "", alias, chat, "provider", 200, false)
	logError(start, "client", "", alias, chat, "client", 400, false)
	logError(start, "quota", "", alias, chat, "platform", 429, true)
	logError(start, "video-poll", "", "video-alias", "/v1/videos/task-id/content", "provider", 502, false)

	repo := NewOpsRepository(integrationDB).(*opsRepository)
	const videoModel = "video-alias"
	videoAt := start.Add(2 * time.Hour)
	usage(videoAt, group.ID, "grok-video:completed", videoModel, nil, 100, &fourSeconds, 1)
	latency := int64(3900)
	complete := service.ModelVideoOutcome{GroupID: group.ID, APIKeyID: key.ID, RequestID: "grok-video:completed",
		Model: videoModel, Succeeded: true, CompletedAt: videoAt, LatencyMs: &latency}
	require.NoError(t, repo.SaveModelVideoOutcome(ctx, complete))
	require.NoError(t, repo.SaveModelVideoOutcome(ctx, complete))
	require.NoError(t, repo.SaveModelVideoOutcome(ctx, service.ModelVideoOutcome{GroupID: group.ID, APIKeyID: key.ID,
		RequestID: "grok-video:failed", Model: videoModel, CompletedAt: videoAt}))
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO bailian_video_tasks (task_key, payload, state, updated_at)
		VALUES ('model-metrics-bailian', jsonb_build_object('group_id',$1::bigint,'pending',jsonb_build_object('original_model','wan-alias')),'failed',$2)`, group.ID, videoAt)
	require.NoError(t, err)

	metrics, err := repo.GetModelRequestMetrics(ctx, start, end, 3600)
	require.NoError(t, err)
	byGroup := make(map[int64]map[string]service.ModelRequestMetric)
	for _, metric := range metrics {
		if byGroup[metric.GroupID] == nil {
			byGroup[metric.GroupID] = make(map[string]service.ModelRequestMetric)
		}
		byGroup[metric.GroupID][metric.Model] = metric
	}
	model := byGroup[group.ID]["gpt-alias"]
	require.EqualValues(t, 3, model.Succeeded)
	require.EqualValues(t, 2, model.Failed)
	require.Equal(t, 1500.0, *model.AvgLatencyMs)
	require.Equal(t, 30.0, *model.TokensPerSecond)
	require.EqualValues(t, 2, model.StreamOutputSamples)
	require.NotNil(t, model.StreamOutputTokensPerSecond)
	require.Equal(t, 55.0, *model.StreamOutputTokensPerSecond)
	require.Equal(t, []service.ModelRequestMetricBucket{
		{Index: 0, Succeeded: 1}, {Index: 1, Succeeded: 2, Failed: 1}, {Index: 23, Failed: 1},
	}, model.Buckets)
	require.EqualValues(t, 1, byGroup[other.ID]["gpt-alias"].Succeeded)
	require.EqualValues(t, 0, byGroup[other.ID]["gpt-alias"].Failed)
	require.Nil(t, byGroup[other.ID]["gpt-alias"].StreamOutputTokensPerSecond)
	require.Zero(t, byGroup[other.ID]["gpt-alias"].StreamOutputSamples)
	require.Nil(t, byGroup[group.ID]["no-timing"].StreamOutputTokensPerSecond)
	require.Zero(t, byGroup[group.ID]["no-timing"].StreamOutputSamples)
	video := byGroup[group.ID][videoModel]
	require.Equal(t, []service.ModelRequestMetricBucket{{Index: 2, Succeeded: 1, Failed: 1}}, video.Buckets)
	require.Equal(t, 3900.0, *video.AvgLatencyMs)
	require.Nil(t, video.TokensPerSecond)
	require.Nil(t, video.StreamOutputTokensPerSecond)
	wan := byGroup[group.ID]["wan-alias"]
	require.Equal(t, []service.ModelRequestMetricBucket{{Index: 2, Failed: 1}}, wan.Buckets)
	require.Nil(t, wan.AvgLatencyMs)
}
