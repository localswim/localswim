package v2

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const humanID = "3109cf0e-805f-4af2-b180-6cfed1c50ca1"
const botID = "5bb4d8c2-2702-4c70-93e1-e98b5cd683f4"

type Service struct {
	events              *eventHub
	store               *store
	workspace           *workspaceStore
	builds              *buildStatus
	lifecycle           serviceLifecycle
	principals          []user
	human, bot, browser [32]byte
	browserToken        string
	closeOnce           sync.Once
	closeErr            error
}

func Open(root, humanToken, botToken string) (*Service, error) {
	if !validToken(humanToken) || !validToken(botToken) || humanToken == botToken {
		return nil, errors.New("configure distinct Human and Bot runtime capabilities of at least 32 base64url bytes")
	}
	now := canonicalTime(time.Now())
	principals := []user{
		{ID: humanID, Revision: 1, CreatedAt: now, Label: "Human", Kind: "human", Color: ""},
		{ID: botID, Revision: 1, CreatedAt: now, Label: "Bot", Kind: "bot", Color: ""},
	}
	boards, err := openStore(root, principals...)
	if err != nil {
		return nil, err
	}
	browserToken := mintBrowserToken()
	events := newEventHub()
	workspace := newWorkspace(boards.root)
	boards.events, workspace.events = events, events
	boards.startBoardPublishers()
	builds := newBuildStatus(boards.root)
	boards.activityWriterID = builds.running.ServiceInstanceID
	boards.activitySince = canonicalTime(time.Now()).Add(time.Second)
	boards.ownerNotices = newOwnerNoticeWorker(ownerNoticeQuietPeriod, boards.ownerNoticeSettings, deliverOwnerNotice)
	boards.ownerNotices.writerID = boards.activityWriterID
	return &Service{events: events, store: boards, workspace: workspace, builds: builds,
		lifecycle:  serviceLifecycle{exit: make(chan ServiceExit, 1)},
		principals: principals, browserToken: browserToken, browser: sha256.Sum256([]byte(browserToken)), human: sha256.Sum256([]byte(humanToken)), bot: sha256.Sum256([]byte(botToken))}, nil
}

func validToken(token string) bool {
	if len(token) < 43 || len(token) > 128 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(decoded) >= 32 && base64.RawURLEncoding.EncodeToString(decoded) == token
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.StopEvents()
		s.stopBackground()
		s.closeErr = s.store.close()
	})
	return s.closeErr
}

func (s *Service) stopBackground() {
	workspaceDone := s.workspace.publisher.stop()
	s.store.closeBoardPublishers()
	if workspaceDone != nil {
		<-workspaceDone
	}
	if s.store.ownerNotices != nil {
		s.store.ownerNotices.close()
	}
}

type request struct {
	w           http.ResponseWriter
	r           *http.Request
	service     *Service
	id          string
	actor       string
	port        int
	boardDone   func()
	serviceDone func()
}

func (s *Service) Handler(address *net.TCPAddr) http.Handler {
	authority, port := address.String(), address.Port
	origin := "http://" + authority
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x := &request{w: w, r: r, service: s, id: newID(), port: port}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; sandbox")
		h.Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		h.Set("X-Request-ID", x.id)
		if r.Host != authority || r.URL.IsAbs() {
			x.problem(403, "forbidden", "Request authority is not accepted.", "", nil)
			return
		}
		origins := r.Header.Values("Origin")
		if len(origins) > 1 {
			x.problem(400, "invalid_request", "Repeated request header.", "", nil)
			return
		}
		if len(origins) == 1 {
			if origins[0] != origin {
				x.problem(403, "forbidden", "Request origin is not accepted.", "", nil)
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
			if r.Method != "OPTIONS" {
				h.Set("Access-Control-Expose-Headers", "ETag, Location, X-Request-ID, X-Board-ETag, Retry-After")
			}
		}
		for _, key := range []string{"Authorization", "If-Match", "If-None-Match", "X-Request-ID", "Content-Type", "X-Expected-Board-ETag", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
			if len(r.Header.Values(key)) > 1 {
				x.problem(400, "invalid_request", "Repeated request header.", "", nil)
				return
			}
		}
		if len(r.Header.Values("X-Request-ID")) != 0 {
			id, ok := normalizeID(r.Header.Get("X-Request-ID"))
			if !ok {
				x.problem(400, "invalid_request", "Invalid request identifier.", "", nil)
				return
			}
			x.id = id
			h.Set("X-Request-ID", id)
		}
		defer func() {
			if x.boardDone != nil {
				x.boardDone()
			}
			if x.serviceDone != nil {
				x.serviceDone()
			}
		}()
		if !x.serveBrowser() {
			x.dispatch()
		}
	})
}

