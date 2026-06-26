package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"atlas-api/internal/config"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
)

func main() {
	// Load environment variables
	config.LoadEnv(".env")

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/generate-token/main.go [superadmin|<tenant_id>]")
		os.Exit(1)
	}

	tenantID := os.Args[1]

	if tenantID == "superadmin" {
		secret := os.Getenv("SUPERADMIN_JWT_SECRET")
		if secret == "" {
			log.Fatal("SUPERADMIN_JWT_SECRET is not set in environment")
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"role": "SUPERADMIN",
		})

		tokenString, err := token.SignedString([]byte(secret))
		if err != nil {
			log.Fatalf("Failed to generate superadmin token: %v", err)
		}

		fmt.Println(tokenString)
		return
	}

	// For standard tenant, look up the secret in master.tenant table
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set in environment")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	var secret string
	err = db.QueryRow("SELECT secret FROM master.tenant WHERE id = $1", tenantID).Scan(&secret)
	if err == sql.ErrNoRows {
		log.Fatalf("Tenant '%s' not found in database", tenantID)
	} else if err != nil {
		log.Fatalf("Database query failed: %v", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": tenantID,
		"role":      "USER",
	})

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		log.Fatalf("Failed to generate tenant token: %v", err)
	}

	fmt.Println(tokenString)
}
