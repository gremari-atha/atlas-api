package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

const (
	baseURL      = "http://localhost:5000"
	superSecret  = "superadminsecret"
	testTenantID = "paytronik"
	tenantSecret = "paytroniksecret"
)

func main() {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "SUPERADMIN",
	})
	superadminToken, err := token.SignedString([]byte(superSecret))
	if err != nil {
		log.Fatalf("failed to generate token: %v", err)
	}

	createTenantReq := map[string]string{
		"id":     testTenantID,
		"secret": tenantSecret,
	}
	jsonBytes, _ := json.Marshal(createTenantReq)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/tenant", bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "VC "+superadminToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Printf("Error creating tenant: HTTP %d: %s\n", resp.StatusCode, string(respBytes))
		os.Exit(1)
	}

	fmt.Println("Successfully provisioned dev tenant 'paytronik' with secret 'paytroniksecret'")
}
