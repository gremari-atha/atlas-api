package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"atlas-api/internal/config"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

func main() {
	config.LoadEnv(".env")

	if len(os.Args) < 3 {
		log.Fatal("Usage: go run cmd/migrate/main.go [master|tenant] [up|down]")
	}

	targetSchema := os.Args[1]
	command := os.Args[2]
	dbURL := os.Getenv("DATABASE_URL")

	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Verify and enable TimescaleDB extension
	checkAndEnableTimescaleDB(dbURL)

	switch targetSchema {
	case "master":
		runMasterMigrate(dbURL, command)
	case "tenant":
		runTenantMigrate(dbURL, command)
	default:
		log.Fatalf("Unsupported target schema '%s'. Allowed: master, tenant", targetSchema)
	}
}

func checkAndEnableTimescaleDB(dbURL string) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database for TimescaleDB check: %v", err)
	}
	defer db.Close()

	// 1. Check if timescaledb extension is already enabled
	var enabled bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')").Scan(&enabled)
	if err != nil {
		log.Fatalf("Failed to query pg_extension: %v", err)
	}

	if enabled {
		log.Println("TimescaleDB extension is already enabled.")
		return
	}

	// 2. Check if timescaledb extension is available on the server
	var available bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb')").Scan(&available)
	if err != nil {
		log.Fatalf("Failed to query pg_available_extensions: %v", err)
	}

	if !available {
		log.Fatal("TimescaleDB extension is not available/installed on this PostgreSQL server. This application requires TimescaleDB.")
	}

	// 3. Enable TimescaleDB extension
	log.Println("Enabling TimescaleDB extension...")
	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS timescaledb")
	if err != nil {
		log.Fatalf("Failed to enable TimescaleDB extension: %v", err)
	}
	log.Println("TimescaleDB extension enabled successfully.")
}

func runMasterMigrate(dbURL, command string) {
	// Ensure master schema exists
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database to prepare master schema: %v", err)
	}
	_, err = db.Exec("CREATE SCHEMA IF NOT EXISTS master")
	if err != nil {
		db.Close()
		log.Fatalf("Failed to create master schema: %v", err)
	}
	db.Close()

	// Append search_path parameter to connection URL
	urlWithSchema := dbURL
	u, err := url.Parse(dbURL)
	if err == nil {
		q := u.Query()
		q.Set("search_path", "master,public")
		u.RawQuery = q.Encode()
		urlWithSchema = u.String()
	} else {
		if strings.Contains(dbURL, "?") {
			urlWithSchema += "&search_path=master,public"
		} else {
			urlWithSchema += "?search_path=master,public"
		}
	}

	m, err := migrate.New("file://migrations/master", urlWithSchema)
	if err != nil {
		log.Fatalf("Failed to initialize master migration: %v", err)
	}
	defer m.Close()

	executeMigrateCommand(m, "master", command)
}

func runTenantMigrate(dbURL, command string) {
	// Query active tenants from master.tenant table
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Direct query from master.tenant
	rows, err := db.Query("SELECT id FROM master.tenant")
	if err != nil {
		log.Fatalf("Failed to retrieve tenants from master: %v", err)
	}
	defer rows.Close()

	var tenants []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			tenants = append(tenants, id)
		}
	}

	if len(tenants) == 0 {
		fmt.Println("No active tenants found for migrations.")
		return
	}

	for _, tenantID := range tenants {
		fmt.Printf("Running migration for tenant '%s'...\n", tenantID)

		// Ensure tenant schema exists
		_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, tenantID))
		if err != nil {
			log.Printf("Failed to create schema for tenant %s: %v", tenantID, err)
			continue
		}

		urlWithSchema := dbURL
		u, err := url.Parse(dbURL)
		if err == nil {
			q := u.Query()
			q.Set("search_path", tenantID+",public")
			u.RawQuery = q.Encode()
			urlWithSchema = u.String()
		} else {
			if strings.Contains(dbURL, "?") {
				urlWithSchema += fmt.Sprintf("&search_path=%s,public", tenantID)
			} else {
				urlWithSchema += fmt.Sprintf("?search_path=%s,public", tenantID)
			}
		}

		m, err := migrate.New("file://migrations/tenant", urlWithSchema)
		if err != nil {
			log.Printf("Failed to initialize migration for tenant %s: %v", tenantID, err)
			continue
		}

		executeMigrateCommand(m, fmt.Sprintf("tenant (%s)", tenantID), command)
		m.Close()
	}
}

func executeMigrateCommand(m *migrate.Migrate, label, command string) {
	var err error
	if command == "up" {
		err = m.Up()
	} else if command == "down" {
		err = m.Down()
	}

	if err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Error during migration %s %s: %v", label, command, err)
	}
	fmt.Printf("Migration %s %s completed successfully.\n", label, command)
}
