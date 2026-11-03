package collect

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/airbar/flight-service/services/collectors/flytoday/internal/config"
	"github.com/airbar/flight-service/services/collectors/flytoday/internal/flytoday"
	"github.com/airbar/flight-service/services/collectors/flytoday/internal/jobs"
	"github.com/airbar/flight-service/shared/domain/flight"
)

type FlytodayClient interface {
	CollectDay(ctx context.Context, origin, destination string, day time.Time) (string, []flight.Flight, error)
	CircuitState() map[string]any
	CircuitOpenRemaining() time.Duration
}
type JobStore interface {
	ListEnabledRoutes(ctx context.Context) ([]flight.Route, error)
	CountActiveJobs(ctx context.Context) (int64, error)
	MarkRouteEnqueued(ctx context.Context, routeID int64) error
	EnqueueSeedJobs(ctx context.Context, routes []flight.Route, spacing time.Duration) (int64, error)
	EnqueueCollectDays(ctx context.Context, route flight.Route, dates []time.Time, spacing time.Duration) (int64, error)
	ClaimJobs(ctx context.Context, WorkerID string, limit int) ([]flight.Job, error)
	CompleteJob(ctx context.Context, jobID int64) error
	FailJob(ctx context.Context, jobID int64, attempts, maxAttempts int, errMsg string, retryDelay time.Duration) error
	DeferJob(ctx context.Context, jobID int64, retryDelay time.Duration, reason string) error
	MarkExhaustedDead(ctx context.Context) (int64, error)
	RequeueFailed(ctx context.Context) (int64, error)
	ReclaimStale(ctx context.Context, after time.Duration) (int64, error)
	UpsertFlights(ctx context.Context, flights []flight.Flight) (int64, error)
}

type Service struct {
	cfg    config.Config
	store  JobStore
	client FlytodayClient
	log    *slog.Logger
	worker string
}

func New(cfg config.Config, store JobStore, client FlytodayClient) *Service {
	hostname, _ := os.Hostname()
	return &Service{
		cfg:    cfg,
		store:  store,
		client: client,
		log:    slog.Default(),
		worker: hostname + "-" + time.Now().Format("150405"),
	}
}

func (s *Service) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.seedLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.maintenanceLoop(ctx)
	}()

	for i := 0; i < s.cfg.MaxWorkers; i++ {
		wg.Add(1)
		id := i
		go func() {
			defer wg.Done()
			s.workerLoop(ctx, id)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (s *Service) seedLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.SeedInterval)
	defer t.Stop()
	s.seedOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.seedOnce(ctx)
		}
	}
}

func (s *Service) seedOnce(ctx context.Context) {
	if reason, delay := s.seedPause(ctx); delay > 0 {
		s.log.Warn("skip seed enqueue", "reason", reason, "retry_in", delay.String())
		return
	}

	routes, err := s.store.ListEnabledRoutes(ctx)
	if err != nil {
		s.log.Error("list routes", "err", err)
		return
	}

	now := time.Now().UTC()
	var due []flight.Route

	for _, r := range routes {
		if r.LastEnqueuedAt == nil || now.Sub(*r.LastEnqueuedAt) >= time.Duration(r.RefreshMinutes)*time.Minute {
			due = append(due, r)
		}
	}
	if len(due) == 0 {
		return
	}

	n, err := s.store.EnqueueSeedJobs(ctx, due, s.cfg.CollectJobSpacing)
	if err != nil {
		s.log.Error("enqueue seeds", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("enqueue seed jobs", "count", n)
	}
}

func (s *Service) maintenanceLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.store.ReclaimStale(ctx, s.cfg.JobStaleAfter); err != nil {
				s.log.Error("reclaim stale", "err", err)
			} else if n > 0 {
				s.log.Warn("reclaimed stale jobs", "count", n)
			}
			if n, err := s.store.RequeueFailed(ctx); err != nil {
				s.log.Error("requeue failed", "err", err)
			} else if n > 0 {
				s.log.Warn("requeued faied jobs", "count", n)
			}
			if n, err := s.store.MarkExhaustedDead(ctx); err != nil {
				s.log.Error("marked exhausted jobs dead", "count", n)
			} else if n > 0 {
				s.log.Warn("marked exhausted jobs dead", "count", n)
			}
		}
	}
}

