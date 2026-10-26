//go:build !windows || !amd64

package evidence

import T "github.com/localswim/localswim/internal/contract/transport"
import "time"

type WindowsStore struct{}

func NewWindowsStore(string) (*WindowsStore, error)    { return nil, ErrUnavailable }
func (*WindowsStore) Save(Record) (string, error)      { return "", ErrUnavailable }
func (*WindowsStore) Load(string) (Record, error)      { return Record{}, ErrUnavailable }
func (*WindowsStore) Resolve(Record, T.Terminal) error { return ErrUnavailable }
func (*WindowsStore) ExpireResolved(time.Time) error   { return ErrUnavailable }
