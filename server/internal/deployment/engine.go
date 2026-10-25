package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ProviderResolver interface {
	Get(string) (Provider, error)
}

type AsyncDispatcher struct {
	Repository Repository
	Workers    int
	Timeout    time.Duration
	sem        chan struct{}
	once       sync.Once
}

// Dispatch persists the intent before starting remote work. The returned row
// is immediately usable by callers; provider_operation_id is filled by the
// worker once the provider accepts the request.
func (d *AsyncDispatcher) Dispatch(ctx context.Context, input CreateOperationInput, request DeploymentRequest, provider Provider) (Operation, bool, error) {
	if d.Repository == nil || provider == nil {
		ScrubBytes(request.Credentials.RawSecret)
		return Operation{}, false, errors.New("deployment dispatcher is not configured")
	}
	if input.ID == uuid.Nil {
		input.ID = uuid.New()
	}
	if len(input.Request) == 0 {
		input.Request, _ = json.Marshal(request)
	}
	op, created, err := d.Repository.CreateOperation(ctx, input)
	if err != nil || !created {
		ScrubBytes(request.Credentials.RawSecret)
		return op, created, err
	}
	claimed, won, err := d.claim(ctx, op)
	if err != nil || !won {
		ScrubBytes(request.Credentials.RawSecret)
		return op, created, err
	}
	d.initSemaphore()
	go func() { _, _ = d.dispatch(context.Background(), claimed, request, provider) }()
	return op, true, nil
}

// DispatchExisting claims and executes a durable operation created by an API
// handler or recovered by the background reconciler. The claim makes a
// restart-safe operation single-flight without introducing another queue.
func (d *AsyncDispatcher) DispatchExisting(ctx context.Context, op Operation, request DeploymentRequest, provider Provider) (Operation, bool, error) {
	if d.Repository == nil || provider == nil {
		ScrubBytes(request.Credentials.RawSecret)
		return Operation{}, false, errors.New("deployment dispatcher is not configured")
	}
	d.initSemaphore()
	claimed, won, err := d.claim(ctx, op)
	if err != nil || !won {
		ScrubBytes(request.Credentials.RawSecret)
		return op, false, err
	}
	updated, err := d.dispatch(ctx, claimed, request, provider)
	return updated, true, err
}

func (d *AsyncDispatcher) claim(ctx context.Context, op Operation) (Operation, bool, error) {
	if op.Status.IsTerminal() {
		return op, false, nil
	}
	if op.LeaseExpiresAt != nil && op.LeaseExpiresAt.After(time.Now().UTC()) {
		return op, false, nil
	}
	owner := uuid.NewString()
	expires := time.Now().UTC().Add(d.leaseDuration())
	next := op
	next.LeaseOwner = &owner
	next.LeaseExpiresAt = &expires
	claimed, err := d.Repository.UpdateOperation(ctx, next, op.Revision)
	if errors.Is(err, ErrRevisionConflict) {
		return op, false, nil
	}
	return claimed, err == nil, err
}

func (d *AsyncDispatcher) leaseDuration() time.Duration {
	if d.Timeout > 0 && d.Timeout < 5*time.Minute {
		return d.Timeout + time.Minute
	}
	return 5 * time.Minute
}

func (d *AsyncDispatcher) dispatch(ctx context.Context, op Operation, request DeploymentRequest, provider Provider) (Operation, error) {
	d.initSemaphore()
	d.sem <- struct{}{}
	defer func() { <-d.sem }()
	providerCtx := ctx
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		providerCtx, cancel = context.WithTimeout(providerCtx, d.Timeout)
		defer cancel()
	}
	remote, err := provider.TriggerDeployment(providerCtx, request)
	next := op
	next.AttemptCount++
	next.LeaseOwner = nil
	next.LeaseExpiresAt = nil
	if err != nil {
		applyFailure(&next, err, time.Now().UTC(), true)
	} else {
		if strings.TrimSpace(remote.ProviderOperationID) == "" {
			applyFailure(&next, errors.New("provider trigger returned no operation id"), time.Now().UTC(), false)
		} else {
			next.ProviderOperationID = strptr(remote.ProviderOperationID)
			observed := normalizedStatus(remote.NormalizedStatus, StatusQueued)
			if EvaluateTransition(op.Status, observed).Allowed {
				next.Status = observed
			}
			next.NextAttemptAt = nil
			next.Evidence = remoteEvidence(remote)
			if remote.ErrorMessage != "" {
				next.LastError = strptr(RedactEvidenceText(remote.ErrorMessage))
			}
		}
	}
	// The provider timeout must not cancel the durable failure write.
	stored, updateErr := d.Repository.UpdateOperation(context.WithoutCancel(providerCtx), next, op.Revision)
	ScrubBytes(request.Credentials.RawSecret)
	if updateErr != nil {
		return next, updateErr
	}
	return stored, nil
}

func (d *AsyncDispatcher) initSemaphore() {
	d.once.Do(func() {
		workers := d.Workers
		if workers <= 0 {
			workers = 4
		}
		d.sem = make(chan struct{}, workers)
	})
}

