package local

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/localswim/localswim/internal/board/grants"
	I "github.com/localswim/localswim/internal/board/installation"
	R "github.com/localswim/localswim/internal/board/recipient"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

// installationEngine is internal and permits owned test backends without a
// public command, path, environment switch or production injection surface.
type installationEngine interface {
	Inspect(context.Context, string, I.Check) (I.Observation, error)
	Control(context.Context, string, string, I.Check) (I.ControlView, error)
	Probe(context.Context, string, string, string, I.Check) (I.ProbeObservation, error)
	Stop(context.Context, string, string, string, I.Check) (I.StopObservation, error)
	Close() error
}

var errProbeUnavailable = errors.New("identity check unavailable")

func probeMedia(q *http.Request) bool {
	if len(q.Header.Values("Content-Type")) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(q.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

// decodeProbeCall validates the complete bounded adapter input before domain
// admission. Control reads carry no probe nonce and do not consume one.
func decodeProbeCall(q *http.Request, action string) (T.ProbeRequest, int) {
	var request T.ProbeRequest
	if q.URL.Fragment != "" || len(q.Header.Values("Content-Encoding")) != 0 || len(q.Header.Values("Content-Transfer-Encoding")) != 0 {
		return request, 400
	}
	body := q.Body
	if body == nil {
		body = http.NoBody
	}
	switch action {
	case "probe-control":
		if q.Method != http.MethodGet {
			return request, 405
		}
		values, err := url.ParseQuery(q.URL.RawQuery)
		bytes, bodyErr := io.ReadAll(io.LimitReader(body, 2))
		if err != nil || bodyErr != nil || len(bytes) != 0 || len(q.URL.RawQuery) > T.ProbeControlQueryLimit || len(values) != 3 ||
			len(values["incarnation"]) != 1 || len(values["expectedRuntimeGeneration"]) != 1 || len(values["expectedConfigurationGeneration"]) != 1 {
			return request, 400
		}
		request.Incarnation = A.UUID(values.Get("incarnation"))
		request.ExpectedRuntimeGeneration = T.UUIDv4(values.Get("expectedRuntimeGeneration"))
		request.ExpectedConfigurationGeneration = T.UUIDv4(values.Get("expectedConfigurationGeneration"))
		if A.Validate(request.Incarnation) != nil || !uuidV4(string(request.ExpectedRuntimeGeneration)) || !uuidV4(string(request.ExpectedConfigurationGeneration)) {
			return T.ProbeRequest{}, 400
		}
		return request, 0
	case "probe-version", "stop":
		if q.Method != http.MethodPost {
			return request, 405
		}
		if q.URL.RawQuery != "" || !probeMedia(q) {
			return request, 400
		}
		bytes, err := io.ReadAll(io.LimitReader(body, T.ProbeRequestLimit+1))
		if err != nil || len(bytes) > T.ProbeRequestLimit {
			return request, 400
		}
		request, err = T.Decode[T.ProbeRequest](bytes)
		if err != nil {
			return T.ProbeRequest{}, 400
		}
		return request, 0
	default:
		return request, 404
	}
}

// probeCurrentCheck never reads recipient input into a public result. Each call
// uses current credentials/read ceiling, physical identities and native sidecar
// validation. The engine owns every started check and its cancellation lifetime.
func (r *Runtime) probeCurrentCheck(mode string, token T.SecretToken, runtime, configuration string) I.Check {
	authority := r.recipientCheck(r.noticeAuthority(mode, token))
	return func(ctx context.Context) error {
		if ctx == nil {
			return errProbeUnavailable
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.recipient == nil || r.recipientState == nil || authority == nil {
			return errProbeUnavailable
		}
		if string(r.recipient.RuntimeGeneration()) != runtime {
			return I.ErrStale
		}
		if err := r.recipientState.verify(); err != nil {
			if errors.Is(err, R.ErrForeign) {
				return I.ErrIdentityChanged
			}
			return errProbeUnavailable
		}
		view, err := r.recipient.Status(ctx, authority)
		if err != nil {
			return err
		}
		if string(view.RuntimeGeneration) != runtime {
			return I.ErrStale
		}
		if view.State == "unavailable" && view.Reason == "foreign" {
			return I.ErrIdentityChanged
		}
		if view.State != "configured" || view.Record == nil {
			return errProbeUnavailable
		}
		if string(view.Record.ConfigurationGeneration) != configuration {
			return I.ErrStale
		}
		return ctx.Err()
	}
}

func refuseProbe(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, grants.ErrUnauthenticated):
		refuse(w, 401, "UNAUTHENTICATED")
	case errors.Is(err, grants.ErrDenied):
		refuse(w, 403, "FORBIDDEN")
	case errors.Is(err, I.ErrBusy), errors.Is(err, R.ErrBusy):
		refuse(w, 409, "CONTROL_BUSY")
	case errors.Is(err, I.ErrStale), errors.Is(err, R.ErrStale):
		refuse(w, 409, "CONTROL_STALE")
	case errors.Is(err, I.ErrIdentityChanged), errors.Is(err, R.ErrForeign):
		refuse(w, 409, "IDENTITY_CHANGED")
	default:
		refuse(w, 503, "UNAVAILABLE")
	}
}

func writeProbeResponse(w http.ResponseWriter, q *http.Request, value any) {
	if q.Context().Err() != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	bytes, err := T.Readable(value)
	if err != nil || len(bytes) > T.ProbeResponseLimit {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(bytes)
}

func (r *Runtime) serveProbe(w http.ResponseWriter, q *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(q.URL.Path, r.boardPath()+"recipient/tool/")
	request, status := decodeProbeCall(q, action)
	if status != 0 {
		code := "INVALID_INPUT"
		if status == 404 {
			code = "NOT_FOUND"
		}
		refuse(w, status, code)
		return
	}
	if string(request.Incarnation) != r.id.Incarnation {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	mode, token, ok := H.Credential(q.Context())
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if r.installation == nil || r.recipient == nil || r.recipientState == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	runtime, configuration := string(request.ExpectedRuntimeGeneration), string(request.ExpectedConfigurationGeneration)
	check := r.probeCurrentCheck(mode, token, runtime, configuration)
	identity := A.Identity{BoardID: A.UUID(r.id.BoardID), Incarnation: request.Incarnation}
	switch action {
	case "probe-control":
		view, err := r.installation.Control(q.Context(), runtime, configuration, check)
		if err != nil {
			refuseProbe(w, err)
			return
		}
		response := T.ProbeControlResponse{APIVersion: 1, Scope: "identity-control", Result: T.ProbeControlView{Identity: identity, RuntimeGeneration: request.ExpectedRuntimeGeneration, ConfigurationGeneration: request.ExpectedConfigurationGeneration, ProbeGeneration: T.UUIDv4(view.ProbeGeneration), Phase: T.ProbeControlPhase(view.Phase), Joined: view.Joined}}
		if err := T.ValidateProbeControlContext(response, identity, request.ExpectedRuntimeGeneration, request.ExpectedConfigurationGeneration); err != nil {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		writeProbeResponse(w, q, response)
	case "probe-version":
		view, err := r.installation.Probe(q.Context(), runtime, configuration, string(request.ProbeGeneration), check)
		if err != nil {
			refuseProbe(w, err)
			return
		}
		var version *T.ProbeToolVersion
		if view.ToolVersion != nil {
			value := T.ProbeToolVersion(*view.ToolVersion)
			version = &value
		}
		response := T.ProbeResult{Version: 1, Identity: identity, RuntimeGeneration: request.ExpectedRuntimeGeneration, ConfigurationGeneration: request.ExpectedConfigurationGeneration, ProbeGeneration: request.ProbeGeneration, Kind: "identity", State: T.ProbeState(view.State), Code: T.ProbeCode(view.Code), ToolVersion: version, Capability: nil, Joined: view.Joined}
		if err := T.ValidateProbeResultContext(response, identity, request.ExpectedRuntimeGeneration, request.ExpectedConfigurationGeneration, request.ProbeGeneration); err != nil {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		writeProbeResponse(w, q, response)
	case "stop":
		view, err := r.installation.Stop(q.Context(), runtime, configuration, string(request.ProbeGeneration), check)
		if err != nil {
			refuseProbe(w, err)
			return
		}
		response := T.ProbeStopResponse{APIVersion: 1, Scope: "identity-stop", Result: T.ProbeStopView{Identity: identity, RuntimeGeneration: request.ExpectedRuntimeGeneration, ConfigurationGeneration: request.ExpectedConfigurationGeneration, ProbeGeneration: T.UUIDv4(view.ProbeGeneration), Phase: T.ProbeStopPhase(view.Phase), Joined: view.Joined}}
		if err := T.ValidateProbeStopContext(response, identity, request.ExpectedRuntimeGeneration, request.ExpectedConfigurationGeneration, request.ProbeGeneration); err != nil {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
		writeProbeResponse(w, q, response)
	}
}
