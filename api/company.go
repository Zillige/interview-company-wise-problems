package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type companyProblem struct {
	ID             int     `json:"id"`
	Title          string  `json:"title"`
	Difficulty     string  `json:"difficulty"`
	AcceptanceRate float64 `json:"acceptance_rate"`
	Link           string  `json:"link"`
	Frequency      float64 `json:"frequency"`
}

type companyDetail struct {
	ID             int              `json:"id"`
	Name           string           `json:"name"`
	Logo           string           `json:"logo"`
	ProblemCount   int              `json:"problem_count"`
	TotalFrequency float64          `json:"total_frequency"`
	Problems       []companyProblem `json:"problems"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, s-maxage=120, stale-while-revalidate=600")

	companyID, err := parseID(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "id query parameter is required", http.StatusBadRequest)
		return
	}

	pool, err := getDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var detail companyDetail
	err = pool.QueryRow(r.Context(), `
		SELECT c.id, c.name
		FROM companies c
		WHERE c.id = $1
	`, companyID).Scan(&detail.ID, &detail.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "company not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to load company", http.StatusInternalServerError)
		return
	}
	detail.Logo = logoPath(detail.Name)

	rows, err := pool.Query(r.Context(), `
		SELECT
			p.id,
			p.title,
			p.difficulty,
			COALESCE(p.acceptance_rate, 0)::float8 AS acceptance_rate,
			p.link,
			COALESCE(cp.frequency, 0)::float8 AS frequency
		FROM company_problems cp
		JOIN problems p ON p.id = cp.problem_id
		WHERE cp.company_id = $1
		ORDER BY cp.frequency DESC NULLS LAST, p.title ASC
	`, companyID)
	if err != nil {
		http.Error(w, "failed to load company problems", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	detail.Problems = make([]companyProblem, 0, 128)
	for rows.Next() {
		var p companyProblem
		if err := rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.AcceptanceRate, &p.Link, &p.Frequency); err != nil {
			http.Error(w, "failed to decode company problems", http.StatusInternalServerError)
			return
		}
		detail.TotalFrequency += p.Frequency
		detail.Problems = append(detail.Problems, p)
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating company problems", http.StatusInternalServerError)
		return
	}

	detail.ProblemCount = len(detail.Problems)
	_ = json.NewEncoder(w).Encode(detail)
}

func parseID(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func logoPath(company string) string {
	name := strings.TrimSpace(company)
	if name == "" {
		return "/logos/default.png"
	}
	return "/logos/" + url.PathEscape(name) + ".png"
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
