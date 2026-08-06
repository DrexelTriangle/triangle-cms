package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// searchLiveArticlesClause pins public search to live content: published and
// not soft-deleted. Archived rows were previously reachable here.
const searchLiveArticlesClause = "`pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL"

const searchSelectColumns = "SELECT `id`, `title`, `slug`, `description`, `text`, `excerpt`, `tags`, `categories`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url`, `focus_keyword`, `meta_description`, `seo_title`, `creation_date`, `scheduled_pub_date`, `canonical_url`, `noindex` FROM `articles` "

// fulltextMinTokenSize mirrors innodb_ft_min_token_size. InnoDB never indexes
// tokens shorter than this, and a required term (`+ab`) that the index cannot
// contain matches nothing at all, so short tokens are dropped before they reach
// AGAINST rather than silently emptying the result set.
const fulltextMinTokenSize = 3

// fulltextTokenSplitter splits on everything InnoDB treats as a word boundary,
// which keeps the Go-side tokens aligned with the indexed ones.
var fulltextTokenSplitter = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// BuildFulltextBooleanQuery turns a raw user query into a safe IN BOOLEAN MODE
// expression. Boolean mode reads +-><()~*"@ as operators, so a query like
// "C++ vs Rust" is a syntax error rather than a search; every token is stripped
// to letters and digits and re-quoted with the operators we actually want: each
// term required, and the last one prefix-matched so a partially typed word
// still finds "university".
//
// It returns "" when nothing usable survives (a query of only short words or
// only punctuation), which is the caller's signal to use the LIKE path instead.
func BuildFulltextBooleanQuery(term string) string {
	fields := fulltextTokenSplitter.Split(strings.TrimSpace(term), -1)

	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) < fulltextMinTokenSize {
			continue
		}
		tokens = append(tokens, "+"+field)
	}
	if len(tokens) == 0 {
		return ""
	}

	tokens[len(tokens)-1] += "*"
	return strings.Join(tokens, " ")
}

// IsFulltextIndexMissingError reports errors that mean the FULLTEXT indexes
// EnsureArticlesSearchIndex builds are not present, so MATCH cannot run. This is
// expected on a database that has not finished (or has never run) that ALTER.
func IsFulltextIndexMissingError(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		// 1191: can't find FULLTEXT index matching the column list.
		if mysqlErr.Number == 1191 {
			return true
		}
	}
	return false
}

