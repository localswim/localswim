package v2

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"time"
)

const timestampLayout = "2006-01-02T15:04:05.000000Z"

var timeMarshaler = jsonv2.MarshalFunc(func(value time.Time) ([]byte, error) {
	return json.Marshal(value.UTC().Format(timestampLayout))
})

func marshalCanonical(value any) ([]byte, error) {
	return jsonv2.Marshal(value, jsonv2.WithMarshalers(timeMarshaler), jsonv2.Deterministic(true))
}
