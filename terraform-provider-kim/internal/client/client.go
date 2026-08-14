package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base     *url.URL
	token    string
	clientID string
	http     *http.Client
	poll     time.Duration
}
type Config struct {
	Endpoint, Token, CACertificate, ClientID string
	InsecureSkipVerify                       bool
	Timeout, PollInterval                    time.Duration
}
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	RequestID string `json:"requestId"`
	Status    int    `json:"status"`
	Retryable bool   `json:"retryable"`
}

func (p *Problem) Error() string {
	return fmt.Sprintf("KIM %s: %s (request_id=%s)", p.Code, p.Detail, p.RequestID)
}

type Response struct {
	ETag, RequestID, Location string
	Status                    int
}

func New(c Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(c.Endpoint, "/"))
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || c.Token == "" || c.ClientID == "" || len(c.ClientID) > 128 || c.Timeout <= 0 {
		return nil, errors.New("complete KIM endpoint, token, client identity, and positive timeout are required")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipVerify}
	if c.CACertificate != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM([]byte(c.CACertificate)) {
			return nil, errors.New("KIM CA certificate contains no certificate")
		}
		tlsConfig.RootCAs = roots
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	return &Client{base: u, token: c.Token, clientID: c.ClientID, http: &http.Client{Timeout: c.Timeout, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, poll: c.PollInterval}, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any, headers map[string]string) (Response, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
	}
	attempts := 1
	if method == http.MethodPost && headers["Idempotency-Key"] != "" {
		attempts = 2
	}
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, bytes.NewReader(raw))
		if err != nil {
			return Response{}, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := c.http.Do(req)
		if err != nil {
			if attempt+1 < attempts && ctx.Err() == nil {
				continue
			}
			return Response{}, fmt.Errorf("KIM transport: %w", err)
		}
		result := Response{Status: res.StatusCode, ETag: res.Header.Get("ETag"), RequestID: res.Header.Get("X-Request-ID"), Location: res.Header.Get("Location")}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		if readErr != nil {
			return result, readErr
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out != nil && len(data) > 0 {
				if err := json.Unmarshal(data, out); err != nil {
					return result, fmt.Errorf("decode KIM response: %w", err)
				}
			}
			return result, nil
		}
		var p Problem
		if json.Unmarshal(data, &p) != nil || p.Code == "" {
			p = Problem{Status: res.StatusCode, Code: "INTERNAL_ERROR", Detail: "KIM returned an invalid error response", RequestID: result.RequestID}
		}
		if p.RequestID == "" {
			p.RequestID = result.RequestID
		}
		return result, &p
	}
	panic("unreachable")
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
}

func ListPage[T any](ctx context.Context, c *Client, path, cursor string, limit int) (Page[T], error) {
	if limit < 1 || limit > 100 {
		return Page[T]{}, errors.New("KIM page limit must be between 1 and 100")
	}
	query := url.Values{"limit": []string{strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	var page Page[T]
	_, err := c.Do(ctx, http.MethodGet, path+separator+query.Encode(), nil, &page, nil)
	return page, err
}

func Retryable(err error) bool {
	var problem *Problem
	return errors.As(err, &problem) && problem.Retryable
}

func RevisionFromETag(etag string, fallback int64) int64 {
	if len(etag) >= 3 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		if v, err := strconv.ParseInt(etag[1:len(etag)-1], 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}
func IdempotencyKey(kind string, desired any) (string, error) {
	raw, err := json.Marshal(desired)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(kind+"\x00"), raw...))
	return "tf-" + hex.EncodeToString(sum[:]), nil
}

// CreateIdempotencyKey reconstructs a stable client-owned Create identity.
// KIM, not the Provider, binds that identity to the exact desired digest and
// rejects conflicting intent.
func (c *Client) CreateIdempotencyKey(kind, clientReference string, desired any) (string, error) {
	if c == nil || c.clientID == "" || clientReference == "" || len(clientReference) > 255 {
		return "", errors.New("complete client create identity is required")
	}
	if _, err := json.Marshal(desired); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + c.clientID + "\x00" + clientReference))
	return "tf-" + hex.EncodeToString(sum[:]), nil
}

type Operation struct {
	ID                 string  `json:"id"`
	Type               string  `json:"type"`
	TargetResourceType string  `json:"targetResourceType"`
	TargetResourceID   string  `json:"targetResourceId"`
	Phase              string  `json:"phase"`
	TargetRevision     int64   `json:"targetRevision"`
	TerminalState      *string `json:"terminalState"`
	ErrorCode          *string `json:"errorCode"`
	Retryable          bool    `json:"retryable"`
	Cancellable        bool    `json:"cancellable"`
}

func (c *Client) WaitOperation(ctx context.Context, id string) (Operation, error) {
	timer := time.NewTicker(c.poll)
	defer timer.Stop()
	for {
		var op Operation
		_, err := c.Do(ctx, http.MethodGet, "/operations/"+id, nil, &op, nil)
		if err != nil {
			return op, err
		}
		switch op.Phase {
		case "SUCCEEDED":
			return op, nil
		case "FAILED", "CANCELLED":
			code := op.Phase
			if op.ErrorCode != nil {
				code = *op.ErrorCode
			}
			return op, fmt.Errorf("KIM operation %s ended %s", id, code)
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-timer.C:
		}
	}
}
