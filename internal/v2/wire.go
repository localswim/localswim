package v2

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"time"
)

const timestampLayout = "2006-01-02 15:04:05+00:00"

func canonicalTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Second)
}

var timeMarshaler = jsonv2.MarshalFunc(func(value time.Time) ([]byte, error) {
	return json.Marshal(canonicalTime(value).Format(timestampLayout))
})

var timeUnmarshaler = jsonv2.UnmarshalFunc(func(data []byte, value *time.Time) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := time.Parse(timestampLayout, text)
	if err != nil || parsed.Format(timestampLayout) != text {
		return errors.New("timestamp must use YYYY-MM-DD HH:MM:SS+00:00")
	}
	*value = parsed.UTC()
	return nil
})

func marshalCanonical(value any) ([]byte, error) {
	return jsonv2.Marshal(value, jsonv2.WithMarshalers(timeMarshaler), jsonv2.Deterministic(true))
}

func unmarshalCanonical(data []byte, value any) error {
	return jsonv2.Unmarshal(data, value, jsonv2.WithUnmarshalers(timeUnmarshaler))
}
