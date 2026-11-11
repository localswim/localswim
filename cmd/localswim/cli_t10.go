package main

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

func parseServiceAction(name string, args []string) (cliCommand, error) {
	command := cliCommand{serviceAction: name}
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if name == "service activate" {
		flags.StringVar(&command.serviceActivationID, "id", "", "activation UUIDv7")
	}
	if err := flags.Parse(args); err != nil {
		return command, err
	}
	if name == "service stop" {
		if flags.NArg() != 0 {
			return command, errors.New("service stop accepts no arguments")
		}
		return command, nil
	}
	resourcePattern := boardIDPattern
	if name == "service activation" {
		resourcePattern = operationIDPattern
	}
	if flags.NArg() != 1 || !resourcePattern.MatchString(flags.Arg(0)) {
		return command, errors.New("expected one valid resource ID")
	}
	command.serviceResourceID = strings.ToLower(flags.Arg(0))
	if name == "service activate" {
		provided := flags.NFlag() != 0
		if provided && !operationIDPattern.MatchString(command.serviceActivationID) {
			return command, errors.New("--id requires a UUIDv7")
		}
		command.serviceActivationID = strings.ToLower(command.serviceActivationID)
		if !provided {
			command.serviceActivationID = newOperationID()
		}
	}
	return command, nil
}

type cliServiceStatus struct {
	Port              int             `json:"port"`
	ServiceInstanceID string          `json:"service_instance_id"`
	BuildID           string          `json:"build_id"`
	State             string          `json:"state"`
	APIVersion        string          `json:"api_version"`
	RestartErrorCode  json.RawMessage `json:"restart_error_code"`
}

var operationIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var serviceInstanceIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *cliServiceStatus) decode(document json.RawMessage) bool {
	return jsonv2.Unmarshal(document, s, jsonv2.RejectUnknownMembers(true)) == nil &&
		s.Port > 0 && s.Port <= 65535 &&
		serviceInstanceIDPattern.MatchString(s.ServiceInstanceID) && canonicalLifecycleID(s.BuildID) &&
		(s.State == "running" || s.State == "draining") && s.APIVersion == v2.Version &&
		validLifecycleErrorCode(s.RestartErrorCode)
}

type cliActivation struct {
	ID        string          `json:"id"`
	BuildID   string          `json:"build_id"`
	State     string          `json:"state"`
	ErrorCode json.RawMessage `json:"error_code"`
}

func (a *cliActivation) decode(document json.RawMessage) bool {
	if jsonv2.Unmarshal(document, a, jsonv2.RejectUnknownMembers(true)) != nil ||
		!(operationIDPattern.MatchString(a.ID) && strings.ToLower(a.ID) == a.ID) || !canonicalLifecycleID(a.BuildID) || !validLifecycleErrorCode(a.ErrorCode) {
		return false
	}
	switch a.State {
	case "accepted", "activated":
		return string(a.ErrorCode) == "null"
	case "failed":
		return string(a.ErrorCode) != "null"
	default:
		return false
	}
}

func canonicalLifecycleID(id string) bool {
	return boardIDPattern.MatchString(id) && strings.ToLower(id) == id
}

func validLifecycleErrorCode(raw json.RawMessage) bool {
	var code *string
	return jsonv2.Unmarshal(raw, &code) == nil && (code == nil || utf8.RuneCountInString(*code) <= 80)
}

func executeServiceAction(api *client.Client, command cliCommand, stdout io.Writer) error {
	if command.serviceAction == "service activation" {
		return inspectActivation(api, command.serviceResourceID, stdout)
	}
	reply, err := api.Do(http.MethodGet, "/service", nil, "")
	if err != nil {
		return err
	}
	var current cliServiceStatus
	if !current.decode(reply.JSON) || !client.IsEntityTag(reply.ETag) {
		return errors.New("service did not return a valid instance with a matching strong ETag")
	}

	currentTag := reply.ETag
	var confirmed bool
	inspection := "use localswim service status to inspect availability"
	if command.serviceAction == "service stop" {
		reply, err = api.Do(http.MethodPatch, "/service", json.RawMessage(`{"state":"stopped"}`), currentTag)
		var stopped cliServiceStatus
		confirmed = err == nil && stopped.decode(reply.JSON) &&
			stopped.ServiceInstanceID == current.ServiceInstanceID && stopped.State == "draining" &&
			stopped.Port == current.Port && stopped.BuildID == current.BuildID &&
			client.IsEntityTag(reply.ETag) && reply.ETag != currentTag
	} else {
		id := command.serviceActivationID
		inspection = "use localswim service activation " + id + " with refreshed credentials as needed"
		body, _ := json.Marshal(map[string]string{
			"id": id, "build_id": command.serviceResourceID, "expected_service_etag": currentTag,
		})
		reply, err = api.Do(http.MethodPost, "/activations", body, "")
		var activation cliActivation
		confirmed = err == nil && activation.decode(reply.JSON) &&
			activation.ID == id && activation.BuildID == command.serviceResourceID &&
			activation.State == "accepted" &&
			reply.Location == "/api/"+v2.Version+"/activations/"+id
	}
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. To check the result, %s", err, inspection)
		}
		return err
	}
	if !confirmed {
		return fmt.Errorf("service response did not confirm acceptance. %w. To check the result, %s",
			client.ErrMutationOutcomeUnknown, inspection)
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return fmt.Errorf("service accepted the request, but its response could not be written. To check the result, %s", inspection)
	}
	return nil
}

func inspectActivation(api *client.Client, id string, stdout io.Writer) error {
	reply, err := api.Do(http.MethodGet, "/activations/"+id, nil, "")
	if err != nil {
		return err
	}
	var activation cliActivation
	if !activation.decode(reply.JSON) || activation.ID != id {
		return errors.New("service did not return the requested activation")
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return errors.New("could not write the API response")
	}
	return nil
}
