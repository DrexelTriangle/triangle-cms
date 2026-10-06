package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"server/internal/models"
)

var (
	// ErrCampaignSent guards the permanent record: a sent campaign cannot be
	// edited.
	ErrCampaignSent = errors.New("newsletter: campaign already sent")
	// ErrCampaignNotDraft: only drafts may be deleted.
	ErrCampaignNotDraft = errors.New("newsletter: campaign is not a draft")
)

const newsletterCampaignSummaryColumns = "c.id, c.subject, COALESCE(c.preview_text, ''), c.status, c.scheduled_at, c.sent_at, c.recipient_count, COALESCE(c.created_by, ''), COALESCE(c.updated_by, ''), c.created_at, c.updated_at"

// CreateNewsletterCampaign stores a new campaign. It is always a draft,
// whatever the caller wanted: nothing can be scheduled or sent until sending
// exists. BodyBlocks must already be validated and normalized.
func CreateNewsletterCampaign(ctx context.Context, conn *sql.DB, req models.NewsletterCampaignCreateRequest, actor string) (models.NewsletterCampaign, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO newsletter_campaigns (subject, preview_text, body_blocks, status, created_by, updated_by)
		VALUES (?, NULLIF(?, ''), ?, 'draft', ?, ?)`,
		req.Subject, req.PreviewText, string(req.BodyBlocks), actor, actor)
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	// Inside the transaction the insert id is this connection's own, so it is
	// safe here even behind MaxScale; the read below goes back through the
	// pool and re-reads the row.
	id, err := res.LastInsertId()
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	if err := replaceCampaignLists(ctx, tx, id, req.ListIDs); err != nil {
		return models.NewsletterCampaign{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.NewsletterCampaign{}, err
	}
	return GetNewsletterCampaign(ctx, conn, id)
}

// GetNewsletterCampaign reads one campaign including its body.
// sql.ErrNoRows if it does not exist.
func GetNewsletterCampaign(ctx context.Context, conn *sql.DB, id int64) (models.NewsletterCampaign, error) {
	var body string
	row := conn.QueryRowContext(ctx, "SELECT "+newsletterCampaignSummaryColumns+", c.body_blocks FROM newsletter_campaigns c WHERE c.id = ?", id)
	c, err := scanNewsletterCampaign(row, &body)
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	c.BodyBlocks = json.RawMessage(body)
	lists, err := campaignListIDs(ctx, conn, []int64{id})
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	c.ListIDs = lists[id]
	return c, nil
}

// ListNewsletterCampaigns is the editor-facing listing, most recently edited
// first. Rows carry no body.
func ListNewsletterCampaigns(ctx context.Context, conn *sql.DB, status, q string, limit, offset int) ([]models.NewsletterCampaign, int, error) {
	where := []string{}
	args := []any{}
	if status != "" {
		where = append(where, "c.status = ?")
		args = append(args, status)
	}
	if q = strings.TrimSpace(q); q != "" {
		where = append(where, `c.subject LIKE ? ESCAPE '\\'`)
		args = append(args, "%"+escapeLike(q)+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM newsletter_campaigns c"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT "+newsletterCampaignSummaryColumns+" FROM newsletter_campaigns c"+clause+" ORDER BY c.updated_at DESC, c.id DESC LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]models.NewsletterCampaign, 0)
	ids := []int64{}
	for rows.Next() {
		c, err := scanNewsletterCampaign(rows, nil)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	lists, err := campaignListIDs(ctx, conn, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].ListIDs = lists[items[i].ID]
	}
	return items, total, nil
}

// CountNewsletterCampaignsByStatus always returns draft, scheduled, sent and
// all, zero when empty.
func CountNewsletterCampaignsByStatus(ctx context.Context, conn *sql.DB) (map[string]int, error) {
	counts := map[string]int{
		models.CampaignStatusDraft:     0,
		models.CampaignStatusScheduled: 0,
		models.CampaignStatusSent:      0,
		"all":                          0,
	}
	rows, err := conn.QueryContext(ctx, "SELECT status, COUNT(*) FROM newsletter_campaigns GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
		counts["all"] += n
	}
	return counts, rows.Err()
}

// UpdateNewsletterCampaign applies a partial update to an unsent campaign.
// req.Status is ignored here; the handler refuses any status change before it
// gets this far. ErrCampaignSent if the campaign has gone out.
func UpdateNewsletterCampaign(ctx context.Context, conn *sql.DB, id int64, req models.NewsletterCampaignPatchRequest, actor string) (models.NewsletterCampaign, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.NewsletterCampaign{}, err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM newsletter_campaigns WHERE id = ? FOR UPDATE", id).Scan(&status); err != nil {
		return models.NewsletterCampaign{}, err
	}
	if status == models.CampaignStatusSent {
		return models.NewsletterCampaign{}, ErrCampaignSent
	}

	sets := []string{"updated_by = ?"}
	args := []any{actor}
	if req.Subject != nil {
		sets = append(sets, "subject = ?")
		args = append(args, *req.Subject)
	}
	if req.PreviewText != nil {
		sets = append(sets, "preview_text = NULLIF(?, '')")
		args = append(args, *req.PreviewText)
	}
	if req.BodyBlocks != nil {
		sets = append(sets, "body_blocks = ?")
		args = append(args, string(req.BodyBlocks))
	}
	if _, err := tx.ExecContext(ctx, "UPDATE newsletter_campaigns SET "+strings.Join(sets, ", ")+" WHERE id = ?", append(args, id)...); err != nil {
		return models.NewsletterCampaign{}, err
	}
	if req.ListIDs != nil {
		if err := replaceCampaignLists(ctx, tx, id, *req.ListIDs); err != nil {
			return models.NewsletterCampaign{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return models.NewsletterCampaign{}, err
	}
	return GetNewsletterCampaign(ctx, conn, id)
}

// DeleteNewsletterCampaign deletes a draft. A campaign that has been
// scheduled or sent is a record and stays (ErrCampaignNotDraft). Returns
// false for a campaign that does not exist.
func DeleteNewsletterCampaign(ctx context.Context, conn *sql.DB, id int64) (bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, "SELECT status FROM newsletter_campaigns WHERE id = ? FOR UPDATE", id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != models.CampaignStatusDraft {
		return false, ErrCampaignNotDraft
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM newsletter_campaigns WHERE id = ?", id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// NewsletterRecipientCount is the number of distinct subscribed members of the
// campaign's target lists. A list's public flag does not matter here: it only
// gates the public form. The send step snapshots recipients with this same
// join.
func NewsletterRecipientCount(ctx context.Context, conn *sql.DB, campaignID int64) (int, error) {
	var n int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT s.id)
		  FROM newsletter_campaign_lists cl
		  JOIN newsletter_subscriber_lists sl ON sl.list_id = cl.list_id
		  JOIN newsletter_subscribers s ON s.id = sl.subscriber_id
		 WHERE cl.campaign_id = ? AND s.status = 'subscribed'`, campaignID).Scan(&n)
	return n, err
}

