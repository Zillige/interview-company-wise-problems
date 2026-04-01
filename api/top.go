package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

type topCompany struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Logo      string  `json:"logo"`
	Frequency float64 `json:"frequency"`
}

type topProblem struct {
	ID             int          `json:"id"`
	Title          string       `json:"title"`
	Difficulty     string       `json:"difficulty"`
	AcceptanceRate float64      `json:"acceptance_rate"`
	Link           string       `json:"link"`
	TotalFrequency float64      `json:"total_frequency"`
	Companies      []topCompany `json:"companies"`
}

type topResponse struct {
	Items []topProblem `json:"items"`
	Total int          `json:"total"`
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

	limit := parseLimit(r.URL.Query().Get("limit"), 25)
	rows, err := pool.Query(r.Context(), `
		WITH ranked AS (
			SELECT
				p.id,
				p.title,
				p.difficulty,
				COALESCE(p.acceptance_rate, 0)::float8 AS acceptance_rate,
				p.link,
				COALESCE(SUM(cp.frequency), 0)::float8 AS total_frequency
			FROM problems p
			LEFT JOIN company_problems cp ON cp.problem_id = p.id
			GROUP BY p.id, p.title, p.difficulty, p.acceptance_rate, p.link
			ORDER BY total_frequency DESC, p.title ASC
			LIMIT $1
		)
		SELECT
			r.id,
			r.title,
			r.difficulty,
			r.acceptance_rate,
			r.link,
			r.total_frequency,
			c.id,
			c.name,
			COALESCE(cp.frequency, 0)::float8 AS frequency
		FROM ranked r
		LEFT JOIN company_problems cp ON cp.problem_id = r.id
		LEFT JOIN companies c ON c.id = cp.company_id
		ORDER BY r.total_frequency DESC, r.title ASC, frequency DESC
	`, limit)
	if err != nil {
		http.Error(w, "failed to load top problems", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	byID := map[int]*topProblem{}
	order := make([]int, 0, limit)

	for rows.Next() {
		var (
			pid        int
			title      string
			difficulty string
			acceptance float64
			link       string
			total      float64
			cid        sql.NullInt64
			cname      sql.NullString
			freq       float64
		)
		if err := rows.Scan(&pid, &title, &difficulty, &acceptance, &link, &total, &cid, &cname, &freq); err != nil {
			http.Error(w, "failed to decode top problems", http.StatusInternalServerError)
			return
		}

		p, ok := byID[pid]
		if !ok {
			p = &topProblem{
				ID:             pid,
				Title:          title,
				Difficulty:     difficulty,
				AcceptanceRate: acceptance,
				Link:           link,
				TotalFrequency: total,
				Companies:      make([]topCompany, 0, 8),
			}
			byID[pid] = p
			order = append(order, pid)
		}

		if cid.Valid && cname.Valid {
			p.Companies = append(p.Companies, topCompany{
				ID:        int(cid.Int64),
				Name:      cname.String,
				Logo:      logoPath(cname.String),
				Frequency: freq,
			})
		}
	}
	if rows.Err() != nil {
		http.Error(w, "error iterating top problems", http.StatusInternalServerError)
		return
	}

	items := make([]topProblem, 0, len(order))
	for _, id := range order {
		items = append(items, *byID[id])
	}

	_ = json.NewEncoder(w).Encode(topResponse{Items: items, Total: len(items)})
}

func parseLimit(raw string, defaultValue int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return defaultValue
	}
	if n > 100 {
		return 100
	}
	return n
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
		dbPool, dbErr = pgxpool.New(ctx, dbURL)
	})
	if dbErr != nil {
		return nil, dbErr
	}
	return dbPool, nil
}
