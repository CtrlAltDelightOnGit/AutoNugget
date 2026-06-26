package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// sendNotification POSTs a webhook message. Returns nil if webhookURL is empty.
// Callers must log errors but never fail the download on notification error (DEC-003).
func sendNotification(webhookURL, webhookType, message string) error {
	if webhookURL == "" {
		return nil
	}
	var payload map[string]string
	switch webhookType {
	case "slack":
		payload = map[string]string{"text": message}
	case "generic":
		payload = map[string]string{"message": message}
	default: // "discord" or unrecognized
		payload = map[string]string{"content": message}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