// GetNewsletterStats answers the page's tiles and per-list counts.
func GetNewsletterStats(ctx context.Context, conn *sql.DB) (models.NewsletterStatsResponse, error) {
	subs, err := CountNewsletterSubscribersByStatus(ctx, conn)
	if err != nil {
		return models.NewsletterStatsResponse{}, err
	}
	campaigns, err := CountNewsletterCampaignsByStatus(ctx, conn)
	if err != nil {
		return models.NewsletterStatsResponse{}, err
	}
	lists, err := ListNewsletterLists(ctx, conn)
	if err != nil {
		return models.NewsletterStatsResponse{}, err
	}
	return models.NewsletterStatsResponse{Subscribers: subs, Campaigns: campaigns, Lists: lists}, nil
}

func replaceCampaignLists(ctx context.Context, tx *sql.Tx, campaignID int64, listIDs []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM newsletter_campaign_lists WHERE campaign_id = ?", campaignID); err != nil {
		return err
	}
	if len(listIDs) == 0 {
		return nil
	}
	values := strings.TrimSuffix(strings.Repeat("(?, ?),", len(listIDs)), ",")
	args := make([]any, 0, 2*len(listIDs))
	for _, listID := range listIDs {
		args = append(args, campaignID, listID)
	}
	_, err := tx.ExecContext(ctx, "INSERT IGNORE INTO newsletter_campaign_lists (campaign_id, list_id) VALUES "+values, args...)
	return err
}

func campaignListIDs(ctx context.Context, conn *sql.DB, ids []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, id := range ids {
		out[id] = []int64{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT campaign_id, list_id FROM newsletter_campaign_lists WHERE campaign_id IN ("+placeholders(len(ids))+") ORDER BY list_id",
		int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, lid int64
		if err := rows.Scan(&cid, &lid); err != nil {
			return nil, err
		}
		out[cid] = append(out[cid], lid)
	}
	return out, rows.Err()
}

// scanNewsletterCampaign reads the summary columns, plus the body when body is
// non-nil (the query must then select body_blocks last).
func scanNewsletterCampaign(row interface{ Scan(...any) error }, body *string) (models.NewsletterCampaign, error) {
	var (
		c                                       models.NewsletterCampaign
		scheduledAt, sentAt, createdAt, updated sql.NullTime
		recipients                              sql.NullInt64
	)
	dest := []any{&c.ID, &c.Subject, &c.PreviewText, &c.Status, &scheduledAt, &sentAt, &recipients, &c.CreatedBy, &c.UpdatedBy, &createdAt, &updated}
	if body != nil {
		dest = append(dest, body)
	}
	if err := row.Scan(dest...); err != nil {
		return models.NewsletterCampaign{}, err
	}
	c.ScheduledAt = utcPtr(scheduledAt)
	c.SentAt = utcPtr(sentAt)
	c.CreatedAt = utcPtr(createdAt)
	c.UpdatedAt = utcPtr(updated)
	if recipients.Valid {
		n := int(recipients.Int64)
		c.RecipientCount = &n
	}
	c.ListIDs = []int64{}
	return c, nil
}
