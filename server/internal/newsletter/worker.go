// Package newsletter sends scheduled newsletter issues to their subscribers.
//
// The state lives in the database (see database/newsletter_send.go): the
// worker here only paces the sends and turns a sender's errors into a retry
// or a failure. Every backend slot runs one; the issue's lease decides which
// of them sends.
package newsletter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/mail"
	"os"
	"time"

	db "server/internal/database"
	"server/internal/mailer"
)

const (
	DefaultInterval = 15 * time.Second
	// DefaultLease outlasts one send (the SMTP sender gives up after 30s) with
	// room to spare; the holder renews it before every message.
	DefaultLease = 2 * time.Minute
	// DefaultRate is messages per second.
	DefaultRate = 2.0
	// DefaultDailyCap stays clear of Google Workspace's ~2,000 recipients a
	// day, leaving room for the other mail notify@ sends (triapply).
	DefaultDailyCap = 1500
)

type Worker struct {
	Conn     *sql.DB
	Composer *mailer.Newsletter
	Sender   mailer.Sender
	// Rate is messages per second; DailyCap is the most copies, of any
	// issue, that may go out in a rolling 24 hours.
	Rate     float64
	DailyCap int
	Interval time.Duration
	Lease    time.Duration
	// Owner names this worker on the lease. It must differ per process.
	Owner  string
	Logger *slog.Logger

	// pausedIssue is the issue last stopped at the daily cap, so the pause
	// and the resume are each logged once.
	pausedIssue  int64
	overdueNoted map[int64]bool
}

// NewOwner names a worker for its lease: the host (the container id, under
// Docker) and a random suffix, so a restarted process never mistakes the old
// one's lease for its own.
func NewOwner() string {
	host, _ := os.Hostname()
	var b [4]byte
	_, _ = rand.Read(b[:])
	return host + "-" + hex.EncodeToString(b[:])
}

func (w *Worker) defaults() {
	if w.Rate <= 0 {
		w.Rate = DefaultRate
	}
	if w.DailyCap <= 0 {
		w.DailyCap = DefaultDailyCap
	}
	if w.Interval <= 0 {
		w.Interval = DefaultInterval
	}
	if w.Lease <= 0 {
		w.Lease = DefaultLease
	}
	if w.Owner == "" {
		w.Owner = NewOwner()
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.overdueNoted == nil {
		w.overdueNoted = map[int64]bool{}
	}
}

// Run sends until ctx is cancelled. A message already handed to the sender is
// seen through and recorded before Run returns, so a deploy never leaves a
// copy unaccounted for.
func (w *Worker) Run(ctx context.Context) {
	if w.Conn == nil || w.Sender == nil || w.Composer == nil {
		return
	}
	w.defaults()
	w.Logger.Info("newsletter sender running", "owner", w.Owner, "rate_per_second", w.Rate, "daily_cap", w.DailyCap)

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.Logger.Error("newsletter send tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick starts any issue that has come due, then sends one issue for as long
// as it can: until it is finished, the daily cap is reached, the sender
// reports a transient failure, or ctx is cancelled.
func (w *Worker) Tick(ctx context.Context) error {
	w.defaults()
	if err := w.noteOverdue(ctx); err != nil {
		return err
	}
	for {
		id, err := db.StartDueNewsletter(ctx, w.Conn)
		if err != nil {
			return err
		}
		if id == 0 {
			break
		}
		w.Logger.Info("newsletter issue started sending", "newsletter_id", id)
	}

	issue, ok, err := db.AcquireNewsletterSend(ctx, w.Conn, w.Owner, w.Lease)
	if err != nil || !ok {
		return err
	}
	defer func() {
		// Let another slot pick the issue up at once. A no-op once it is sent.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := db.ReleaseNewsletterSend(releaseCtx, w.Conn, issue.ID, w.Owner); err != nil {
			w.Logger.Error("release newsletter send lease", "newsletter_id", issue.ID, "error", err)
		}
	}()

	links, err := db.EnsureNewsletterLinks(ctx, w.Conn, issue.ID, mailer.TrackableURLs(issue.EmailHTML))
	if err != nil {
		return err
	}
	content := mailer.Issue{ID: issue.ID, Subject: issue.Subject, EmailHTML: issue.EmailHTML}
	gap := time.Duration(float64(time.Second) / w.Rate)

	for sent := 0; ; sent++ {
		if sent > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(gap):
			}
		} else if ctx.Err() != nil {
			return nil
		}

		d, outcome, err := db.ClaimNewsletterDelivery(ctx, w.Conn, issue.ID, w.Owner, w.Lease, w.DailyCap)
		if err != nil {
			return err
		}
		switch outcome {
		case db.DeliveriesDone:
			w.Logger.Info("newsletter issue sent", "newsletter_id", issue.ID)
			return nil
		case db.DailyCapReached:
			if w.pausedIssue != issue.ID {
				w.Logger.Warn("newsletter sending paused at the daily cap", "newsletter_id", issue.ID, "daily_cap", w.DailyCap)
			}
			w.pausedIssue = issue.ID
			return nil
		case db.LeaseLost:
			w.Logger.Warn("newsletter send lease lost", "newsletter_id", issue.ID)
			return nil
		}
		if w.pausedIssue == issue.ID {
			w.Logger.Info("newsletter sending resumed", "newsletter_id", issue.ID)
		}
		w.pausedIssue = 0

		if transient := w.deliver(ctx, content, links, d); transient {
			// The sender is struggling; give it until the next tick.
			return nil
		}
	}
}

// deliver sends one claimed copy and records how it went, both after ctx is
// cancelled if need be: the message may already be on its way. It reports a
// transient failure.
func (w *Worker) deliver(ctx context.Context, issue mailer.Issue, links map[string]int64, d db.ClaimedDelivery) bool {
	ctx = context.WithoutCancel(ctx)
	m := w.Composer.Compose(issue, &mail.Address{Name: d.Name, Address: d.Email}, d.SubscriberToken,
		&mailer.Tracking{Delivery: d.Token, Links: links})
	err := w.Sender.Send(ctx, m)

	result, msg := db.DeliverySent, ""
	if err != nil {
		msg = err.Error()
		result = db.DeliveryDeferred
		if mailer.IsPermanent(err) {
			result = db.DeliveryRejected
		}
		w.Logger.Warn("newsletter delivery failed", "newsletter_id", issue.ID, "delivery_id", d.ID,
			"attempt", d.Attempts, "permanent", result == db.DeliveryRejected, "error", err)
	}

	recordCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.FinishNewsletterDelivery(recordCtx, w.Conn, d, result, msg); err != nil {
		// The row stays in sending and is marked failed when the lease next
		// changes hands: never resent, since it may well have arrived.
		w.Logger.Error("record newsletter delivery", "newsletter_id", issue.ID, "delivery_id", d.ID, "sent", result == db.DeliverySent, "error", err)
	}
	return result == db.DeliveryDeferred
}

// noteOverdue logs, once per issue, a scheduled issue too late to go out on
// its own, so it isn't silently stuck.
func (w *Worker) noteOverdue(ctx context.Context) error {
	ids, err := db.OverdueScheduledNewsletters(ctx, w.Conn)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !w.overdueNoted[id] {
			w.overdueNoted[id] = true
			w.Logger.Warn("scheduled newsletter is too far overdue to send on its own; reschedule it",
				"newsletter_id", id, "max_lateness", db.MaxNewsletterLateness.String())
		}
	}
	return nil
}
