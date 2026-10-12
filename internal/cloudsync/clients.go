package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiBase = "https://cloud-api.yandex.net/v1/disk/public/resources"

type YDClient struct {
	http      *http.Client
	publicKey string
}

func NewYDClient(publicKey string) *YDClient {
	return &YDClient{
		http:      &http.Client{Timeout: 30 * time.Second},
		publicKey: publicKey,
	}
}

// Fetch returns the file's contents. The caller must close the body.
func (c *YDClient) Fetch(ctx context.Context) (io.ReadCloser, error) {
	var d struct {
		Href string `json:"href"`
	}
	if err := c.getJSON(ctx, apiBase+"/download", nil, &d); err != nil {
		return nil, fmt.Errorf("download link: %w", err)
	}
	if d.Href == "" {
		return nil, errors.New("download link: empty href")
	}

	resp, err := download(ctx, c.http, d.Href)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "YD", err)
	}
	return resp.Body, nil
}

func (c *YDClient) getJSON(ctx context.Context, endpoint string, params url.Values, v any) error {
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

type GDClient struct {
	http        *http.Client
	downloadURL string
}

func NewGDClient(link string) (*GDClient, error) {
	downloadURL, err := googleDownloadURL(link)
	if err != nil {
		return nil, fmt.Errorf("GD client construction: %w", err)
	}
	return &GDClient{
		http:        &http.Client{Timeout: 30 * time.Second},
		downloadURL: downloadURL,
	}, nil
}

func (c *GDClient) Fetch(ctx context.Context) (io.ReadCloser, error) {
	resp, err := download(ctx, c.http, c.downloadURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "GD", err)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		resp.Body.Close()
		return nil, errors.New(`Google returned a web page instead of the file: is it shared as "Anyone with the link"?`)
	}
	return resp.Body, nil
}

func download(ctx context.Context, client *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("download: %s", statusError(resp))
	}
	return resp, nil
}

func googleDownloadURL(link string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", fmt.Errorf("invalid link: %w", err)
	}
	id := u.Query().Get("id")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "d" {
			id = parts[i+1]
			break
		}
	}
	if id == "" {
		return "", errors.New("no file ID in the link")
	}

	switch {
	case u.Host == "docs.google.com" && len(parts) > 0 && parts[0] == "spreadsheets":
		return "https://docs.google.com/spreadsheets/d/" + url.PathEscape(id) + "/export?format=xlsx", nil
	case u.Host == "drive.google.com":
		return "https://drive.google.com/uc?" + url.Values{"export": {"download"}, "id": {id}}.Encode(), nil
	}
	return "", errors.New("not a Google Drive or Google Sheets link")
}
