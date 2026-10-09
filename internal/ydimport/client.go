package ydimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const apiBase = "https://cloud-api.yandex.net/v1/disk/public/resources"

type Client struct {
	http      *http.Client
	publicKey string
}

func NewClient(publicKey string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		publicKey: publicKey,
	}
}

type Meta struct {
	SHA256   string    `json:"sha256"`
	Modified time.Time `json:"modified"`
}

func (c *Client) Meta(ctx context.Context) (Meta, error) {
	var m Meta
	params := url.Values{"fields": {"sha256,modified"}}
	if err := c.getJSON(ctx, apiBase, params, &m); err != nil {
		return Meta{}, fmt.Errorf("meta: %w", err)
	}
	if m.SHA256 == "" {
		return Meta{}, errors.New("meta: public link has no sha256: is it a folder?")
	}
	return m, nil
}

// Download returns the file's contents. The caller must close the body.
func (c *Client) Download(ctx context.Context) (io.ReadCloser, error) {
	var d struct {
		Href string `json:"href"`
	}
	if err := c.getJSON(ctx, apiBase+"/download", nil, &d); err != nil {
		return nil, fmt.Errorf("download link: %w", err)
	}
	if d.Href == "" {
		return nil, errors.New("download link: empty href")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Href, nil)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("download: %s", statusError(resp))
	}
	return resp.Body, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, params url.Values, v any) error {
	q := url.Values{}
	for k, vs := range params {
		q[k] = vs
	}
	q.Set("public_key", c.publicKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New(statusError(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// statusError describes a non-200 response, including the start of its body
// (Yandex puts a readable "description" there).
func statusError(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Sprintf("%s: %s", resp.Status, body)
}
