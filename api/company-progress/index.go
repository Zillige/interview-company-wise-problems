package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type companyProgressItem struct {
	ID           int   `json:"id"`
	Name         string `json:"name"`
	Logo         string `json:"logo"`
	ProblemCount int    `json:"problem_count"`
	ProblemIDs   []int  `json:"problem_ids"`
}

type companyProgressResponse struct {
	Items []companyProgressItem `json:"items"`
	Total int                   `json:"total"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=3600, stale-while-revalidate=86400")

	pool, err := getDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := pool.Query(r.Context(), `
		SELECT
			c.id,
			c.name,
			COUNT(cp.problem_id)::int AS problem_count,
			COALESCE(array_agg(cp.problem_id ORDER BY cp.problem_id) FILTER (WHERE cp.problem_id IS NOT NULL), '{}') AS problem_ids
		FROM companies c
		LEFT JOIN company_problems cp ON cp.company_id = c.id
		GROUP BY c.id, c.name
		ORDER BY c.name ASC
	`)
	if err != nil {
		http.Error(w, "failed to load company progress data", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]companyProgressItem, 0, 512)
	for rows.Next() {
		var (
			item   companyProgressItem
			idList []int32
		)
		if err := rows.Scan(&item.ID, &item.Name, &item.ProblemCount, &idList); err != nil {
			http.Error(w, "failed to decode company progress data", http.StatusInternalServerError)
			return
		}
		item.Logo = logoPath(item.Name)
		item.ProblemIDs = make([]int, 0, len(idList))
		for _, id := range idList {
			item.ProblemIDs = append(item.ProblemIDs, int(id))
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating company progress data", http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(companyProgressResponse{Items: items, Total: len(items)})
}

func logoPath(company string) string {
	slug := logoSlug(company)
	if slug == "" {
		return "/logos/default.png"
	}
	return "/logos/" + slug + ".png"
}

func logoSlug(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}

	var b strings.Builder
	prevDash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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
		cfg, err := pgxpool.ParseConfig(dbURL)
		if err != nil {
			dbErr = err
			return
		}
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		dbPool, dbErr = pgxpool.NewWithConfig(ctx, cfg)
	})
	if dbErr != nil {
		return nil, dbErr
	}
	return dbPool, nil
}
