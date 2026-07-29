package knowledge

import (
	"testing"
	"time"
)

// TestWorkerBackoffGrowsAndCaps pins both halves of the backoff. Growth matters
// because a provider that just dropped a four-minute connection is usually still
// unhappy a second later; the cap matters because without it a build disappears
// for hours over a transient failure.
func TestWorkerBackoffGrowsAndCaps(t *testing.T) {
	worker := &Worker{cfg: WorkerConfig{
		RetryBackoff:    30 * time.Second,
		MaxRetryBackoff: 2 * time.Minute,
	}}
	for _, testCase := range []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 30 * time.Second},
		{attempt: 2, want: 60 * time.Second},
		{attempt: 3, want: 2 * time.Minute},
		{attempt: 9, want: 2 * time.Minute},
	} {
		if got := worker.backoff(testCase.attempt); got != testCase.want {
			t.Errorf("backoff(%d) = %s, want %s", testCase.attempt, got, testCase.want)
		}
	}
}

// TestWorkerConfigDerivesLeaseFromCompileTimeout guards an invariant the two
// values are not free to break independently: a lease shorter than a compile means
// a perfectly healthy worker gets its build stolen while still working on it, and
// two workers then write the same wiki.
func TestWorkerConfigDerivesLeaseFromCompileTimeout(t *testing.T) {
	cfg := WorkerConfigFromEnv(15 * time.Minute)
	if cfg.LeaseFor <= 15*time.Minute {
		t.Fatalf("lease %s must exceed the compile timeout", cfg.LeaseFor)
	}
	if cfg.HeartbeatInterval >= cfg.LeaseFor {
		t.Fatalf("heartbeat %s must be shorter than the lease %s, or a live worker loses it",
			cfg.HeartbeatInterval, cfg.LeaseFor)
	}

	// A tiny timeout must not produce a lease so short that ordinary scheduling
	// jitter looks like a dead worker.
	short := WorkerConfigFromEnv(time.Second)
	if short.LeaseFor < 2*time.Minute {
		t.Fatalf("lease floor not applied, got %s", short.LeaseFor)
	}
}

// TestWorkerOwnerIsUniquePerProcess covers why the owner carries a nonce: without
// it, a restarted process on the same host looks like the same owner and can
// inherit its own stale lease, which defeats reclaim entirely.
func TestWorkerOwnerIsUniquePerProcess(t *testing.T) {
	first := NewWorker(nil, nil, WorkerConfig{Concurrency: 1})
	second := NewWorker(nil, nil, WorkerConfig{Concurrency: 1})
	if first.Owner() == second.Owner() {
		t.Fatalf("two workers share an identity: %s", first.Owner())
	}
	if first.Owner() == "" {
		t.Fatal("worker identity must not be empty; an empty lease owner matches nothing")
	}
}
