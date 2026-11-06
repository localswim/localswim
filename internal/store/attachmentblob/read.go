// Package attachmentblob reads verified attachment bytes in a caller-owned
// snapshot. The caller establishes authority and exact attachment membership.
package attachmentblob

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

const MaxFileBytes = 10 * 1024 * 1024

var ErrIntegrity = errors.New("attachment integrity unavailable")

type scanner interface {
	Scan(string, []any, ...any) error
}

// Read checks the metadata and SQLite length before transferring any BLOB.
// An absent BLOB is an integrity failure once membership has been established.
func Read(r scanner, inc, hash string, length uint64) ([]byte, error) {
	args := []any{inc, hash}
	var stored, actual uint64
	err := r.Scan("SELECT byte_length,length(bytes) FROM blob WHERE incarnation=? AND sha256=?", args, &stored, &actual)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIntegrity
	}
	if err != nil {
		return nil, err
	}
	if length > MaxFileBytes || stored != length || actual != length {
		return nil, ErrIntegrity
	}
	var b []byte
	err = r.Scan("SELECT bytes FROM blob WHERE incarnation=? AND sha256=?", args, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIntegrity
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(b)
	if uint64(len(b)) != length || hex.EncodeToString(digest[:]) != hash {
		return nil, ErrIntegrity
	}
	return b, nil
}
