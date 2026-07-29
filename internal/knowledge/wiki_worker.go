package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/wellxie/agentmate/internal/llm"
)

// The compile worker.
//
// Compilation is a multi-minute call to a model, which is why it cannot run inside
// a request. The queue is a table, not a broker: the build row already exists and
// already needs a status, so putting the lease on it keeps one row as the single
// answer to "is this build running". A broker would add a second answer that can
// disagree with the first after a crash — exactly when the answer matters.

// WorkerConfig is deliberately small. Every value here has a defensible default,
// because an operator should not have to tune a queue to get a working wiki.
type WorkerConfig struct {
	// Concurrency bounds how many builds compile at once. The binding constraint
	// is provider rate limits and cost, not local CPU, so the default is low.
	Concurrency int
	// PollInterval is how long an idle worker waits before looking again. Polling
	// rather than listening keeps recovery of an abandoned build on the same code
	// path as picking up new work — a LISTEN/NOTIFY wakeup would not fire for a
	// build whose worker died.
	PollInterval time.Duration
	// LeaseFor must exceed the compile timeout, or a healthy worker loses its own
	// lease mid-compile and two workers write the same wiki.
	LeaseFor time.Duration
	// HeartbeatInterval must be comfortably shorter than LeaseFor so a brief
	// database hiccup does not cost a live worker its lease.
	HeartbeatInterval time.Duration
	// RetryBackoff is the base for exponential backoff. A provider that just
	// dropped a four-minute connection is usually still unhappy a second later.
	RetryBackoff time.Duration
	// MaxRetryBackoff caps the wait so a build cannot disappear for hours.
	MaxRetryBackoff time.Duration
}

func WorkerConfigFromEnv(compileTimeout time.Duration) WorkerConfig {
	cfg := WorkerConfig{
		Concurrency:       envPositiveInt("WIKI_WORKER_CONCURRENCY", 2),
		PollInterval:      time.Duration(envPositiveInt("WIKI_WORKER_POLL_SECONDS", 5)) * time.Second,
		HeartbeatInterval: time.Duration(envPositiveInt("WIKI_WORKER_HEARTBEAT_SECONDS", 15)) * time.Second,
		RetryBackoff:      time.Duration(envPositiveInt("WIKI_WORKER_RETRY_BACKOFF_SECONDS", 30)) * time.Second,
		MaxRetryBackoff:   time.Duration(envPositiveInt("WIKI_WORKER_MAX_RETRY_BACKOFF_SECONDS", 600)) * time.Second,
	}
	// Derived from the compile timeout rather than configured independently: the
	// two are not free to disagree. A lease shorter than a compile guarantees that
	// a perfectly healthy worker gets its build stolen while it is still working.
	cfg.LeaseFor = compileTimeout * 2
	if cfg.LeaseFor < 2*time.Minute {
		cfg.LeaseFor = 2 * time.Minute
	}
	return cfg
}

type Worker struct {
	svc  *Service
	repo *Repo
	cfg  WorkerConfig
	// owner is opaque: host plus a process-lifetime nonce. The nonce matters —
	// without it a restarted process on the same host would look like the same
	// owner and could inherit its own stale lease, defeating recovery.
	owner string

	inFlight sync.WaitGroup
	slots    chan struct{}

	// leased tracks what this process holds, so a graceful shutdown can hand the
	// builds back instead of making the next worker wait out the lease.
	leasedMu sync.Mutex
	leased   map[string]string
}

func NewWorker(svc *Service, repo *Repo, cfg WorkerConfig) *Worker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		// A predictable fallback is still unique enough for lease ownership, and
		// failing to start a worker over an entropy hiccup would be worse.
		copy(nonce, fmt.Appendf(nil, "%012d", time.Now().UnixNano()%1e12))
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return &Worker{
		svc:    svc,
		repo:   repo,
		cfg:    cfg,
		owner:  fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(nonce)),
		slots:  make(chan struct{}, cfg.Concurrency),
		leased: make(map[string]string),
	}
}

func (w *Worker) Owner() string { return w.owner }