// SearchArticles is the public article search.
//
// It prefers FULLTEXT: a leading-wildcard LIKE over `text` cannot use an index,
// so the old query scanned every published body on every keystroke. MATCH ranks
// by term frequency, and the title-only index is weighted above the combined one
// so a headline hit still outranks a passing body mention -- the intent the
// previous CASE expression encoded, with real relevance underneath it.
//
// It falls back to LIKE when the query is too short to tokenize or when the
// indexes have not been built yet, because a search box that returns nothing is
// worse than a slow one.
func SearchArticles(ctx context.Context, conn *sql.DB, term string, limit, offset int) ([]models.Article, error) {
	trimmedTerm := strings.TrimSpace(term)
	if trimmedTerm == "" {
		return []models.Article{}, nil
	}

	booleanQuery := BuildFulltextBooleanQuery(trimmedTerm)
	if booleanQuery == "" {
		return searchArticlesByLike(ctx, conn, trimmedTerm, limit, offset)
	}

	articles, err := searchArticlesByFulltext(ctx, conn, booleanQuery, limit, offset)
	if IsFulltextIndexMissingError(err) {
		return searchArticlesByLike(ctx, conn, trimmedTerm, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	return articles, nil
}

// rrfK damps the contribution of low-ranked results in reciprocal rank fusion.
// 60 is the value from the original RRF paper and the de facto default; it is
// large enough that the top few positions of each list dominate without any
// single list being able to veto the other.
const rrfK = 60.0

// SearchArticlesHybrid ranks by fusing the FULLTEXT results with vector
// nearest-neighbours.
//
// The two are fused by rank, not by score. A MATCH relevance score and a
// euclidean distance have no common scale, and any attempt to normalize them
// into one is a tuning constant that silently rots as the corpus grows.
// Reciprocal rank fusion only asks each side for an ordering, which is the part
// each is actually good at: lexical for exact names and quotes, vector for
// "that story about parking permits" where the article never says "parking".
//
// It falls back to lexical-only whenever the vector half is unavailable, so a
// missing sidecar or an unembedded corpus degrades quality rather than results.
func SearchArticlesHybrid(ctx context.Context, conn *sql.DB, term string, queryVector []float32, limit, offset int) ([]models.Article, error) {
	trimmedTerm := strings.TrimSpace(term)
	if trimmedTerm == "" {
		return []models.Article{}, nil
	}
	if len(queryVector) != EmbeddingDimensions {
		return SearchArticles(ctx, conn, trimmedTerm, limit, offset)
	}

	booleanQuery := BuildFulltextBooleanQuery(trimmedTerm)
	if booleanQuery == "" {
		// Too short to tokenize. The vector half would still return its nearest
		// neighbours for a two-letter query, but they would be noise dressed up as
		// relevance, so this stays on substring matching.
		return searchArticlesByLike(ctx, conn, trimmedTerm, limit, offset)
	}

	// Fusion needs more candidates than the page being served, or a result ranked
	// highly by both lists but outside the top `limit` of each is never seen.
	pool := (limit + offset) * 2
	if pool < 50 {
		pool = 50
	}

	lexicalIDs, err := searchArticleIDsByFulltext(ctx, conn, booleanQuery, pool)
	if IsFulltextIndexMissingError(err) {
		return searchArticlesByLike(ctx, conn, trimmedTerm, limit, offset)
	}
	if err != nil {
		return nil, err
	}

	vectorIDs, err := searchArticleIDsByVector(ctx, conn, queryVector, pool)
	if err != nil {
		if !IsVectorSearchUnavailableError(err) {
			return nil, err
		}
		vectorIDs = nil
	}

	fused := fuseByReciprocalRank(lexicalIDs, vectorIDs)
	if offset >= len(fused) {
		return []models.Article{}, nil
	}
	fused = fused[offset:]
	if limit > 0 && limit < len(fused) {
		fused = fused[:limit]
	}

	return LoadArticlesByIDsInOrder(ctx, conn, fused)
}

// fuseByReciprocalRank merges ranked ID lists into one ordering. Ties break
// toward the first list, which is the lexical one -- when both halves rate two
// articles equally, the one whose words the reader actually typed wins.
func fuseByReciprocalRank(lists ...[]int64) []int64 {
	scores := make(map[int64]float64)
	best := make(map[int64]int)
	order := make([]int64, 0)

	for listIndex, list := range lists {
		for rank, id := range list {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
				best[id] = listIndex
			}
			scores[id] += 1.0 / (rrfK + float64(rank) + 1.0)
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		left, right := order[i], order[j]
		if scores[left] != scores[right] {
			return scores[left] > scores[right]
		}
		return best[left] < best[right]
	})
	return order
}

func searchArticleIDsByFulltext(ctx context.Context, conn *sql.DB, booleanQuery string, limit int) ([]int64, error) {
	const combinedMatch = "MATCH(`title`, `tags`, `text`) AGAINST (? IN BOOLEAN MODE)"
	const titleMatch = "MATCH(`title`) AGAINST (? IN BOOLEAN MODE)"

	query := "SELECT `id` FROM `articles` " +
		"WHERE " + searchLiveArticlesClause + " AND " + combinedMatch + " " +
		"ORDER BY (" + titleMatch + ") * 4 + (" + combinedMatch + ") DESC, `pub_date` DESC, `id` DESC " +
		"LIMIT " + strconv.Itoa(limit)

	rows, err := conn.QueryContext(ctx, query, booleanQuery, booleanQuery, booleanQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectIDs(rows)
}

// vectorOverFetch is how many extra neighbours the inner scan takes so the
// visibility filter has something to discard. The reconciler deletes vectors for
// articles that stop being live, so in practice almost nothing is dropped here;
// this only has to cover the interval between an article being unpublished and
// the next reconciler pass.
const vectorOverFetch = 3

// buildVectorNeighbourQuery is split out so the query's shape can be asserted on
// without a database. An EXPLAIN-based test cannot do that job: on a small table
// the optimizer picks the vector index even for the join form, so the plan only
// diverges at a corpus size no unit test should have to build.
func buildVectorNeighbourQuery(limit int) string {
	// The nearest-neighbour scan has to stand alone in a derived table.
	// MariaDB only uses the HNSW index for a bare ORDER BY VEC_DISTANCE ... LIMIT
	// over the one table; joining articles in to filter by visibility -- which is
	// how this was first written -- silently disqualifies the index and turns the
	// query into a full scan of every stored vector plus a filesort. Measured on
	// the production corpus that was 360ms against 7ms, and it degrades linearly
	// as the archive grows.
	//
	// The visibility filter stays as a join rather than trusting the embeddings
	// table alone: the reconciler's deletes run on an interval, and an
	// unpublished story surfacing in public search during that window is not a
	// tolerable race. It just has to happen outside the derived table.
	//
	// The distance is carried out and re-sorted on, because a derived table's
	// row order is not guaranteed to survive the join, and that order *is* the
	// ranking that reciprocal rank fusion consumes. Re-sorting a few hundred
	// already-ranked rows costs nothing.
	return "SELECT a.`id` FROM (" +
		"SELECT `article_id`, VEC_DISTANCE_EUCLIDEAN(`embedding`, VEC_FromText(?)) AS `d` " +
		"FROM article_embeddings ORDER BY `d` LIMIT " + strconv.Itoa(limit*vectorOverFetch) +
		") AS nn " +
		"JOIN articles AS a ON a.id = nn.article_id " +
		"WHERE a.`pub_date` IS NOT NULL AND a.`pub_date` <= UTC_TIMESTAMP() AND a.`archived_at` IS NULL " +
		"ORDER BY nn.`d`, a.`id` DESC " +
		"LIMIT " + strconv.Itoa(limit)
}

func searchArticleIDsByVector(ctx context.Context, conn *sql.DB, queryVector []float32, limit int) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, buildVectorNeighbourQuery(limit), FormatVector(queryVector))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectIDs(rows)
}

