package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type problemSummary struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Difficulty       string  `json:"difficulty"`
	AcceptanceRate   float64 `json:"acceptance_rate"`
	Link             string  `json:"link"`
	TotalFrequency   float64 `json:"total_frequency"`
	MatchedFrequency float64 `json:"matched_frequency,omitempty"`
}

type problemsResponse struct {
	Items []problemSummary `json:"items"`
	Total int              `json:"total"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400, stale-while-revalidate=604800")

	pool, err := getDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	difficulties, err := parseDifficultyList(r.URL.Query().Get("difficulty"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	companyIDs, err := parseCompanyIDs(r.URL.Query().Get("company_ids"), r.URL.Query().Get("companies"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sortBy, err := parseSort(r.URL.Query().Get("sort"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sortDir, err := parseSortDirection(r.URL.Query().Get("sort_dir"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"), 60, 200)
	offset := parseOffset(r.URL.Query().Get("offset"), 0)

	whereClauses := []string{"1=1"}
	countArgs := make([]any, 0, 8)
	if q != "" {
		countArgs = append(countArgs, q)
		whereClauses = append(whereClauses, fmt.Sprintf("p.title ILIKE '%%' || $%d || '%%'", len(countArgs)))
	}
	if len(difficulties) > 0 {
		placeholders := make([]string, 0, len(difficulties))
		for _, d := range difficulties {
			countArgs = append(countArgs, d)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(countArgs)))
		}
		whereClauses = append(whereClauses, "p.difficulty IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(companyIDs) > 0 {
		placeholders := make([]string, 0, len(companyIDs))
		for _, id := range companyIDs {
			countArgs = append(countArgs, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(countArgs)))
		}
		whereClauses = append(whereClauses, "EXISTS (SELECT 1 FROM company_problems cpx WHERE cpx.problem_id = p.id AND cpx.company_id IN ("+strings.Join(placeholders, ",")+"))")
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	var total int
	err = pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM problems p WHERE "+whereSQL, countArgs...).Scan(&total)
	if err != nil {
		http.Error(w, "failed to count problems", http.StatusInternalServerError)
		return
	}

	queryArgs := append([]any{}, countArgs...)
	matchedExpr := "0::float8"
	if len(companyIDs) > 0 {
		placeholders := make([]string, 0, len(companyIDs))
		for _, id := range companyIDs {
			queryArgs = append(queryArgs, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(queryArgs)))
		}
		matchedExpr = "COALESCE(SUM(CASE WHEN cp.company_id IN (" + strings.Join(placeholders, ",") + ") THEN cp.frequency ELSE 0 END), 0)::float8"
	}

	orderBy := "total_frequency DESC, p.title ASC"
	switch sortBy {
	case "difficulty":
		if sortDir == "desc" {
			orderBy = "CASE p.difficulty WHEN 'Easy' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Hard' THEN 3 ELSE 4 END DESC, total_frequency DESC, p.title ASC"
		} else {
			orderBy = "CASE p.difficulty WHEN 'Easy' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Hard' THEN 3 ELSE 4 END ASC, total_frequency DESC, p.title ASC"
		}
	case "company":
		if len(companyIDs) > 0 {
			orderBy = "matched_frequency DESC, total_frequency DESC, p.title ASC"
		}
	}

	queryArgs = append(queryArgs, limit, offset)
	limitPos := len(queryArgs) - 1
	offsetPos := len(queryArgs)

	rows, err := pool.Query(r.Context(), `
		SELECT
			p.id,
			p.title,
			p.difficulty,
			COALESCE(p.acceptance_rate, 0)::float8 AS acceptance_rate,
			p.link,
			COALESCE(SUM(cp.frequency), 0)::float8 AS total_frequency,
			`+matchedExpr+` AS matched_frequency
		FROM problems p
		LEFT JOIN company_problems cp ON cp.problem_id = p.id
		WHERE `+whereSQL+`
		GROUP BY p.id, p.title, p.difficulty, p.acceptance_rate, p.link
		ORDER BY `+orderBy+`
		LIMIT $`+strconv.Itoa(limitPos)+`
		OFFSET $`+strconv.Itoa(offsetPos)+`
	`, queryArgs...)
	if err != nil {
		http.Error(w, "failed to load problems", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]problemSummary, 0, limit)
	for rows.Next() {
		var p problemSummary
		if err := rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.AcceptanceRate, &p.Link, &p.TotalFrequency, &p.MatchedFrequency); err != nil {
			http.Error(w, "failed to decode problem", http.StatusInternalServerError)
			return
		}
		items = append(items, p)
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating problems", http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(problemsResponse{Items: items, Total: total})
}

func parseDifficultyList(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "all") {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		v, err := parseDifficulty(part)
		if err != nil {
			return nil, err
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, nil
}

func parseDifficulty(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "", nil
	case "easy":
		return "Easy", nil
	case "medium":
		return "Medium", nil
	case "hard":
		return "Hard", nil
	default:
		return "", fmt.Errorf("invalid difficulty: %q", raw)
	}
}

func parseCompanyIDs(rawValues ...string) ([]int, error) {
	merged := strings.Join(rawValues, ",")
	if strings.TrimSpace(merged) == "" {
		return nil, nil
	}

	parts := strings.Split(merged, ",")
	seen := map[int]bool{}
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		id, err := strconv.Atoi(trimmed)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid company id: %q", part)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

func parseSort(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "frequency":
		return "frequency", nil
	case "difficulty":
		return "difficulty", nil
	case "company":
		return "company", nil
	default:
		return "", fmt.Errorf("invalid sort: %q", raw)
	}
}

func parseSortDirection(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "asc":
		return "asc", nil
	case "desc":
		return "desc", nil
	default:
		return "", fmt.Errorf("invalid sort_dir: %q", raw)
	}
}

func parseOffset(raw string, defaultValue int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 {
		return defaultValue
	}
	return n
}

func parseLimit(raw string, defaultValue, maxValue int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return defaultValue
	}
	if n > maxValue {
		return maxValue
	}
	return n
}

var (
	dbPool *pgxpool.Pool
	dbOnce sync.Once
	dbErr  error
)

func getDB(ctx context.Context) (*pgxpool.Pool, error) {
	dbOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			dbErr = errors.New("DATABASE_URL environment variable is required")
			return
		}
		dbPool, dbErr = pgxpool.New(ctx, dbURL)
	})
	if dbErr != nil {
		return nil, dbErr
	}
	return dbPool, nil
}
