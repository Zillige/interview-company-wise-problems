package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := ensureSchema(ctx, pool); err != nil {
		log.Fatalf("failed to create schema: %v", err)
	}

	companies, err := os.ReadDir("data")
	if err != nil {
		log.Fatalf("failed to read data directory: %v", err)
	}

	sort.Slice(companies, func(i, j int) bool {
		return strings.ToLower(companies[i].Name()) < strings.ToLower(companies[j].Name())
	})

	for _, entry := range companies {
		if !entry.IsDir() {
			continue
		}

		company := strings.TrimSpace(entry.Name())
		if company == "" {
			continue
		}
		count, err := seedCompany(ctx, pool, company)
		if err != nil {
			log.Printf("failed to seed %s: %v", company, err)
			continue
		}

		log.Printf("Seeded %s: %d problems", company, count)
	}
}

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS companies (
		    id SERIAL PRIMARY KEY,
		    name TEXT NOT NULL UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS problems (
		    id SERIAL PRIMARY KEY,
		    title TEXT NOT NULL UNIQUE,
		    difficulty TEXT NOT NULL CHECK (difficulty IN ('Easy', 'Medium', 'Hard')),
		    acceptance_rate NUMERIC(5,2),
		    link TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS company_problems (
		    company_id INT REFERENCES companies(id) ON DELETE CASCADE,
		    problem_id INT REFERENCES problems(id) ON DELETE CASCADE,
		    frequency NUMERIC(6,2),
		    PRIMARY KEY (company_id, problem_id)
		)`,
	}

	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

func seedCompany(ctx context.Context, pool *pgxpool.Pool, company string) (int, error) {
	companyID, err := upsertCompany(ctx, pool, company)
	if err != nil {
		return 0, fmt.Errorf("company upsert: %w", err)
	}

	csvPath, err := locateAllCSV(filepath.Join("data", company))
	if err != nil {
		return 0, err
	}

	file, err := os.Open(csvPath)
	if err != nil {
		return 0, fmt.Errorf("open csv: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	count := 0
	headerSkipped := false

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("read csv: %w", err)
		}

		if !headerSkipped {
			if looksLikeHeader(record) {
				headerSkipped = true
				continue
			}
			headerSkipped = true
		}

		if len(record) < 5 {
			continue
		}

		difficulty := normalizeDifficulty(record[0])
		if difficulty == "" {
			continue
		}

		title := strings.TrimSpace(record[1])
		if title == "" {
			continue
		}

		freq, freqOk := parseNumeric(record[2])
		accept, acceptOk := parseNumeric(record[3])
		link := strings.TrimSpace(record[4])
		if link == "" {
			continue
		}

		problemID, err := upsertProblem(ctx, pool, title, difficulty, accept, acceptOk, link)
		if err != nil {
			return count, fmt.Errorf("problem upsert: %w", err)
		}

		if err := upsertCompanyProblem(ctx, pool, companyID, problemID, freq, freqOk); err != nil {
			return count, fmt.Errorf("link problem: %w", err)
		}

		count++
	}

	return count, nil
}

func locateAllCSV(companyDir string) (string, error) {
	entries, err := os.ReadDir(companyDir)
	if err != nil {
		return "", fmt.Errorf("read company dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.Contains(name, "all.csv") {
			return filepath.Join(companyDir, entry.Name()), nil
		}
	}

	return "", fmt.Errorf("all.csv not found for %s", companyDir)
}

func upsertCompany(ctx context.Context, pool *pgxpool.Pool, name string) (int, error) {
	var id int
	err := pool.QueryRow(ctx, `
INSERT INTO companies (name)
VALUES ($1)
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id`, name).Scan(&id)
	return id, err
}

func upsertProblem(ctx context.Context, pool *pgxpool.Pool, title, difficulty string, acceptance float64, hasAcceptance bool, link string) (int, error) {
	var id int
	var acceptanceValue interface{}
	if hasAcceptance {
		acceptanceValue = acceptance
	}

	err := pool.QueryRow(ctx, `
INSERT INTO problems (title, difficulty, acceptance_rate, link)
VALUES ($1, $2, $3, $4)
ON CONFLICT (title) DO UPDATE SET
    difficulty = EXCLUDED.difficulty,
    acceptance_rate = COALESCE(EXCLUDED.acceptance_rate, problems.acceptance_rate),
    link = EXCLUDED.link
RETURNING id`, title, difficulty, acceptanceValue, link).Scan(&id)
	return id, err
}

func upsertCompanyProblem(ctx context.Context, pool *pgxpool.Pool, companyID, problemID int, frequency float64, hasFrequency bool) error {
	var freqValue interface{}
	if hasFrequency {
		freqValue = frequency
	}

	_, err := pool.Exec(ctx, `
INSERT INTO company_problems (company_id, problem_id, frequency)
VALUES ($1, $2, $3)
ON CONFLICT (company_id, problem_id) DO UPDATE SET
    frequency = COALESCE(EXCLUDED.frequency, company_problems.frequency)
`, companyID, problemID, freqValue)
	return err
}

func parseNumeric(raw string) (float64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	value = strings.TrimSuffix(value, "%")
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func normalizeDifficulty(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "easy":
		return "Easy"
	case "medium":
		return "Medium"
	case "hard":
		return "Hard"
	default:
		return ""
	}
}

func looksLikeHeader(record []string) bool {
	if len(record) < 2 {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(record[0]), "difficulty") && strings.EqualFold(strings.TrimSpace(record[1]), "title")
}
