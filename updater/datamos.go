package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func RunDataMos() error {
	apiKey := strings.TrimSpace(os.Getenv("DATAMOS_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("DATAMOS_API_KEY is required")
	}

	// Пока тестовый URL: подставь endpoint из docs data.mos.ru
	baseURL := strings.TrimSpace(os.Getenv("DATAMOS_TEST_URL"))
	if baseURL == "" {
		return fmt.Errorf("DATAMOS_TEST_URL is required for first test")
	}

	limit := getIntEnv("DATAMOS_PAGE_LIMIT", 100)
	offset := getIntEnv("DATAMOS_PAGE_OFFSET", 0)

	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid DATAMOS_TEST_URL: %w", err)
	}

	q := u.Query()
	// В docs проверь точное имя параметра ключа: api_key / apikey.
	q.Set("api_key", apiKey)
	q.Set("$top", strconv.Itoa(limit))
	q.Set("$skip", strconv.Itoa(offset))
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("datamos status %d: %s", resp.StatusCode, truncate(string(body), 1000))
	}

	// Проверка, что ответ JSON
	var js any
	if err := json.Unmarshal(body, &js); err != nil {
		return fmt.Errorf("invalid JSON response: %w; body: %s", err, truncate(string(body), 500))
	}

	// Пока пишем sample в файл внутри контейнера (для отладки)
	if err := os.WriteFile("/tmp/datamos_sample.json", body, 0o644); err != nil {
		return fmt.Errorf("write sample failed: %w", err)
	}

	fmt.Printf("DataMos sample saved: %d bytes\n", len(body))
	return nil
}

func getIntEnv(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}