// Start runs the poll loop until ctx is cancelled, then yields whatever it holds.
//
// The worker does nothing when no compiler is configured. Polling a queue that can
// only produce ErrCompilerUnavailable would burn every build's attempt budget on a
// deployment that simply has no model.
func (w *Worker) Start(ctx context.Context) {
	if w.svc.compiler == nil || !w.svc.compiler.Configured() {
		log.Printf("wiki worker not started: no compiler model configured")
		return
	}
	log.Printf("wiki worker %s started: concurrency=%d lease=%s poll=%s",
		w.owner, w.cfg.Concurrency, w.cfg.LeaseFor, w.cfg.PollInterval)
	go w.loop(ctx)
}

func (w *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Drain the queue rather than taking one build per tick, so a burst does
		// not trickle out at one build per poll interval.
		for w.claimAndRun(ctx) {
		}
		select {
		case <-ctx.Done():
			w.shutdown()
			return
		case <-ticker.C:
		}
	}
}

// claimAndRun claims at most one build and starts it. It reports whether the queue
// might still hold more work.
func (w *Worker) claimAndRun(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case w.slots <- struct{}{}:
	default:
		// At capacity. Not an error: the point of a bound is to stop here.
		return false
	}

	build, err := w.repo.ClaimNextBuild(ctx, w.owner, w.cfg.LeaseFor)
	if err != nil || build == nil {
		<-w.slots
		if err != nil && ctx.Err() == nil {
			log.Printf("wiki worker %s: claim failed: %v", w.owner, err)
		}
		return false
	}

	w.track(build.ID, build.AccountID)
	w.inFlight.Add(1)
	go func() {
		defer func() {
			w.untrack(build.ID)
			<-w.slots
			w.inFlight.Done()
		}()
		w.run(ctx, build)
	}()
	return true
}

// run compiles one build and classifies the outcome.
func (w *Worker) run(ctx context.Context, build *BuildRevision) {
	// The compile gets its own deadline. Without one, a provider that accepts the
	// connection and then stalls holds a slot forever while the lease is dutifully
	// renewed by the heartbeat.
	runCtx, cancel := context.WithTimeout(ctx, w.cfg.LeaseFor)
	defer cancel()
	stopHeartbeat := w.startHeartbeat(runCtx, cancel, build)
	defer stopHeartbeat()

	started := time.Now()
	err := w.svc.RunBuild(runCtx, build)
	elapsed := time.Since(started).Round(time.Second)

	switch {
	case err == nil:
		log.Printf("wiki worker %s: build %s finished in %s", w.owner, build.ID, elapsed)
		return

	case errors.Is(err, ErrLeaseLost):
		// Another worker owns the build. Say nothing to the row: the new owner is
		// authoritative and writing here would corrupt its state.
		log.Printf("wiki worker %s: build %s lease lost after %s, discarding output", w.owner, build.ID, elapsed)
		return

	case llm.Retryable(err) && build.Attempt < build.MaxAttempts:
		backoff := w.backoff(build.Attempt)
		if _, requeueErr := w.repo.RequeueBuild(detachedContext(ctx), build.AccountID, build.ID, w.owner,
			fmt.Sprintf("attempt %d failed, retrying: %v", build.Attempt, err), backoff); requeueErr != nil {
			log.Printf("wiki worker %s: build %s could not be requeued: %v", w.owner, build.ID, requeueErr)
			return
		}
		log.Printf("wiki worker %s: build %s attempt %d/%d failed after %s, retrying in %s: %v",
			w.owner, build.ID, build.Attempt, build.MaxAttempts, elapsed, backoff, err)

	default:
		// Terminal. Either the failure would deterministically recur — a truncated
		// reply, a rejected request — or the attempt budget is spent. Retrying a
		// deterministic failure only doubles the bill for the same outcome.
		status := BuildStatusFailed
		if runCtx.Err() != nil && ctx.Err() != nil {
			// Shutdown, not a defect in the build. Yield it so the next worker
			// picks it up immediately rather than waiting out the lease.
			if yieldErr := w.repo.YieldBuild(detachedContext(ctx), build.AccountID, build.ID, w.owner); yieldErr != nil {
				log.Printf("wiki worker %s: build %s could not be yielded: %v", w.owner, build.ID, yieldErr)
			}
			log.Printf("wiki worker %s: build %s yielded on shutdown after %s", w.owner, build.ID, elapsed)
			return
		}
		reason := err.Error()
		if build.Attempt >= build.MaxAttempts && llm.Retryable(err) {
			reason = fmt.Sprintf("gave up after %d of %d attempts: %v", build.Attempt, build.MaxAttempts, err)
		}
		if _, finishErr := w.repo.FinishBuild(detachedContext(ctx), build.AccountID, build.ID,
			status, CheckStatusPending, nil, reason); finishErr != nil {
			log.Printf("wiki worker %s: build %s could not be marked failed: %v", w.owner, build.ID, finishErr)
			return
		}
		log.Printf("wiki worker %s: build %s failed terminally after %s: %v", w.owner, build.ID, elapsed, err)
	}
}

