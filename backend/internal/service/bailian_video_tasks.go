package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// BailianVideoTask contains no prompts, media, supplier keys or client keys.
// Known usage is saved before settlement so a database failure can be retried
// even after the supplier's 24-hour task lookup window has elapsed.
type BailianVideoTask struct {
	TaskID        string                  `json:"task_id"`
	UserID        int64                   `json:"user_id"`
	APIKeyID      int64                   `json:"api_key_id"`
	GroupID       int64                   `json:"group_id"`
	AccountID     int64                   `json:"account_id"`
	QuotaPlatform string                  `json:"quota_platform"`
	Pending       GrokVideoPendingBilling `json:"pending"`
	Usage         *domain.VideoUsage      `json:"usage,omitempty"`
	LeaseToken    string                  `json:"-"`
}

func (t *BailianVideoTask) StorageKey() string {
	return grokVideoPendingBillingKey(t.TaskID, t.UserID, t.APIKeyID)
}

// Implemented by the existing SQL billing repository, without another pool or
// a second accounting ledger. Lease tokens fence stale workers across replicas.
type BailianVideoTaskStore interface {
	SaveBailianVideoTask(context.Context, *BailianVideoTask) error
	GetBailianVideoTask(context.Context, string) (*BailianVideoTask, error)
	LeaseBailianVideoTasks(context.Context, int) ([]*BailianVideoTask, error)
	UpdateBailianVideoTask(context.Context, *BailianVideoTask, string, time.Time) error
}

type bailianVideoUsageObserver interface {
	ObserveBailianVideoUsage(context.Context, string, *domain.VideoUsage) error
}

func (s *OpenAIGatewayService) PersistBailianVideoObservedUsage(ctx context.Context, taskID string, userID, keyID int64, usage *domain.VideoUsage) error {
	if store, ok := s.usageBillingRepo.(bailianVideoUsageObserver); ok {
		return store.ObserveBailianVideoUsage(ctx, grokVideoPendingBillingKey(taskID, userID, keyID), usage)
	}
	return nil
}

type bailianSettlementWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *OpenAIGatewayService) bailianTaskStore() BailianVideoTaskStore {
	store, _ := s.usageBillingRepo.(BailianVideoTaskStore)
	return store
}

func (s *OpenAIGatewayService) BailianVideoTaskStorageAvailable() bool {
	return s != nil && (s.bailianTaskStore() != nil || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple && !s.cfg.SimpleModeKeyRateLimitEnabled))
}

func (s *OpenAIGatewayService) StoreBailianVideoTask(ctx context.Context, task *BailianVideoTask) error {
	if store := s.bailianTaskStore(); store != nil {
		return store.SaveBailianVideoTask(ctx, task)
	}
	if s.BailianVideoTaskStorageAvailable() { // Unbilled simple-mode test/embedded deployments.
		return nil
	}
	return fmt.Errorf("Bailian durable task storage is unavailable")
}

// RestoreBailianVideoTaskBinding repairs a lost Redis owner/snapshot from SQL.
// It never allows another user, Key, group or an expired task to take ownership.
func (s *OpenAIGatewayService) RestoreBailianVideoTaskBinding(ctx context.Context, taskID string, key *APIKey, userID int64) (bool, error) {
	store := s.bailianTaskStore()
	if store == nil || key == nil || key.GroupID == nil {
		return false, nil
	}
	task, err := store.GetBailianVideoTask(ctx, grokVideoPendingBillingKey(taskID, userID, key.ID))
	if err != nil || task == nil {
		return false, err
	}
	if task.UserID != userID || task.APIKeyID != key.ID || task.GroupID != *key.GroupID || task.TaskID != taskID ||
		GrokVideoE2EDuration(task.Pending.CreatedAt, time.Now()) >= 24*time.Hour {
		return false, nil
	}
	if err := s.BindGrokMediaVideoRequestAccount(ctx, key.GroupID, taskID, userID, key.ID, task.AccountID); err != nil {
		return false, err
	}
	return true, s.StoreGrokVideoPendingBilling(ctx, taskID, userID, key.ID, task.Pending)
}

func (s *OpenAIGatewayService) StartBailianVideoSettlement(keys *APIKeyService) {
	if s == nil || s.bailianTaskStore() == nil || s.userRepo == nil || s.accountRepo == nil || s.cache == nil || keys == nil {
		return
	}
	s.bailianWorkerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		worker := &bailianSettlementWorker{cancel: cancel, done: make(chan struct{})}
		s.bailianWorker = worker
		go func() {
			defer close(worker.done)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.pollBailianVideoTasks(ctx, keys)
				}
			}
		}()
	})
}

func (s *OpenAIGatewayService) StopBailianVideoSettlement() {
	if s == nil || s.bailianWorker == nil {
		return
	}
	s.bailianWorker.cancel()
	<-s.bailianWorker.done
}

func (s *OpenAIGatewayService) pollBailianVideoTasks(ctx context.Context, keys *APIKeyService) {
	store := s.bailianTaskStore()
	batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	tasks, err := store.LeaseBailianVideoTasks(batchCtx, 8)
	cancel()
	if err != nil {
		slog.Warn("bailian task lease unavailable")
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for _, task := range tasks {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			s.settleBailianVideoTask(ctx, keys, task)
		})
	}
	wg.Wait()
}

