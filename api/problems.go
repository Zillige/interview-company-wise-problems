package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type problemSummary struct {
	ID             int     `json:"id"`
	Title          string  `json:"title"`
	Difficulty     string  `json:"difficulty"`
	AcceptanceRate float64 `json:"acceptance_rate"`
	Link           string  `json:"link"`
	TotalFrequency float64 `json:"total_frequency"`
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
	difficulty, err := parseDifficulty(r.URL.Query().Get("difficulty"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 3000, 5000)

	rows, err := pool.Query(r.Context(), `
		SELECT
			p.id,
			p.title,
			p.difficulty,
			COALESCE(p.acceptance_rate, 0)::float8 AS acceptance_rate,
			p.link,
			COALESCE(SUM(cp.frequency), 0)::float8 AS total_frequency
		FROM problems p
		LEFT JOIN company_problems cp ON cp.problem_id = p.id
		WHERE ($1 = '' OR p.title ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR p.difficulty = $2)
		GROUP BY p.id, p.title, p.difficulty, p.acceptance_rate, p.link
		ORDER BY total_frequency DESC, p.title ASC
		LIMIT $3
	`, q, difficulty, limit)
	if err != nil {
		http.Error(w, "failed to load problems", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]problemSummary, 0, 2048)
	for rows.Next() {
		var p problemSummary
		if err := rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.AcceptanceRate, &p.Link, &p.TotalFrequency); err != nil {
			http.Error(w, "failed to decode problem", http.StatusInternalServerError)
			return
		}
		items = append(items, p)
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating problems", http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(problemsResponse{Items: items, Total: len(items)})
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