func collectIDs(rows *sql.Rows) ([]int64, error) {
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

// LoadArticlesByIDsInOrder fetches articles and returns them in the order the
// IDs were given, because that order is the ranking and SQL will not preserve it.
func LoadArticlesByIDsInOrder(ctx context.Context, conn *sql.DB, ids []int64) ([]models.Article, error) {
	if len(ids) == 0 {
		return []models.Article{}, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := conn.QueryContext(ctx, searchSelectColumns+"WHERE `id` IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles, err := CollectArticles(rows)
	if err != nil {
		return nil, err
	}

	byID := make(map[int64]models.Article, len(articles))
	for _, article := range articles {
		byID[article.ID] = article
	}

	ordered := make([]models.Article, 0, len(ids))
	for _, id := range ids {
		if article, ok := byID[id]; ok {
			ordered = append(ordered, article)
		}
	}
	return ordered, nil
}

func searchArticlesByFulltext(ctx context.Context, conn *sql.DB, booleanQuery string, limit, offset int) ([]models.Article, error) {
	const combinedMatch = "MATCH(`title`, `tags`, `text`) AGAINST (? IN BOOLEAN MODE)"
	const titleMatch = "MATCH(`title`) AGAINST (? IN BOOLEAN MODE)"

	query := searchSelectColumns +
		"WHERE " + searchLiveArticlesClause + " AND " + combinedMatch + " " +
		// The title score is added rather than used as a tiebreaker so a strong
		// body match can still beat a weak headline one.
		"ORDER BY (" + titleMatch + ") * 4 + (" + combinedMatch + ") DESC, `pub_date` DESC, `id` DESC"
	query = BuildOrderLimit(query, "", "", ArticleSortByColumn, limit, offset)

	rows, err := conn.QueryContext(ctx, query, booleanQuery, booleanQuery, booleanQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return CollectArticles(rows)
}

func searchArticlesByLike(ctx context.Context, conn *sql.DB, term string, limit, offset int) ([]models.Article, error) {
	like := "%" + likeEscaper.Replace(term) + "%"
	query := searchSelectColumns +
		"WHERE " + searchLiveArticlesClause + " AND (`title` LIKE ? ESCAPE '\\\\' OR `tags` LIKE ? ESCAPE '\\\\' OR `text` LIKE ? ESCAPE '\\\\') " +
		"ORDER BY CASE " +
		"WHEN `title` LIKE ? ESCAPE '\\\\' THEN 1 " +
		"WHEN `tags` LIKE ? ESCAPE '\\\\' THEN 2 " +
		"WHEN `text` LIKE ? ESCAPE '\\\\' THEN 3 " +
		"ELSE 4 END, `pub_date` DESC, `id` DESC"
	query = BuildOrderLimit(query, "", "", ArticleSortByColumn, limit, offset)

	rows, err := conn.QueryContext(ctx, query, like, like, like, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return CollectArticles(rows)
}
