package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type companySummary struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Logo         string `json:"logo"`
	ProblemCount int    `json:"problem_count"`
}

type companiesResponse struct {
	Items []companySummary `json:"items"`
	Total int              `json:"total"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, s-maxage=300, stale-while-revalidate=600")

	pool, err := getDB(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := pool.Query(r.Context(), `
		SELECT c.id, c.name, COUNT(cp.problem_id)::int AS problem_count
		FROM companies c
		LEFT JOIN company_problems cp ON cp.company_id = c.id
		GROUP BY c.id, c.name
		ORDER BY c.name ASC
	`)
	if err != nil {
		http.Error(w, "failed to load companies", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]companySummary, 0, 512)
	for rows.Next() {
		var c companySummary
		if err := rows.Scan(&c.ID, &c.Name, &c.ProblemCount); err != nil {
			http.Error(w, "failed to decode company", http.StatusInternalServerError)
			return
		}
		c.Logo = logoPath(c.Name)
		items = append(items, c)
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating companies", http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(companiesResponse{Items: items, Total: len(items)})
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
