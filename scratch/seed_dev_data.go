package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testTenantID = "paytronik"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/atlas?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer dbPool.Close()

	// Clear previous data
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DELETE FROM "%s".transaction_ts`, testTenantID))
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DELETE FROM "%s".account`, testTenantID))
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DELETE FROM "%s".product_variant`, testTenantID))
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DELETE FROM "%s".product`, testTenantID))
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DELETE FROM "%s".email`, testTenantID))

	// 1. Seed Email
	var emailID int64
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO "%s".email (email, password, created_at, updated_at)
		VALUES ('user.e2e.netflix@gmail.com', 'securepass123', NOW(), NOW())
		RETURNING id
	`, testTenantID)).Scan(&emailID)
	if err != nil {
		log.Fatalf("failed to seed email: %v", err)
	}
	fmt.Printf("Seeded email with ID: %d\n", emailID)

	// 2. Seed Product
	var productID int64
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO "%s".product (name, created_at, updated_at)
		VALUES ('Netflix UHD Premium', NOW(), NOW())
		RETURNING id
	`, testTenantID)).Scan(&productID)
	if err != nil {
		log.Fatalf("failed to seed product: %v", err)
	}
	fmt.Printf("Seeded product with ID: %d\n", productID)

	// 3. Seed Variant
	var variantID int64
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO "%s".product_variant (product_id, name, duration, interval, cooldown, base_price, created_at, updated_at)
		VALUES ($1, '1-Month Sharing', 30, 1, 300000, 150000, NOW(), NOW())
		RETURNING id
	`, testTenantID), productID).Scan(&variantID)
	if err != nil {
		log.Fatalf("failed to seed product variant: %v", err)
	}
	fmt.Printf("Seeded variant with ID: %d\n", variantID)

	// 4. Seed Account
	var accountID int64
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO "%s".account (email_id, product_variant_id, account_password, subscription_expiry, status, billing, created_at, updated_at)
		VALUES ($1, $2, 'secureNetflixPassword123!', NOW() + INTERVAL '30 days', 'active', 'monthly', NOW(), NOW())
		RETURNING id
	`, testTenantID), emailID, variantID).Scan(&accountID)
	if err != nil {
		log.Fatalf("failed to seed account: %v", err)
	}
	fmt.Printf("Seeded account with ID: %d\n", accountID)

	// 5. Seed Transactions (TimescaleDB hypertable)
	// We need 'id', 'customer', 'platform', 'total_price', and 'created_at' for transaction_ts
	_, err = dbPool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".transaction_ts (id, customer, platform, total_price, created_at)
		VALUES 
			('TX-001', 'John Doe', 'Netflix', 150000, NOW() - INTERVAL '1 hour'),
			('TX-002', 'Jane Smith', 'Netflix', 150000, NOW() - INTERVAL '30 minutes')
	`, testTenantID))
	if err != nil {
		log.Fatalf("failed to seed transactions: %v", err)
	}
	fmt.Println("Seeded transactions successfully")

	fmt.Println("Successfully seeded dev sample data into 'paytronik' tenant schema")
}
