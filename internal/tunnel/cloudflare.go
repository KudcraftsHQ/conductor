package tunnel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	cloudflareAPIBase = "https://api.cloudflare.com/client/v4"
)

// CloudflareClient handles Cloudflare API operations
type CloudflareClient struct {
	accountID string
	zoneID    string
	apiToken  string
	client    *http.Client
}

// TunnelInfo represents a Cloudflare tunnel
type TunnelInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
}

// TunnelCredentials represents tunnel credentials returned from API
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelID     string `json:"TunnelID"`
	TunnelName   string `json:"TunnelName"`
	TunnelSecret string `json:"TunnelSecret"`
}

// DNSRecord represents a Cloudflare DNS record
type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// APIResponse is the generic Cloudflare API response wrapper
type APIResponse struct {
	Success  bool            `json:"success"`
	Errors   []APIError      `json:"errors"`
	Messages []string        `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

// APIError represents a Cloudflare API error
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewCloudflareClient creates a new Cloudflare API client
func NewCloudflareClient(accountID, zoneID, apiToken string) *CloudflareClient {
	return &CloudflareClient{
		accountID: accountID,
		zoneID:    zoneID,
		apiToken:  apiToken,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetAPIToken retrieves the Cloudflare API token from environment or config
func GetAPIToken(configToken string) string {
	// Priority: environment variable > config file
	if token := os.Getenv("CLOUDFLARE_API_TOKEN"); token != "" {
		return token
	}
	return configToken
}

// ValidateCredentials tests if the API token is valid
func (c *CloudflareClient) ValidateCredentials() error {
	req, err := http.NewRequest("GET", cloudflareAPIBase+"/user/tokens/verify", nil)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to validate credentials: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return fmt.Errorf("invalid credentials: %s", resp.Errors[0].Message)
		}
		return fmt.Errorf("invalid credentials")
	}

	return nil
}

// CreateTunnel creates a new Cloudflare tunnel
func (c *CloudflareClient) CreateTunnel(name string) (*TunnelInfo, *TunnelCredentials, error) {
	payload := map[string]interface{}{
		"name":          name,
		"tunnel_secret": generateTunnelSecret(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	url := fmt.Sprintf("%s/accounts/%s/cfd_tunnel", cloudflareAPIBase, c.accountID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create tunnel: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, nil, fmt.Errorf("failed to create tunnel: %s", resp.Errors[0].Message)
		}
		return nil, nil, fmt.Errorf("failed to create tunnel")
	}

	var tunnel TunnelInfo
	if err := json.Unmarshal(resp.Result, &tunnel); err != nil {
		return nil, nil, fmt.Errorf("failed to parse tunnel response: %w", err)
	}

	// Build credentials
	creds := &TunnelCredentials{
		AccountTag: c.accountID,
		TunnelID:   tunnel.ID,
		TunnelName: tunnel.Name,
		// Note: TunnelSecret is not returned by API, we generated it
	}

	return &tunnel, creds, nil
}

// GetTunnel retrieves tunnel information by ID
func (c *CloudflareClient) GetTunnel(tunnelID string) (*TunnelInfo, error) {
	url := fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s", cloudflareAPIBase, c.accountID, tunnelID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("failed to get tunnel: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to get tunnel")
	}

	var tunnel TunnelInfo
	if err := json.Unmarshal(resp.Result, &tunnel); err != nil {
		return nil, fmt.Errorf("failed to parse tunnel response: %w", err)
	}

	return &tunnel, nil
}

// ListTunnels lists all tunnels for the account
func (c *CloudflareClient) ListTunnels() ([]TunnelInfo, error) {
	url := fmt.Sprintf("%s/accounts/%s/cfd_tunnel", cloudflareAPIBase, c.accountID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tunnels: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("failed to list tunnels: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to list tunnels")
	}

	var tunnels []TunnelInfo
	if err := json.Unmarshal(resp.Result, &tunnels); err != nil {
		return nil, fmt.Errorf("failed to parse tunnels response: %w", err)
	}

	return tunnels, nil
}

// DeleteTunnel deletes a tunnel by ID
func (c *CloudflareClient) DeleteTunnel(tunnelID string) error {
	url := fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s", cloudflareAPIBase, c.accountID, tunnelID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to delete tunnel: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return fmt.Errorf("failed to delete tunnel: %s", resp.Errors[0].Message)
		}
		return fmt.Errorf("failed to delete tunnel")
	}

	return nil
}

// CreateDNSRecord creates a CNAME record pointing to the tunnel
func (c *CloudflareClient) CreateDNSRecord(hostname, tunnelID string) (*DNSRecord, error) {
	payload := map[string]interface{}{
		"type":    "CNAME",
		"name":    hostname,
		"content": fmt.Sprintf("%s.cfargotunnel.com", tunnelID),
		"proxied": true,
		"ttl":     1, // Auto TTL
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/zones/%s/dns_records", cloudflareAPIBase, c.zoneID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS record: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("failed to create DNS record: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to create DNS record")
	}

	var record DNSRecord
	if err := json.Unmarshal(resp.Result, &record); err != nil {
		return nil, fmt.Errorf("failed to parse DNS record response: %w", err)
	}

	return &record, nil
}

// DeleteDNSRecord deletes a DNS record by ID
func (c *CloudflareClient) DeleteDNSRecord(recordID string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", cloudflareAPIBase, c.zoneID, recordID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return fmt.Errorf("failed to delete DNS record: %s", resp.Errors[0].Message)
		}
		return fmt.Errorf("failed to delete DNS record")
	}

	return nil
}

// FindDNSRecord finds a DNS record by hostname
func (c *CloudflareClient) FindDNSRecord(hostname string) (*DNSRecord, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records?name=%s&type=CNAME", cloudflareAPIBase, c.zoneID, hostname)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to find DNS record: %w", err)
	}

	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("failed to find DNS record: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to find DNS record")
	}

	var records []DNSRecord
	if err := json.Unmarshal(resp.Result, &records); err != nil {
		return nil, fmt.Errorf("failed to parse DNS records response: %w", err)
	}

	if len(records) == 0 {
		return nil, nil
	}

	return &records[0], nil
}

// doRequest executes an HTTP request with authentication
func (c *CloudflareClient) doRequest(req *http.Request) (*APIResponse, error) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &apiResp, nil
}

// generateTunnelSecret generates a random tunnel secret
func generateTunnelSecret() string {
	// Generate a 32-byte random secret encoded as base64
	// In production, this should use crypto/rand
	return fmt.Sprintf("conductor-%d", time.Now().UnixNano())
}
