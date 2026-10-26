package transport

import (
	"fmt"
	"reflect"

	A "github.com/localswim/localswim/internal/contract/archive"
)

type codeSecretWire CodeSecret
type exchangeRequestWire ExchangeRequest
type exchangeSecretWire ExchangeSecret
type credentialSecretWire CredentialSecret

func plain(v any) any {
	switch x := v.(type) {
	case CodeSecret:
		return codeSecretWire(x)
	case ExchangeRequest:
		return exchangeRequestWire(x)
	case ExchangeSecret:
		return exchangeSecretWire(x)
	case CredentialSecret:
		return credentialSecretWire(x)
	}
	return v
}
func secretMarshal() ([]byte, error) {
	return nil, fmt.Errorf("ephemeral secret body requires MarshalEphemeral")
}
func (CodeSecret) MarshalJSON() ([]byte, error)       { return secretMarshal() }
func (ExchangeRequest) MarshalJSON() ([]byte, error)  { return secretMarshal() }
func (ExchangeSecret) MarshalJSON() ([]byte, error)   { return secretMarshal() }
func (CredentialSecret) MarshalJSON() ([]byte, error) { return secretMarshal() }
func (CodeSecret) String() string                     { return "CodeSecret{REDACTED}" }
func (ExchangeRequest) String() string                { return "ExchangeRequest{REDACTED}" }
func (ExchangeSecret) String() string                 { return "ExchangeSecret{REDACTED}" }
func (CredentialSecret) String() string               { return "CredentialSecret{REDACTED}" }
func (x CodeSecret) GoString() string                 { return x.String() }
func (x ExchangeRequest) GoString() string            { return x.String() }
func (x ExchangeSecret) GoString() string             { return x.String() }
func (x CredentialSecret) GoString() string           { return x.String() }
func (SecretCode) String() string                     { return "REDACTED" }
func (SecretToken) String() string                    { return "REDACTED" }
func (x SecretCode) GoString() string                 { return x.String() }
func (x SecretToken) GoString() string                { return x.String() }

// MarshalEphemeral is an explicit serialization opt-in for the four secret
// transport bodies. Callers must set Cache-Control: no-store and keep returned
// bytes out of stdout, logs, evidence, canonical intent, retained data and export.
// It does not generate entropy, deliver secrets, publish files or grant access.
func MarshalEphemeral(v any) ([]byte, error) {
	switch v.(type) {
	case CodeSecret, ExchangeRequest, ExchangeSecret, CredentialSecret:
	default:
		return nil, fmt.Errorf("unsupported ephemeral body")
	}
	if e := Validate(v); e != nil {
		return nil, e
	}
	return A.Readable(plain(v))
}

// Readable emits D02 R bytes only after validating a secret-free public boundary.
// No arbitrary unsafe received body is rendered for diagnostics.
func Readable(v any) ([]byte, error) {
	if hasSecret(reflect.TypeOf(v)) {
		return nil, fmt.Errorf("secret transport is not public output")
	}
	if e := Validate(v); e != nil {
		return nil, e
	}
	return A.Readable(v)
}
func hasSecret(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t == reflect.TypeFor[SecretCode]() || t == reflect.TypeFor[SecretToken]() {
		return true
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return hasSecret(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if hasSecret(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}
