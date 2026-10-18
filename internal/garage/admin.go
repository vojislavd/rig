package garage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Admin talks to Garage's admin API (v2) on loopback with the admin token.
type Admin struct {
	Addr  string // host:port, normally AdminAddr
	Token string
	HTTP  *http.Client
}

// NewAdmin returns a client for the local Garage.
func NewAdmin(addr, token string) *Admin {
	return &Admin{Addr: addr, Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (a *Admin) call(method, endpoint string, query url.Values, in, out any) error {
	u := url.URL{Scheme: "http", Host: a.Addr, Path: "/v2/" + endpoint, RawQuery: query.Encode()}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Endpoint: endpoint, Status: resp.StatusCode, Body: string(data)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// APIError is a non-200 answer from the admin API.
type APIError struct {
	Endpoint string
	Status   int
	Body     string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("garage %s: HTTP %d: %.200s", e.Endpoint, e.Status, e.Body)
}

// IsNotFound reports whether err is a 404 from the admin API.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Node is one entry of GetClusterStatus.
type Node struct {
	ID   string           `json:"id"`
	IsUp bool             `json:"isUp"`
	Role *json.RawMessage `json:"role"` // null until the layout gives it one
}

// Status is the part of GetClusterStatus rig uses.
type Status struct {
	LayoutVersion int    `json:"layoutVersion"`
	Nodes         []Node `json:"nodes"`
}

func (a *Admin) Status() (Status, error) {
	var s Status
	err := a.call(http.MethodGet, "GetClusterStatus", nil, nil, &s)
	return s, err
}

// capacity is the size Garage plans with for the single node. Garage uses
// it to place data; it is not a hard quota.
const capacity = 100_000_000_000 // 100 GB

// InitLayout gives the single node a storage role if it has none. Safe to
// call again: a node that already has a role is left alone.
func (a *Admin) InitLayout() error {
	s, err := a.Status()
	if err != nil {
		return err
	}
	if len(s.Nodes) != 1 {
		return fmt.Errorf("expected one Garage node, found %d", len(s.Nodes))
	}
	n := s.Nodes[0]
	if n.Role != nil && string(*n.Role) != "null" {
		return nil
	}
	roles := map[string]any{"roles": []map[string]any{{"id": n.ID, "zone": "local", "capacity": capacity, "tags": []string{}}}}
	if err := a.call(http.MethodPost, "UpdateClusterLayout", nil, roles, nil); err != nil {
		return err
	}
	return a.call(http.MethodPost, "ApplyClusterLayout", nil, map[string]int{"version": s.LayoutVersion + 1}, nil)
}

// Bucket returns the ID of the bucket with this global alias, creating it
// if it doesn't exist.
func (a *Admin) Bucket(alias string) (string, error) {
	var b struct {
		ID string `json:"id"`
	}
	err := a.call(http.MethodGet, "GetBucketInfo", url.Values{"globalAlias": {alias}}, nil, &b)
	if IsNotFound(err) {
		err = a.call(http.MethodPost, "CreateBucket", nil, map[string]string{"globalAlias": alias}, &b)
	}
	return b.ID, err
}

// Key returns the access key ID and secret for the key with this name,
// creating it if it doesn't exist. The secret is fetched from Garage, so
// running this again returns the same key rather than a new one.
func (a *Admin) Key(name string) (id, secret string, err error) {
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := a.call(http.MethodGet, "ListKeys", nil, nil, &list); err != nil {
		return "", "", err
	}
	var k struct {
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	for _, l := range list {
		if l.Name == name {
			err := a.call(http.MethodGet, "GetKeyInfo", url.Values{"id": {l.ID}, "showSecretKey": {"true"}}, nil, &k)
			return k.AccessKeyID, k.SecretAccessKey, err
		}
	}
	err = a.call(http.MethodPost, "CreateKey", nil, map[string]string{"name": name}, &k)
	return k.AccessKeyID, k.SecretAccessKey, err
}

// Allow gives the key read, write and owner rights on the bucket, and
// nothing else.
func (a *Admin) Allow(bucketID, keyID string) error {
	body := map[string]any{
		"bucketId":    bucketID,
		"accessKeyId": keyID,
		"permissions": map[string]bool{"read": true, "write": true, "owner": true},
	}
	return a.call(http.MethodPost, "AllowBucketKey", nil, body, nil)
}

// WaitReady polls until the admin API answers or the timeout passes.
func (a *Admin) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := a.Status()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("garage did not become ready: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