func (s *OpenAIGatewayService) settleBailianVideoTask(parent context.Context, keys *APIKeyService, task *BailianVideoTask) {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	store := s.bailianTaskStore()
	state, next := "pending", time.Now().Add(time.Minute)
	defer func() {
		updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := store.UpdateBailianVideoTask(updateCtx, task, state, next); err != nil {
			slog.Warn("bailian task progress persistence failed", "api_key_id", task.APIKeyID)
		}
	}()
	if task.Pending.BailianPricing == nil {
		next = time.Now().Add(time.Minute)
		return
	}
	if s.BailianVideoAlreadySettled(ctx, task.TaskID, task.UserID, task.APIKeyID) {
		state = "done"
		return
	}
	var account *Account
	if task.Usage == nil {
		if GrokVideoE2EDuration(task.Pending.CreatedAt, time.Now()) >= 24*time.Hour {
			state = "expired"
			return
		}
		var err error
		account, err = s.accountRepo.GetByID(ctx, task.AccountID)
		if err != nil {
			next = time.Now().Add(time.Minute)
			return
		}
		result, err := func() (*OpenAIForwardResult, error) {
			if s.concurrencyService != nil {
				slot, err := s.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
				if err != nil || slot == nil || !slot.Acquired {
					return nil, fmt.Errorf("Bailian status query capacity unavailable")
				}
				if slot.ReleaseFunc != nil {
					defer slot.ReleaseFunc()
				}
			}
			return s.ForwardBailianVideo(ctx, nil, account, BailianEndpointStatus, task.TaskID, nil)
		}()
		if err != nil {
			if upstream, ok := err.(*BailianVideoUpstreamError); ok && upstream.RetryAfter > time.Minute {
				next = time.Now().Add(upstream.RetryAfter)
			}
			return
		}
		if result.VideoUsage == nil {
			var body struct {
				Output struct {
					Status string `json:"task_status"`
				} `json:"output"`
			}
			_ = json.Unmarshal(result.DeferredMediaResponse.Body, &body)
			if body.Output.Status == "FAILED" || body.Output.Status == "CANCELED" {
				state = "failed"
			}
			return
		}
		task.Usage = result.VideoUsage
		// Persist authoritative seconds before any monetary commit.
		if err := store.UpdateBailianVideoTask(ctx, task, "pending", next); err != nil {
			return
		}
	}
	user, err := s.userRepo.GetByID(ctx, task.UserID)
	if err != nil {
		next = time.Now().Add(time.Minute)
		return
	}
	if account == nil {
		account = &Account{ID: task.AccountID, Platform: PlatformBailian, Type: AccountTypeAPIKey}
	}
	group := &Group{ID: task.GroupID, Platform: PlatformBailian}
	key := &APIKey{ID: task.APIKeyID, UserID: task.UserID, User: user, GroupID: &task.GroupID, Group: group}
	if current, err := keys.GetByID(ctx, task.APIKeyID); err == nil {
		key.Key = current.Key // Only for post-commit auth cache invalidation; never persisted.
	}
	billingModel := task.Pending.BailianPricing.BillingModels[task.Usage.Resolution]
	if billingModel == "" {
		next = time.Now().Add(time.Minute)
		return
	}
	result := &OpenAIForwardResult{RequestID: StableGrokVideoBillingRequestID(task.TaskID), ResponseID: task.TaskID,
		Model: task.Pending.Model, BillingModel: billingModel, UpstreamModel: task.Pending.UpstreamModel,
		VideoUsage: task.Usage, VideoCount: 1, VideoResolution: task.Usage.Resolution,
		BailianVideoPricing: task.Pending.BailianPricing, Duration: GrokVideoE2EDuration(task.Pending.CreatedAt, time.Now())}
	err = s.RecordUsage(ctx, &OpenAIRecordUsageInput{Result: result, APIKey: key, User: user, Account: account,
		InboundEndpoint: BailianVideoCreatePath, UpstreamEndpoint: "/api/v1/tasks", QuotaPlatform: task.QuotaPlatform,
		RequestPayloadHash: HashUsageRequestPayload([]byte(task.TaskID)), APIKeyService: keys,
		ChannelUsageFields: ChannelUsageFields{OriginalModel: task.Pending.OriginalModel, ChannelMappedModel: task.Pending.Model}})
	if err != nil {
		next = time.Now().Add(time.Minute)
		slog.Warn("bailian task settlement will retry", "api_key_id", task.APIKeyID)
		return
	}
	_ = s.MarkBailianVideoSettled(ctx, task.TaskID, task.UserID, task.APIKeyID)
	state = "done"
}

// Used only to skip work after an already committed settlement. Cache errors
// never erase a durable job; SQL deduplication remains authoritative.
func (s *OpenAIGatewayService) BailianVideoAlreadySettled(ctx context.Context, taskID string, userID, keyID int64) bool {
	if s.cache == nil {
		return false
	}
	marker, err := s.cache.GetGrokVideoPendingBilling(ctx, bailianVideoSettledKey(taskID, userID, keyID))
	return err == nil && string(marker) == "settled"
}
