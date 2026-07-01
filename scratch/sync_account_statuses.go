package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/atlas?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer dbPool.Close()

	// 1. Fetch all active tenants from master.tenant
	rows, err := dbPool.Query(ctx, `SELECT id FROM master.tenant`)
	if err != nil {
		log.Fatalf("failed to query tenants: %v", err)
	}
	defer rows.Close()

	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			tenantIDs = append(tenantIDs, id)
		}
	}

	fmt.Printf("Found %d tenants to sync: %v\n", len(tenantIDs), tenantIDs)

	// 2. Loop through each tenant and sync account statuses
	for _, tenantID := range tenantIDs {
		fmt.Printf("Syncing tenant: %s...\n", tenantID)
		
		query := fmt.Sprintf(`
			UPDATE "%s".account a
			SET status = CASE
				WHEN EXISTS (
					SELECT 1 FROM "%s".account_user au
					WHERE au.account_id = a.id AND au.status = 'active'
				) THEN 'active'
				ELSE 'ready'
			END,
			batch_end_date = (
				SELECT MAX(expired_at) FROM "%s".account_user au
				WHERE au.account_id = a.id AND au.status = 'active'
			),
			batch_start_date = CASE
				WHEN EXISTS (
					SELECT 1 FROM "%s".account_user au
					WHERE au.account_id = a.id AND au.status = 'active'
				) THEN COALESCE(
					batch_start_date,
					(
						SELECT MIN(created_at) FROM "%s".account_user au
						WHERE au.account_id = a.id AND au.status = 'active'
					),
					NOW()
				)
				ELSE NULL
			END
			WHERE a.status != 'disable';
		`, tenantID, tenantID, tenantID, tenantID, tenantID)

		res, err := dbPool.Exec(ctx, query)
		if err != nil {
			fmt.Printf("Error syncing tenant %s: %v\n", tenantID, err)
			continue
		}
		
		fmt.Printf("Successfully synced tenant %s: %d accounts updated\n", tenantID, res.RowsAffected())
	}

	fmt.Println("All tenant account statuses successfully synchronized!")
}
