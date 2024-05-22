// Copyright 2024 The OSS Rebuild Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package http provides a simpler http.Client abstraction and derivative uses.
package http

import (
	"bufio"
	"bytes"
	"errors"
	"net/http"

	"github.com/google/oss-rebuild/internal/cache"
)

// BasicClient is a simpler http.Client that only requires a Do method.
type BasicClient interface {
	Do(*http.Request) (*http.Response, error)
}

var _ BasicClient = http.DefaultClient

// WithUserAgent is a basic HTTP client that adds a User-Agent header.
type WithUserAgent struct {
	BasicClient
	UserAgent string
}

var _ BasicClient = &WithUserAgent{}

// Do adds the User-Agent header and sends the request.
func (c *WithUserAgent) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", c.UserAgent)
	return c.BasicClient.Do(req)
}

// CachedClient is a BasicClient that caches responses.
type CachedClient struct {
	BasicClient
	ch cache.Cache
}

// NewCachedClient returns a new CachedClient.
func NewCachedClient(client BasicClient, c cache.Cache) *CachedClient {
	return &CachedClient{client, c}
}

// Do attempts to fetch from cache (if applicable) or fulfills the request using the underlying client.
func (cc *CachedClient) Do(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return cc.BasicClient.Do(req)
	}
	respBytes, err := cc.ch.GetOrSet(req.URL.String(), func() (any, error) {
		resp, err := cc.BasicClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, errors.New(resp.Status)
		}
		defer resp.Body.Close()
		foo := new(bytes.Buffer)
		if err := resp.Write(foo); err != nil {
			return nil, err
		}
		return foo.Bytes(), nil
	})
	if err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(bytes.NewReader(respBytes.([]byte))), req)
}

var _ BasicClient = &CachedClient{}
