// Package client is the CLI's transport for the local V2 HTTP service.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const apiPath = "/api/2026-11-10"
const maxResponseBytes = 96 << 20

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,}$`)
var entityTagPattern = regexp.MustCompile(`^"[A-Za-z0-9_-]{16,128}"$`)

type Client struct {
	origin string
	token  string
	http   *http.Client
}

type Response struct {
	JSON json.RawMessage
	ETag string
}

func New(origin, token string) (*Client, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" ||
		parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.Opaque != "" || origin != "http://"+parsed.Host {
		return nil, errors.New("LOCALSWIM_URL must be http://127.0.0.1:<port>")
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return nil, errors.New("LOCALSWIM_URL must specify a port from 1 to 65535")
	}
	if len(token) > 4096 || !tokenPattern.MatchString(token) {
		return nil, errors.New("LOCALSWIM_TOKEN must contain a valid bearer credential")
	}
	return &Client{
		origin: origin,
		token:  token,
		http: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true, DisableCompression: true},
			Timeout:   30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Do(method, resource string, body json.RawMessage, ifMatch string) (Response, error) {
	// Resolve only API-relative resource paths. Never let a caller supply an origin.
	path, err := url.ParseRequestURI(resource)
	if err != nil || path.IsAbs() || path.Host != "" || path.Path == "" || path.Path[0] != '/' {
		return Response{}, errors.New("invalid API resource path")
	}
	request, err := http.NewRequest(method, c.origin+apiPath+resource, bytes.NewReader(body))
	if err != nil {
		return Response{}, errors.New("could not construct the API request")
	}
	request.GetBody = nil
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}

	mutation := method != http.MethodGet && method != http.MethodHead
	reply, err := c.http.Do(request)
	if err != nil {
		return Response{}, responseError("could not reach the local service or read its response", mutation)
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		uncertainStatus := reply.StatusCode < 400 || reply.StatusCode >= 500
		return Response{}, responseError(fmt.Sprintf("service returned HTTP %d", reply.StatusCode),
			mutation && uncertainStatus)
	}
	var etag string
	tags := reply.Header.Values("ETag")
	if len(tags) == 1 && IsEntityTag(tags[0]) {
		etag = tags[0]
	}
	if mutation {
		expected := http.StatusOK
		if method == http.MethodPost {
			expected = http.StatusCreated
		}
		if reply.StatusCode != expected {
			return Response{}, responseError(fmt.Sprintf("service returned unexpected HTTP %d", reply.StatusCode), true)
		}
		boardTags := reply.Header.Values("X-Board-ETag")
		validBoardTag := len(boardTags) == 1 && IsEntityTag(boardTags[0])
		if method == http.MethodPut && strings.HasPrefix(path.Path, "/policies/") {
			if len(tags) != 0 || !validBoardTag {
				return Response{}, responseError("service did not return the expected policy replacement validators", true)
			}
		} else if etag == "" {
			return Response{}, responseError("service did not return one canonical strong ETag", true)
		}
		if method == http.MethodPost && path.Path == "/cards" && !validBoardTag {
			return Response{}, responseError("service did not return one canonical strong X-Board-ETag", true)
		}
	}
	data, err := io.ReadAll(io.LimitReader(reply.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || !IsJSONObject(data) {
		return Response{}, responseError("service returned an unreadable, oversized, or invalid JSON object", mutation)
	}
	return Response{JSON: data, ETag: etag}, nil
}

func IsEntityTag(tag string) bool {
	return entityTagPattern.MatchString(tag)
}

func IsJSONObject(data []byte) bool {
	object := bytes.TrimLeft(data, " \t\r\n")
	return len(object) > 0 && object[0] == '{' && utf8.Valid(data) && json.Valid(data)
}

func responseError(message string, unknown bool) error {
	if unknown {
		return fmt.Errorf("%s. Mutation outcome unknown. Inspect the resource before retrying", message)
	}
	return errors.New(message)
}