func applyFailure(next *Operation, err error, now time.Time, retryable bool) {
	maxAttempts := next.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
		next.MaxAttempts = maxAttempts
	}
	if retryable && isRetryable(err) && next.AttemptCount < maxAttempts {
		next.NextAttemptAt = timeptr(now.Add(retryDelay(next.AttemptCount)))
		next.LastError = strptr(safeError(err))
		return
	}
	next.Status = StatusFailed
	next.NextAttemptAt = nil
	next.LastError = strptr(safeError(err))
}

func retryDelay(attempt int32) time.Duration {
	delay := time.Second
	for i := int32(1); i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	return delay
}

func isRetryable(err error) bool {
	var final *FinalProviderError
	if errors.As(err, &final) {
		return false
	}
	var retryable *RetryableProviderError
	if errors.As(err, &retryable) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func remoteEvidence(remote RemoteOperation) json.RawMessage {
	var evidence any
	if json.Unmarshal(remote.Evidence, &evidence) != nil || evidence == nil {
		evidence = map[string]any{}
	}
	if values, ok := evidence.(map[string]any); ok {
		if remote.DeploymentURL != "" {
			values["deployment_url"] = remote.DeploymentURL
		}
		if remote.InspectURL != "" {
			values["inspect_url"] = remote.InspectURL
		}
		evidence = values
	} else {
		evidence = map[string]any{"provider_evidence": evidence}
	}
	b, _ := json.Marshal(evidence)
	return RedactEvidence(b)
}

type EventGateway struct {
	Repository Repository
	Providers  ProviderResolver
	Find       func(context.Context, string, string, string) (Operation, error)
}

type WebhookResult struct {
	Event     Event
	Applied   bool
	Duplicate bool
}

// Handle verifies and normalizes a provider webhook before writing the
// deduplicated event. Provider adapters own vendor signature details.
func (g *EventGateway) Handle(ctx context.Context, workspaceID string, providerName string, secret []byte, headers map[string][]string, body []byte) (WebhookResult, error) {
	if g.Repository == nil || g.Providers == nil || g.Find == nil {
		return WebhookResult{}, errors.New("deployment webhook gateway is not configured")
	}
	p, err := g.Providers.Get(providerName)
	if err != nil {
		return WebhookResult{}, err
	}
	if !p.Capabilities().Webhook {
		return WebhookResult{}, fmt.Errorf("%w: webhook", ErrCapabilityUnsupported)
	}
	event, err := p.ParseWebhook(ctx, WebhookRequest{Headers: headers, RawBody: body, SigningSecret: secret})
	if err != nil {
		return WebhookResult{}, err
	}
	if strings.TrimSpace(event.ProviderOperationID) == "" {
		return WebhookResult{}, errors.New("deployment webhook has no operation id")
	}
	op, err := g.Find(ctx, workspaceID, providerName, event.ProviderOperationID)
	if err != nil {
		return WebhookResult{}, err
	}
	dedupe := strings.TrimSpace(event.ProviderEventID)
	if dedupe == "" {
		h := sha256.Sum256(body)
		dedupe = hex.EncodeToString(h[:])
	}
	decision := EvaluateTransition(op.Status, event.ObservedStatus)
	appendEvent := AppendEventInput{ID: uuid.New(), WorkspaceID: op.WorkspaceID, OperationID: op.ID, Provider: providerName, DedupeKey: dedupe, Kind: event.ProviderEventType, ObservedStatus: &event.ObservedStatus, Payload: RedactEvidence(event.RawPayload), Applied: decision.Allowed || decision.Action == ActionIgnoreIdempotent}
	if event.ProviderEventID != "" {
		appendEvent.ProviderEventID = &event.ProviderEventID
	}
	stored, created, err := g.Repository.AppendEvent(ctx, appendEvent)
	if err != nil {
		return WebhookResult{}, err
	}
	if !created {
		return WebhookResult{Event: stored, Duplicate: true}, nil
	}
	if decision.Allowed {
		next := op
		next.Status = event.ObservedStatus
		next.Evidence = RedactEvidence(event.Evidence)
		_, err = g.Repository.UpdateOperation(ctx, next, op.Revision)
		if err != nil {
			return WebhookResult{}, err
		}
	}
	return WebhookResult{Event: stored, Applied: decision.Allowed}, nil
}

type OperationLister interface {
	ListNonTerminalOperations(context.Context, string) ([]Operation, error)
}

type ReconcileLookup func(context.Context, Operation) (Provider, OperationLookup, error)
type TriggerLookup func(context.Context, Operation) (Provider, DeploymentRequest, error)

type Reconciler struct {
	Repository    Repository
	Lister        OperationLister
	Resolve       ReconcileLookup
	Trigger       TriggerLookup
	Dispatcher    *AsyncDispatcher
	Interval      time.Duration
	Backoff       time.Duration
	Deadline      time.Duration
	Now           func() time.Time
	OnLongRunning func(Operation)
}

// Reconcile polls due non-terminal operations once. Call it from the existing
// background scheduler; it deliberately does not own a goroutine or ticker.
func (r *Reconciler) Reconcile(ctx context.Context, workspaceID string) (int, error) {
	if r.Lister == nil || r.Repository == nil || r.Resolve == nil {
		return 0, errors.New("deployment reconciler is not configured")
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	ops, err := r.Lister.ListNonTerminalOperations(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, op := range ops {
		if op.Status.IsTerminal() || (op.NextAttemptAt != nil && op.NextAttemptAt.After(now())) {
			continue
		}
		if op.LeaseExpiresAt != nil && op.LeaseExpiresAt.After(now()) {
			continue
		}
		deadlineExceeded := r.Deadline > 0 && now().Sub(op.CreatedAt) >= r.Deadline
		if deadlineExceeded && (op.ProviderOperationID == nil || strings.TrimSpace(*op.ProviderOperationID) == "") {
			if r.OnLongRunning != nil {
				r.OnLongRunning(op)
			}
			next := op
			next.Status = StatusFailed
			next.LastError = strptr("deployment reconciliation deadline exceeded")
			if _, err := r.Repository.UpdateOperation(ctx, next, op.Revision); err != nil {
				return processed, err
			}
			processed++
			continue
		}
		if op.ProviderOperationID == nil || strings.TrimSpace(*op.ProviderOperationID) == "" {
			if r.Trigger == nil || r.Dispatcher == nil {
				r.scheduleRetry(ctx, op, now(), errors.New("deployment trigger is not configured"))
				processed++
				continue
			}
			provider, request, err := r.Trigger(ctx, op)
			if err != nil {
				r.scheduleRetry(ctx, op, now(), err)
				processed++
				continue
			}
			if _, _, err := r.Dispatcher.DispatchExisting(ctx, op, request, provider); err != nil {
				return processed, err
			}
			processed++
			continue
		}
		if r.Resolve == nil {
			r.scheduleRetry(ctx, op, now(), errors.New("deployment lookup is not configured"))
			processed++
			continue
		}
		provider, lookup, err := r.Resolve(ctx, op)
		if err != nil {
			if deadlineExceeded {
				next := op
				next.Status = StatusFailed
				next.LastError = strptr("deployment reconciliation deadline exceeded")
				if _, updateErr := r.Repository.UpdateOperation(ctx, next, op.Revision); updateErr != nil {
					return processed, updateErr
				}
				processed++
				continue
			}
			r.scheduleRetry(ctx, op, now(), err)
			processed++
			continue
		}
		remote, err := provider.GetOperation(ctx, lookup)
		ScrubBytes(lookup.Credentials.RawSecret)
		next := op
		if err != nil {
			if deadlineExceeded {
				next.Status = StatusFailed
				next.LastError = strptr("deployment reconciliation deadline exceeded")
				if _, updateErr := r.Repository.UpdateOperation(ctx, next, op.Revision); updateErr != nil {
					return processed, updateErr
				}
				processed++
				continue
			}
			r.scheduleRetry(ctx, op, now(), err)
			processed++
			continue
		}
		observed := normalizedStatus(remote.NormalizedStatus, op.Status)
		if deadlineExceeded && !observed.IsTerminal() {
			if r.OnLongRunning != nil {
				r.OnLongRunning(op)
			}
			next.Status = StatusFailed
			next.LastError = strptr("deployment reconciliation deadline exceeded")
		} else if EvaluateTransition(op.Status, observed).Allowed {
			next.Status = observed
			next.LastError = nil
		}
		next.ProviderOperationID = strptr(remote.ProviderOperationID)
		next.NextAttemptAt = nil
		next.Evidence = remoteEvidence(remote)
		if remote.ErrorMessage != "" {
			next.LastError = strptr(RedactEvidenceText(remote.ErrorMessage))
		}
		if _, err := r.Repository.UpdateOperation(ctx, next, op.Revision); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (r *Reconciler) scheduleRetry(ctx context.Context, op Operation, now time.Time, err error) {
	next := op
	next.AttemptCount++
	maxAttempts := next.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
		next.MaxAttempts = maxAttempts
	}
	if isRetryable(err) && next.AttemptCount < maxAttempts {
		base := r.Backoff
		if base <= 0 {
			base = time.Second
		}
		delay := base
		for i := int32(1); i < next.AttemptCount && delay < time.Hour; i++ {
			delay *= 2
		}
		next.NextAttemptAt = timeptr(now.Add(delay))
	} else {
		next.Status = StatusFailed
		next.NextAttemptAt = nil
	}
	next.LastError = strptr(safeError(err))
	_, _ = r.Repository.UpdateOperation(ctx, next, op.Revision)
}

func normalizedStatus(value, fallback OperationStatus) OperationStatus {
	switch value {
	case StatusPending, StatusQueued, StatusBuilding, StatusDeploying, StatusReady, StatusSucceeded, StatusFailed, StatusCancelled:
		return value
	case StatusCanceled:
		return StatusCancelled
	default:
		return fallback
	}
}

func safeError(err error) string         { return RedactEvidenceText(fmt.Sprint(err)) }
func strptr(value string) *string        { return &value }
func timeptr(value time.Time) *time.Time { return &value }
