package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// healthcheck performs one probe against a running instance. It exists so the
// distroless container image, which ships no shell or curl, can use the server
// binary itself as a Docker health check.
func healthcheck(rawURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("health probe %s: %w", rawURL, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health probe %s: unexpected status %d", rawURL, response.StatusCode)
	}
	return nil
}
