package main

import (
	"database/sql"
	"fmt"
	"log"
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

	switch targetSchema {
	case "master":
		runMasterMigrate(dbURL, command)
	case "tenant":
		runTenantMigrate(dbURL, command)
	default:
		log.Fatalf("Unsupported target schema '%s'. Allowed: master, tenant", targetSchema)
	}
}

func runMasterMigrate(dbURL, command string) {
	// Append search_path parameter to connection URL
	urlWithSchema := dbURL
	if strings.Contains(dbURL, "?") {
		urlWithSchema += "&search_path=master,public"
	} else {
		urlWithSchema += "?search_path=master,public"
	}

	m, err := migrate.New("file://db/migrations/master", urlWithSchema)
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
		
		urlWithSchema := dbURL
		if strings.Contains(dbURL, "?") {
			urlWithSchema += fmt.Sprintf("&search_path=%s,public", tenantID)
		} else {
			urlWithSchema += fmt.Sprintf("?search_path=%s,public", tenantID)
		}

		m, err := migrate.New("file://db/migrations/tenant", urlWithSchema)
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