func (x *request) authenticate() bool {
	scheme, token, ok := strings.Cut(x.r.Header.Get("Authorization"), " ")
	token = strings.TrimLeft(token, " ")
	if ok && strings.EqualFold(scheme, "Bearer") && validToken(token) {
		hash := sha256.Sum256([]byte(token))
		if (subtle.ConstantTimeCompare(hash[:], x.service.human[:]) |
			subtle.ConstantTimeCompare(hash[:], x.service.browser[:])) == 1 {
			x.actor = humanID
			return true
		}
		if subtle.ConstantTimeCompare(hash[:], x.service.bot[:]) == 1 {
			x.actor = botID
			return true
		}
	}
	x.w.Header().Set("WWW-Authenticate", `Bearer realm="localswim"`)
	x.problem(401, "unauthorized", "A valid local capability is required.", "", nil)
	return false
}

func (x *request) options(methods []string) {
	x.w.Header().Set("Allow", strings.Join(methods, ", "))
	preflightMethod := x.r.Header.Get("Access-Control-Request-Method")
	if len(x.r.Header.Values("Access-Control-Request-Method")) != 0 || len(x.r.Header.Values("Access-Control-Request-Headers")) != 0 {
		requested := strings.ToUpper(preflightMethod)
		if x.r.Header.Get("Origin") == "" || requested == "OPTIONS" || !slices.Contains(methods, requested) {
			x.problem(400, "invalid_request", "Invalid preflight method.", "", nil)
			return
		}
		headers := x.r.Header.Get("Access-Control-Request-Headers")
		if headers != "" {
			for name := range strings.SplitSeq(headers, ",") {
				switch strings.ToLower(strings.TrimSpace(name)) {
				case "authorization", "content-type", "if-match", "if-none-match", "x-request-id", "x-expected-board-etag":
				default:
					x.problem(400, "invalid_request", "Invalid preflight header.", "", nil)
					return
				}
			}
		}
		x.w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, If-None-Match, X-Expected-Board-ETag, X-Request-ID")
		x.w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
		x.w.Header().Set("Access-Control-Max-Age", "600")
	}
	x.w.WriteHeader(204)
}

func (x *request) body() (map[string]json.RawMessage, error) {
	return x.boundedBody(maxJSONBytes)
}

func (x *request) boundedBody(limit int64) (map[string]json.RawMessage, error) {
	mt, params, err := mime.ParseMediaType(x.r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, errMediaType
	}
	raw, err := io.ReadAll(http.MaxBytesReader(x.w, x.r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errTooBig
		}
		return nil, invalid("body", "Unreadable request body.")
	}
	return decodeObject(raw)
}

var errMediaType = errors.New("unsupported media type")
var errTooBig = errors.New("payload too large")
var errRequired = errors.New("precondition required")

func (x *request) ifMatch() (string, error) {
	tag := x.r.Header.Get("If-Match")
	if len(x.r.Header.Values("If-Match")) == 0 {
		return "", errRequired
	}
	if _, ok := parseTagList(tag); !ok {
		return "", invalid("If-Match", "Expected an entity tag list.")
	}
	return tag, nil
}

