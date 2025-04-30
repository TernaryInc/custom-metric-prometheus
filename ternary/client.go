package ternary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
)

// Client represents a Ternary API client
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// CustomMetric represents a custom metric in the Ternary API
type CustomMetric struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TenantID    string `json:"tenantId"`
	CSVData     string `json:"csvData"`
}

// NewClient creates a new Ternary API client
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{},
	}
}

// do performs an HTTP request and unmarshals the response into v
func (c *Client) do(req *http.Request, v interface{}) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}
	}

	return nil
}

// ListCustomMetrics retrieves all custom metrics for the tenant
func (c *Client) ListCustomMetrics() ([]CustomMetric, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/custom-metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	var result []CustomMetric
	if err := c.do(req, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// CreateCustomMetric creates a new custom metric
func (c *Client) CreateCustomMetric(metric *CustomMetric) error {
	body, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("error marshaling request: %v", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/custom-metrics", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	return c.do(req, metric)
}

// UpdateCustomMetric updates an existing custom metric
func (c *Client) UpdateCustomMetric(id string, metric *CustomMetric) error {
	body, err := json.Marshal(metric)
	if err != nil {
		return fmt.Errorf("error marshaling request: %v", err)
	}

	endpoint := path.Join("/api/custom-metrics", id)
	req, err := http.NewRequest("PATCH", c.baseURL+endpoint, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	return c.do(req, metric)
}

// FindOrCreateMetric finds a custom metric by name or creates it if it doesn't exist
func (c *Client) FindOrCreateMetric(name, description, tenantID, csvData string) (*CustomMetric, error) {
	metrics, err := c.ListCustomMetrics()
	if err != nil {
		return nil, fmt.Errorf("error listing metrics: %v", err)
	}

	// Look for existing metric
	for _, m := range metrics {
		if m.Name == name && m.TenantID == tenantID {
			// Update existing metric with new CSV data
			m.CSVData = csvData
			if err := c.UpdateCustomMetric(m.ID, &m); err != nil {
				return nil, fmt.Errorf("error updating metric: %v", err)
			}
			return &m, nil
		}
	}

	// Create new metric
	newMetric := &CustomMetric{
		Name:        name,
		Description: description,
		TenantID:    tenantID,
		CSVData:     csvData,
	}

	if err := c.CreateCustomMetric(newMetric); err != nil {
		return nil, fmt.Errorf("error creating metric: %v", err)
	}

	return newMetric, nil
}