// startHeartbeat renews the lease while the build runs, and cancels the compile if
// the lease is gone.
//
// Cancelling on a lost lease is the point: without it, a worker that lost its
// lease keeps paying a model for output it is no longer allowed to commit.
func (w *Worker) startHeartbeat(ctx context.Context, cancel context.CancelFunc, build *BuildRevision) func() {
	ticker := time.NewTicker(w.cfg.HeartbeatInterval)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.repo.HeartbeatBuild(ctx, build.AccountID, build.ID, w.owner, w.cfg.LeaseFor); err != nil {
					if errors.Is(err, ErrLeaseLost) {
						log.Printf("wiki worker %s: build %s lease lost, cancelling compile", w.owner, build.ID)
						cancel()
						return
					}
					// A transient database error is not evidence the lease moved.
					// Keep trying: giving up here would abandon a healthy compile.
					log.Printf("wiki worker %s: build %s heartbeat failed: %v", w.owner, build.ID, err)
				}
			}
		}
	}()
	return func() { close(done) }
}

// backoff grows exponentially from the configured base, capped.
func (w *Worker) backoff(attempt int) time.Duration {
	wait := w.cfg.RetryBackoff
	for i := 1; i < attempt; i++ {
		wait *= 2
		if wait >= w.cfg.MaxRetryBackoff {
			return w.cfg.MaxRetryBackoff
		}
	}
	if wait > w.cfg.MaxRetryBackoff {
		return w.cfg.MaxRetryBackoff
	}
	return wait
}

// Stop waits for in-flight builds to wind down. Called after the context that
// Start was given is cancelled.
func (w *Worker) Stop(timeout time.Duration) {
	finished := make(chan struct{})
	go func() {
		w.inFlight.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(timeout):
		// The builds still running will be reclaimed on lease expiry. Waiting
		// longer than the shutdown budget would just get the process killed.
		log.Printf("wiki worker %s: shutdown timeout, leaving builds to lease expiry", w.owner)
	}
}

// shutdown yields everything still leased. Yielding beats waiting out the lease:
// a rolling deploy would otherwise stall every in-flight build for a full lease
// period after the new process is already up and idle.
func (w *Worker) shutdown() {
	w.leasedMu.Lock()
	pending := make(map[string]string, len(w.leased))
	for buildID, accountID := range w.leased {
		pending[buildID] = accountID
	}
	w.leasedMu.Unlock()
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for buildID, accountID := range pending {
		if err := w.repo.YieldBuild(ctx, accountID, buildID, w.owner); err != nil {
			log.Printf("wiki worker %s: build %s could not be yielded on shutdown: %v", w.owner, buildID, err)
		}
	}
}

func (w *Worker) track(buildID, accountID string) {
	w.leasedMu.Lock()
	w.leased[buildID] = accountID
	w.leasedMu.Unlock()
}

func (w *Worker) untrack(buildID string) {
	w.leasedMu.Lock()
	delete(w.leased, buildID)
	w.leasedMu.Unlock()
}

// RunOnce claims and runs a single build synchronously. It exists for tests and
// for a one-shot operator invocation; the poll loop is the production path.
func (w *Worker) RunOnce(ctx context.Context) (*BuildRevision, error) {
	build, err := w.repo.ClaimNextBuild(ctx, w.owner, w.cfg.LeaseFor)
	if err != nil || build == nil {
		return nil, err
	}
	w.run(ctx, build)
	return w.repo.GetBuild(ctx, build.AccountID, build.ID)
}

// detachedContext keeps values and drops cancellation, for writes that record what
// happened. Those writes are needed most precisely when the context died.
func detachedContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

func envPositiveInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed := 0
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
