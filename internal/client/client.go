// Package client is the CLI's transport for the local V2 HTTP service.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
var ErrMutationOutcomeUnknown = errors.New("Mutation outcome unknown")

type Client struct {
	origin string
	token  string
	http   *http.Client
}

type Response struct {
	JSON      json.RawMessage
	ETag      string
	BoardETag string
	Location  string
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
	return c.DoWithContentType(method, resource, body, ifMatch, "")
}

func (c *Client) DoWithContentType(method, resource string, body []byte, ifMatch, contentType string) (Response, error) {
	return c.doWithContentType(method, resource, body, ifMatch, contentType, false)
}

func (c *Client) doWithContentType(method, resource string, body []byte, ifMatch, contentType string, boardAvailability bool) (Response, error) {
	request, err := c.newRequest(method, resource, body, ifMatch, contentType, "application/json")
	if err != nil {
		return Response{}, err
	}
	mutation := method != http.MethodGet && method != http.MethodHead
	path := request.URL.Path[len(apiPath):]
	lifecycleWrite := method == http.MethodPatch && path == "/service" || method == http.MethodPost && path == "/activations"
	activationRead := method == http.MethodGet && strings.HasPrefix(path, "/activations/")
	lifecycleRead := method == http.MethodGet && path == "/service" || activationRead
	reply, err := c.http.Do(request)
	if err != nil {
		return Response{}, responseError("could not reach the local service or read its response", mutation)
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		uncertainStatus := reply.StatusCode < 400 || reply.StatusCode >= 500
		if (boardAvailability || lifecycleWrite) && isUnavailableRefusal(reply) {
			return Response{}, errors.New("service returned HTTP 503 (unavailable, not applied)")
		}
		return Response{}, responseError(fmt.Sprintf("service returned HTTP %d", reply.StatusCode),
			mutation && uncertainStatus)
	}
	if lifecycleRead && reply.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("service returned unexpected HTTP %d", reply.StatusCode)
	}
	var etag string
	tags := reply.Header.Values("ETag")
	if len(tags) == 1 && IsEntityTag(tags[0]) {
		etag = tags[0]
	}
	if activationRead && etag == "" {
		return Response{}, errors.New("service did not return one canonical strong activation ETag")
	}
	if mutation {
		expected := http.StatusOK
		if lifecycleWrite {
			expected = http.StatusAccepted
		} else if method == http.MethodPost {
			expected = http.StatusCreated
			if pathWithoutQuery(resource) == "/publications" {
				expected = http.StatusAccepted
			}
		}
		if reply.StatusCode != expected {
			return Response{}, responseError(fmt.Sprintf("service returned unexpected HTTP %d", reply.StatusCode), true)
		}
		boardTags := reply.Header.Values("X-Board-ETag")
		validBoardTag := len(boardTags) == 1 && IsEntityTag(boardTags[0])
		if method == http.MethodPut && strings.HasPrefix(request.URL.Path, apiPath+"/policies/") {
			if len(tags) != 0 || !validBoardTag {
				return Response{}, responseError("service did not return the expected policy replacement validators", true)
			}
		} else if method == http.MethodPatch && request.URL.Path == apiPath+"/cards" {
			if !validBoardTag {
				return Response{}, responseError("service did not return one canonical strong X-Board-ETag", true)
			}
		} else if method == http.MethodPut && (strings.HasPrefix(pathWithoutQuery(resource), "/ticket-orders/") || strings.HasPrefix(pathWithoutQuery(resource), "/snapshots/")) {
			if !validBoardTag {
				return Response{}, responseError("service did not return a strong board validator", true)
			}
		} else if etag == "" && !lifecycleWrite && !(method == http.MethodPost && (path == "/backups" || path == "/publications" || path == "/restorations")) {
			return Response{}, responseError("service did not return one canonical strong ETag", true)
		}
		if requiresBoardTag(method, request.URL.Path[len(apiPath):]) && !validBoardTag {
			return Response{}, responseError("service did not return one canonical strong X-Board-ETag", true)
		}
		if boardAvailability && (!validBoardTag || boardTags[0] != etag) {
			return Response{}, responseError("service did not return matching ETag and X-Board-ETag validators", true)
		}
	}
	if lifecycleRead || lifecycleWrite {
		types := reply.Header.Values("Content-Type")
		mediaType, _, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
		if len(types) != 1 || err != nil || mediaType != "application/json" {
			return Response{}, responseError("service did not return an application/json response", mutation)
		}
	}
	data, err := io.ReadAll(io.LimitReader(reply.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || !IsJSONObject(data) {
		return Response{}, responseError("service returned an unreadable, oversized, or invalid JSON object", mutation)
	}
	if strings.HasPrefix(pathWithoutQuery(resource), "/attachments") && !validAttachmentResponse(resource, data) {
		return Response{}, responseError("service returned invalid attachment metadata", mutation)
	}
	if (method == http.MethodPatch && pathWithoutQuery(resource) == "/cards" ||
		strings.HasPrefix(pathWithoutQuery(resource), "/ticket-orders/") ||
		strings.HasPrefix(pathWithoutQuery(resource), "/backups")) && !validT06Response(resource, data) {
		return Response{}, responseError("service returned invalid batch, ticket-order, or backup metadata", mutation)
	}
	var boardETag string
	if tags := reply.Header.Values("X-Board-ETag"); len(tags) == 1 && IsEntityTag(tags[0]) {
		boardETag = tags[0]
	}
	var location string
	if lifecycleWrite && method == http.MethodPost {
		if locations := reply.Header.Values("Location"); len(locations) == 1 {
			location = locations[0]
		}
	}
	return Response{JSON: data, ETag: etag, BoardETag: boardETag, Location: location}, nil
}

func (c *Client) newRequest(method, resource string, body []byte, ifMatch, contentType, accept string) (*http.Request, error) {
	// Resolve only API-relative resource paths. Never let a caller supply an origin.
	path, err := url.ParseRequestURI(resource)
	if err != nil || path.IsAbs() || path.Host != "" || path.Path == "" || path.Path[0] != '/' {
		return nil, errors.New("invalid API resource path")
	}
	request, err := http.NewRequest(method, c.origin+apiPath+resource, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not construct the API request")
	}
	request.GetBody = nil
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", accept)
	if body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		request.Header.Set("Content-Type", contentType)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	return request, nil
}

func requiresBoardTag(method, path string) bool {
	if method == http.MethodPost {
		switch path {
		case "/cards", "/comments", "/lanes", "/attachments", "/relationships", "/restorations":
			return true
		}
	}
	return method == http.MethodPatch && (strings.HasPrefix(path, "/cards/") || strings.HasPrefix(path, "/lanes/") ||
		strings.HasPrefix(path, "/relationships/"))
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
		return fmt.Errorf("%s. %w. Inspect the resource before retrying", message, ErrMutationOutcomeUnknown)
	}
	return errors.New(message)
}
