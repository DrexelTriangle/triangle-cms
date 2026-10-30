package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"

	"server/internal/models"
)

// Per-campaign stats, the numbers the WordPress plugin's reports gave: who it
// went to, opens, clickers, clicks per link, send errors and unsubscribes.
//
// Opens, clickers and total clicks have been tracked per copy since the first
// Delta send. Unique clickers per link (newsletter_delivery_links) and
// unsubscribes put down to the copy they came from (newsletter_deliveries.
// unsubscribed_at) came later, so an issue is only "detailed" when every copy
// that went out was sent by code that tracks them:
//
//   - newsletters.tracking_version is set when the send starts, and
//   - newsletter_deliveries.tracking_version is set when a copy is recorded
//     sent. Both slots run a sender, and the idle one keeps running the
//     previous release until the next deploy, so a copy it sends (whose
//     unsubscribe link carries no delivery token) leaves its version at 0.
//
// For any other issue the unknown numbers are null, never zero.

// NewsletterTrackingVersion is the tracking a send gets today. Version 1 adds
// per-link clickers and unsubscribe attribution.
const NewsletterTrackingVersion = 1

// RecentCampaignCount is how many sent issues the comparison covers,
// including the one asked about.
const RecentCampaignCount = 8

// ErrNewsletterNotSent means the issue has not started sending, so it has no
// stats yet.
var ErrNewsletterNotSent = errors.New("newsletter has not been sent")

// GetNewsletterCampaignStats returns one issue's stats, sql.ErrNoRows for an
// unknown issue, or ErrNewsletterNotSent for a draft or scheduled one.
func GetNewsletterCampaignStats(ctx context.Context, conn *sql.DB, id int64) (models.NewsletterCampaignStats, error) {
	var stats models.NewsletterCampaignStats
	var status string
	var sentAt sql.NullTime
	if err := conn.QueryRowContext(ctx, "SELECT status, sent_at FROM newsletters WHERE id = ?", id).Scan(&status, &sentAt); err != nil {
		return stats, err
	}
	if status != models.NewsletterStatusSending && status != models.NewsletterStatusSent {
		return stats, ErrNewsletterNotSent
	}

	ids := []int64{id}
	earlier, err := earlierSentCampaigns(ctx, conn, id, sentAt)
	if err != nil {
		return stats, err
	}
	ids = append(ids, earlier...)
	summaries, err := campaignSummaries(ctx, conn, ids)
	if err != nil {
		return stats, err
	}
	stats.NewsletterCampaignSummary = summaries[0]
	stats.Recent = summaries

	if stats.Links, err = campaignLinks(ctx, conn, id, stats.DetailedTracking); err != nil {
		return stats, err
	}
	if stats.Errors, err = campaignSendErrors(ctx, conn, id); err != nil {
		return stats, err
	}
	return stats, nil
}

