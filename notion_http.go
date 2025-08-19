package main

import (
	"bytes"
	"io"
	"net/http"
)

// NotionHTTP wraps HTTP logic for Notion API
// It sets required headers and exposes helper methods
// for POST, PATCH, PUT, GET requests.
type NotionHTTP struct {
	Token   string
	Version string
	Client  *http.Client
}

func NewNotionHTTP(token, version string) *NotionHTTP {
	return &NotionHTTP{
		Token:   token,
		Version: version,
		Client:  &http.Client{},
	}
}

func (n *NotionHTTP) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+n.Token)
	req.Header.Set("Notion-Version", n.Version)
}

func (n *NotionHTTP) Post(url string, body []byte, contentType string) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	n.setHeaders(req)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return n.Client.Do(req)
}

func (n *NotionHTTP) Patch(url string, body []byte, contentType string) (*http.Response, error) {
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	n.setHeaders(req)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return n.Client.Do(req)
}

func (n *NotionHTTP) Put(url string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest("PUT", url, body)
	if err != nil {
		return nil, err
	}
	n.setHeaders(req)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return n.Client.Do(req)
}

func (n *NotionHTTP) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	n.setHeaders(req)
	return n.Client.Do(req)
}