func (s *Service) workerLoop(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if remaining := s.client.CircuitOpenRemaining(); remaining > 0 {
			s.log.Warn("circuit open; worker backing off", "worker", id, "retry_in", remaining.String())
			sleep(ctx, minDuration(remaining, 30*time.Second))
			continue
		}
		jobsBatch, err := s.store.ClaimJobs(ctx, s.worker, 1)
		if err != nil {
			s.log.Error("claim jobs", "worker", id, "err", err)
			sleep(ctx, time.Second)
			continue
		}

		if len(jobsBatch) == 0 {
			sleep(ctx, s.cfg.PollInterval)
			continue
		}

		for _, job := range jobsBatch {
			s.handle(ctx, id, job)
		}
	}
}

func (s *Service) handle(ctx context.Context, workerID int, job flight.Job) {
	start := time.Now()
	var err error
	switch job.Kind {
	case flight.JobSeedRoute:
		err = s.handleSeed(ctx, job)
	case flight.JobCollectDay:
		err = s.handleCollect(ctx, job)

	default:
		err = fmt.Errorf("unsupported job kind: %s", job.Kind)
	}

	if err != nil {
		delay := s.retryDelay(job, err)
		if flytoday.IsCircuitOpen(err) || flytoday.IsRateLimited(err) {
			s.log.Warn("job deferred by rate limit", "worker", workerID, "job", job.ID, "retry_in", delay.String(), "err", err)
			if deferErr := s.store.DeferJob(ctx, job.ID, delay, err.Error()); deferErr != nil {
				s.log.Error("persist deferred job", "job", job.ID, "err", deferErr)
			}
			return
		}
		if IsSeedPaused(err) {
			s.log.Warn("seed deferred", "worker", workerID, "job", job.ID, "retry_in", delay.String(), "err", err)
			if deferErr := s.store.DeferJob(ctx, job.ID, delay, err.Error()); deferErr != nil {
				s.log.Error("persist deferred seed", "job", job.ID, "err", deferErr)
			}
			return
		}
		s.log.Error("job failed", "worker", workerID, "job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "retry_in", delay.String(), "err", err)
		if failErr := s.store.FailJob(ctx, job.ID, job.Attempts, job.MaxAttempts, err.Error(), delay); failErr != nil {
			s.log.Error("persist job failure", "job", job.ID, "err", failErr)
		}
		return
	}
	if err := s.store.CompleteJob(ctx, job.ID); err != nil {
		s.log.Error("compelete job", "job", job.ID, "err", err)
		return
	}
	s.log.Info("job done", "worker", workerID, "job", job.ID, "kind", job.Kind, "dur", time.Since(start))

}

func (s *Service) handleSeed(ctx context.Context, job flight.Job) error {
	if reason, delay := s.seedPause(ctx); delay > 0 {
		return &SeedPausedError{Reason: reason, RetryAfter: delay}
	}
	routes, err := s.store.ListEnabledRoutes(ctx)
	if err != nil {
		return err
	}
	var route *flight.Route
	for i := range routes {
		if job.RouteID != nil && routes[i].ID == *job.RouteID {
			route = &routes[i]
			break
		}
		if routes[i].Origin == job.Origin && routes[i].Destination == job.Destination {
			route = &routes[i]
			break
		}
	}
	if route == nil {
		route = &flight.Route{
			ID: deref(job.RouteID), Origin: job.Origin, Destination: job.Destination,
			Priority: job.Priority, WindowDays: 30, RefreshMinutes: 360,
		}
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	dates := make([]time.Time, 0, route.WindowDays)
	for i := 0; i < route.WindowDays; i++ {
		dates = append(dates, today.AddDate(0, 0, i))
	}
	n, err := s.store.EnqueueCollectDays(ctx, *route, dates, s.cfg.CollectJobSpacing)
	if err != nil {
		return err
	}
	if job.RouteID != nil {
		_ = s.store.MarkRouteEnqueued(ctx, *job.RouteID)
	}

	s.log.Info("seeded collect_day jobs", "route", route.Origin+"-"+route.Destination, "new", n)
	return nil
}

func (s *Service) handleCollect(ctx context.Context, job flight.Job) error {
	if job.FlightDate == nil {
		return fmt.Errorf("collect_day without date")
	}
	day := job.FlightDate.UTC().Truncate(24 * time.Hour)
	_, flights, err := s.client.CollectDay(ctx, job.Origin, job.Destination, day)
	if err != nil {
		return err
	}
	for i := range flights {
		flights[i].Source = jobs.SourceFlytoday
	}
	n, err := s.store.UpsertFlights(ctx, flights)
	if err != nil {
		return err
	}
	s.log.Info("collected", "od", job.Origin+"-"+job.Destination, "date", day.Format("2006-01-02"), "flights", len(flights), "upserted", n)
	return nil
}

func (s *Service) seedPause(ctx context.Context) (string, time.Duration) {
	if remaining := s.client.CircuitOpenRemaining(); remaining > 0 {
		return "circuit open", remaining
	}
	n, err := s.store.CountActiveJobs(ctx)
	if err != nil {
		s.log.Error("count active jobs", "err", err)
		return "", 0
	}
	if int(n) >= s.cfg.MaxSeedBacklog {
		return fmt.Sprintf("backlog %d >= %d", n, s.cfg.MaxSeedBacklog), 5 * time.Minute
	}
	return "", 0
}

func (s *Service) retryDelay(job flight.Job, err error) time.Duration {
	attempt := maxInt(1, job.Attempts)
	if seedErr, ok := err.(*SeedPausedError); ok && seedErr.RetryAfter > 0 {
		return addPositiveJitter(seedErr.RetryAfter, 0.1)
	}
	if serverDelay := flytoday.RetryAfter(err); serverDelay > 0 {
		return addPositiveJitter(serverDelay, 0.1)
	}
	if flytoday.IsRateLimited(err) {
		d := exponential(s.cfg.RateLimitBackoffBase, s.cfg.RateLimitBackoffMax, attempt)
		return addPositiveJitter(d, 0.25)
	}
	return addJitter(exponential(s.cfg.TransientBackoffBase, s.cfg.TransientBackoffMax, attempt), 0.25)
}

func exponential(backoffBase, backoffMax time.Duration, attempt int) time.Duration {
	if backoffBase <= 0 {
		backoffBase = time.Second
	}
	if backoffMax < backoffBase {
		backoffMax = backoffBase
	}
	exponent := minInt(attempt-1, 20)
	factor := math.Pow(2, float64(exponent))
	d := time.Duration(float64(backoffBase) * factor)
	if d > backoffMax || d < 0 {
		return backoffMax
	}
	return d
}

func addPositiveJitter(d time.Duration, fraction float64) time.Duration {
	if d <= 0 || fraction <= 0 {
		return d
	}
	return d + time.Duration(rand.Float64()*float64(d)*fraction)
}

func addJitter(d time.Duration, fraction float64) time.Duration {
	if d <= 0 || fraction <= 0 {
		return d
	}
	span := float64(d) * fraction
	offset := (rand.Float64()*2 - 1) * span
	jittered := time.Duration(float64(d) + offset)
	if jittered < time.Second {
		return time.Second
	}
	return jittered
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:

	}
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type SeedPausedError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *SeedPausedError) Error() string {
	return fmt.Sprintf("seed paused: %s", e.Reason)
}

func IsSeedPaused(err error) bool {
	_, ok := err.(*SeedPausedError)
	return ok
}