func (x *request) fail(err error) {
	var input invalidField
	switch {
	case errors.As(err, &input):
		x.problem(400, "invalid_request", input.Message, "", []fieldIssue{{Field: input.Field, Code: "invalid_value"}})
	case errors.Is(err, errMediaType):
		x.problem(415, "unsupported_media_type", "Use application/json with UTF-8.", "", nil)
	case errors.Is(err, errTooBig):
		x.problem(413, "payload_too_large", "The request exceeds the entity limit.", "", nil)
	case errors.Is(err, errCommentCardETagRequired):
		x.problem(428, "precondition_required", "expected_card_etag is required.", "", nil)
	case errors.Is(err, errRequired):
		x.problem(428, "precondition_required", "If-Match is required.", "", nil)
	case errors.Is(err, errStale):
		x.problem(412, "precondition_failed", "The resource version has changed.", "", nil)
	case errors.Is(err, errForbidden):
		x.problem(403, "forbidden", "This capability does not authorize the operation.", "", nil)
	case errors.Is(err, errMissing), errors.Is(err, errBoardNotFound):
		x.problem(404, "not_found", "Resource unavailable at this address.", "", nil)
	case errors.Is(err, errBoardExists), errors.Is(err, errIdentityConflict):
		x.problem(409, "conflict", "The requested identity conflicts with existing state.", "", nil)
	case errors.Is(err, errPolicyGrantConflict):
		x.problem(409, "conflict", "The replacement must retain existing grants, resource permissions, and the default priority.", "", nil)
	case errors.Is(err, errWriteUncertain):
		x.problem(503, "outcome_unknown", "Inspect the saved state before attempting another write.", "unknown", nil)
	case errors.Is(err, errServiceDraining):
		x.problem(503, "unavailable", "The service is draining.", "", nil)
	case errors.Is(err, errFinalPublish):
		x.problem(503, "unavailable", "Final publication failed. The service remains running.", "", nil)
	case errors.Is(err, errActivationBuild):
		x.problem(409, "conflict", "The staged build has not been validated for activation.", "", nil)
	case errors.Is(err, errActivationID):
		x.problem(409, "conflict", "The activation identifier already exists.", "", nil)
	case errors.Is(err, errActivationStore):
		x.problem(503, "unavailable", "The activation outcome is unavailable.", "", nil)
	case errors.Is(err, errActivationStart):
		x.problem(503, "unavailable", "The staged build could not prepare for activation. The service remains running.", "", nil)
	case errors.Is(err, errBoardUnavailable):
		x.problem(503, "unavailable", "This board is closed or draining.", "", nil)
	case errors.Is(err, errBoardFlush):
		x.problem(503, "unavailable", "Board publication could not be flushed. The board is still open.", "", nil)
	case errors.Is(err, errDurableStateChanged):
		x.problem(503, "unavailable", "Saved board state differs from the accepted snapshot.", "", nil)
	case errors.Is(err, errWorkspaceUnavailable), errors.Is(err, errInvalidSnapshot):
		x.problem(503, "unavailable", "Durable state is unavailable.", "", nil)
	default:
		x.problem(500, "internal_error", "The operation could not be served.", "", nil)
	}
}

type fieldIssue struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}
type problemBody struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail"`
	RequestID string       `json:"request_id"`
	Outcome   string       `json:"outcome"`
	Errors    []fieldIssue `json:"errors"`
}

func (x *request) problem(status int, code, detail, outcome string, issues []fieldIssue) {
	if outcome == "" {
		outcome = "not_applicable"
		switch x.r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			outcome = "not_applied"
		}
	}
	if issues == nil {
		issues = []fieldIssue{}
	}
	x.w.Header().Set("Cache-Control", "no-store")
	x.w.Header().Del("ETag")
	x.w.Header().Set("Content-Type", "application/problem+json")
	x.w.WriteHeader(status)
	if x.r.Method != "HEAD" {
		_ = json.NewEncoder(x.w).Encode(problemBody{"about:blank", http.StatusText(status), status, code, detail, x.id, outcome, issues})
	}
}

func (x *request) respond(status int, value any, tag string) {
	x.respondWithConditionalTag(status, value, tag, true)
}

func (x *request) respondPublishing(status int, value any, tag string, info publishing) {
	// This revision tag remains the command validator. A worker can change the
	// live status without changing that revision, so reread its representation.
	stable := !info.Enabled && !info.Pending && info.LastSuccessAt == nil && info.ErrorCode == nil
	x.respondWithConditionalTag(status, value, tag, stable)
}

func (x *request) respondWithConditionalTag(status int, value any, tag string, conditional bool) {
	data, err := marshalCanonical(value)
	if err != nil || len(data) > maxJSONBytes {
		x.fail(errors.New("response encoding failed"))
		return
	}
	h := x.w.Header()
	if x.r.Method == "GET" || x.r.Method == "HEAD" {
		var conditions []conditionTag
		if len(x.r.Header.Values("If-None-Match")) != 0 {
			var ok bool
			conditions, ok = parseTagList(x.r.Header.Get("If-None-Match"))
			if !ok {
				x.fail(invalid("If-None-Match", "Expected an entity tag list."))
				return
			}
		}
		h.Set("Cache-Control", "private, no-cache")
		if tag == "" {
			tag = fmt.Sprintf("\"%x\"", sha256.Sum256(data))
		}
		h.Set("ETag", tag)
		for _, candidate := range conditions {
			if candidate.opaque == "*" || conditional && candidate.opaque == tag {
				x.w.WriteHeader(304)
				return
			}
		}
	} else if tag != "" {
		h.Set("ETag", tag)
	}
	h.Set("Content-Type", "application/json")
	x.w.WriteHeader(status)
	if x.r.Method != "HEAD" {
		_, _ = x.w.Write(data)
	}
}