// earlierSentCampaigns lists the sent issues before this one, newest first.
// An issue still sending has no sent_at; it is compared with the latest ones.
func earlierSentCampaigns(ctx context.Context, conn *sql.DB, id int64, sentAt sql.NullTime) ([]int64, error) {
	query := "SELECT id FROM newsletters WHERE status = ? AND id <> ?"
	args := []any{models.NewsletterStatusSent, id}
	if sentAt.Valid {
		query += " AND (sent_at < ? OR (sent_at = ? AND id < ?))"
		args = append(args, sentAt.Time, sentAt.Time, id)
	}
	query += " ORDER BY sent_at DESC, id DESC LIMIT ?"
	args = append(args, RecentCampaignCount-1)

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// campaignSummaries returns the summaries of ids, in the same order. Every
// count is over real copies; test sends never count.
func campaignSummaries(ctx context.Context, conn *sql.DB, ids []int64) ([]models.NewsletterCampaignSummary, error) {
	args := []any{
		DeliveryStatusSent, DeliveryStatusFailed, DeliveryStatusSkipped, DeliveryStatusPending, DeliveryStatusSending,
		DeliveryStatusSent, NewsletterTrackingVersion,
	}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT n.id, n.subject, n.status, n.sent_at, n.tracking_version,
			COUNT(d.id),
			COALESCE(SUM(d.status = ?), 0),
			COALESCE(SUM(d.status = ?), 0),
			COALESCE(SUM(d.status = ?), 0),
			COALESCE(SUM(d.status IN (?, ?)), 0),
			COALESCE(SUM(d.first_opened_at IS NOT NULL), 0),
			COALESCE(SUM(d.first_clicked_at IS NOT NULL), 0),
			COALESCE(SUM(d.click_count), 0),
			COALESCE(SUM(d.unsubscribed_at IS NOT NULL), 0),
			COALESCE(SUM(d.status = ? AND d.tracking_version < ?), 0)
		FROM newsletters n
		LEFT JOIN newsletter_deliveries d ON d.newsletter_id = n.id AND d.is_test = 0
		WHERE n.id IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")+`)
		GROUP BY n.id, n.subject, n.status, n.sent_at, n.tracking_version
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := make(map[int64]models.NewsletterCampaignSummary, len(ids))
	for rows.Next() {
		var (
			s                      models.NewsletterCampaignSummary
			sentAt                 sql.NullTime
			version                int
			unsubscribes, untraced int
		)
		r := &s.Recipients
		if err := rows.Scan(&s.ID, &s.Subject, &s.Status, &sentAt, &version,
			&r.Queued, &r.Sent, &r.Failed, &r.Skipped, &r.InFlight,
			&s.UniqueOpens, &s.UniqueClickers, &s.TotalClicks, &unsubscribes, &untraced); err != nil {
			return nil, err
		}
		if sentAt.Valid {
			t := sentAt.Time.UTC()
			s.SentAt = &t
		}
		s.DetailedTracking = version >= NewsletterTrackingVersion && untraced == 0
		if s.DetailedTracking {
			s.Unsubscribes = &unsubscribes
		}
		fillCampaignRates(&s)
		byID[s.ID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	summaries := make([]models.NewsletterCampaignSummary, 0, len(ids))
	for _, id := range ids {
		if s, ok := byID[id]; ok {
			summaries = append(summaries, s)
		}
	}
	if len(summaries) == 0 || summaries[0].ID != ids[0] {
		// Deleted between the first read and this one.
		return nil, sql.ErrNoRows
	}
	return summaries, nil
}

func rate(n, of int) *float64 {
	if of <= 0 {
		return nil
	}
	r := float64(n) / float64(of)
	return &r
}

// fillCampaignRates works out the rates from the counts. Opens, clicks and
// unsubscribes are over the copies that were sent; click-to-open is clickers
// over openers.
func fillCampaignRates(s *models.NewsletterCampaignSummary) {
	s.OpenRate = rate(s.UniqueOpens, s.Recipients.Sent)
	s.ClickRate = rate(s.UniqueClickers, s.Recipients.Sent)
	s.ClickToOpenRate = rate(s.UniqueClickers, s.UniqueOpens)
	s.UnsubscribeRate = nil
	if s.Unsubscribes != nil {
		s.UnsubscribeRate = rate(*s.Unsubscribes, s.Recipients.Sent)
	}
}

// campaignLinks lists an issue's links, most clicked first. Total clicks come
// from newsletter_links, which has always counted them; unique clickers only
// when the issue is detailed.
func campaignLinks(ctx context.Context, conn *sql.DB, id int64, detailed bool) ([]models.NewsletterCampaignLink, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT l.id, l.url, l.click_count, COUNT(d.id)
		FROM newsletter_links l
		LEFT JOIN newsletter_delivery_links dl ON dl.link_id = l.id
		LEFT JOIN newsletter_deliveries d ON d.id = dl.delivery_id AND d.is_test = 0
		WHERE l.newsletter_id = ?
		GROUP BY l.id, l.url, l.click_count
		ORDER BY l.click_count DESC, l.id
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]models.NewsletterCampaignLink, 0)
	for rows.Next() {
		var l models.NewsletterCampaignLink
		var unique int
		if err := rows.Scan(&l.ID, &l.URL, &l.TotalClicks, &unique); err != nil {
			return nil, err
		}
		if detailed {
			l.UniqueClickers = &unique
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

type sendErrorCount struct {
	message string
	count   int
}

func campaignSendErrors(ctx context.Context, conn *sql.DB, id int64) ([]models.NewsletterSendError, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT error, COUNT(*) FROM newsletter_deliveries
		WHERE newsletter_id = ? AND is_test = 0 AND status = ?
		GROUP BY error
	`, id, DeliveryStatusFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var counts []sendErrorCount
	for rows.Next() {
		var msg sql.NullString
		var n int
		if err := rows.Scan(&msg, &n); err != nil {
			return nil, err
		}
		counts = append(counts, sendErrorCount{msg.String, n})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groupSendErrors(counts), nil
}

// emailLike matches anything shaped like an address, generously: an
// over-masked error is fine, a leaked address is not.
var emailLike = regexp.MustCompile(`[^\s<>"'(),;:=\[\]@]+@[^\s<>"'(),;:=\[\]@]+`)

// maskSendError hides the addresses a mail server's refusal tends to quote.
func maskSendError(msg string) string {
	return emailLike.ReplaceAllString(msg, "[address]")
}

// groupSendErrors masks each error and merges those that are then the same,
// most frequent first.
func groupSendErrors(counts []sendErrorCount) []models.NewsletterSendError {
	totals := map[string]int{}
	for _, c := range counts {
		msg := strings.TrimSpace(maskSendError(c.message))
		if msg == "" {
			msg = "Unknown error"
		}
		totals[msg] += c.count
	}
	grouped := make([]models.NewsletterSendError, 0, len(totals))
	for msg, n := range totals {
		grouped = append(grouped, models.NewsletterSendError{Message: msg, Count: n})
	}
	sort.Slice(grouped, func(i, j int) bool {
		if grouped[i].Count != grouped[j].Count {
			return grouped[i].Count > grouped[j].Count
		}
		return grouped[i].Message < grouped[j].Message
	})
	return grouped
}
