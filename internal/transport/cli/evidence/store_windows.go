//go:build windows && amd64

package evidence

import (
	"bytes"
	"errors"
	T "github.com/localswim/localswim/internal/contract/transport"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// WindowsStore uses an explicitly composed per-user evidence directory, never a
// current-directory/default search. It refuses unsupported volumes and unsafe ACLs.
// No existing file is overwritten, repaired, discarded or replayed.
type WindowsStore struct{ fs *nativeFS }

func NewWindowsStore(root string) (*WindowsStore, error) {
	n, e := newNativeFS(root)
	if e != nil {
		return nil, ErrUnavailable
	}
	return &WindowsStore{n}, nil
}

func (s *WindowsStore) lock(create bool) (func(), error) {
	if s == nil || s.fs == nil {
		return nil, ErrUnavailable
	}
	pin, e := s.fs.rootPin(create)
	if e != nil {
		return nil, ErrUnavailable
	}
	if create {
		f, err := s.fs.open("evidence.lock", windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW)
		if err == nil {
			err = f.Close()
		}
		if err != nil && !errors.Is(err, windows.ERROR_FILE_EXISTS) && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			pin()
			return nil, ErrUnavailable
		}
	}
	f, e := s.fs.open("evidence.lock", windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING)
	if e != nil {
		pin()
		return nil, ErrUnavailable
	}
	return func() { f.Close(); pin() }, nil
}

func (s *WindowsStore) Save(r Record) (string, error) {
	if !r.Valid() {
		return "", ErrUnavailable
	}
	done, e := s.lock(true)
	if e != nil {
		return "", ErrUnavailable
	}
	defer done()
	entries, e := os.ReadDir(s.fs.root)
	if e != nil {
		return "", ErrUnavailable
	}
	total, count := 0, 0
	files := map[string][]byte{}
	for _, f := range entries {
		if f.Name() == "evidence.lock" {
			continue
		}
		if f.IsDir() || (!strings.HasSuffix(f.Name(), ".json") && !strings.HasSuffix(f.Name(), ".pending") && !strings.HasSuffix(f.Name(), ".resolved")) {
			return "", ErrUnavailable
		}
		b, err := s.fs.read(f.Name())
		if err != nil {
			return "", ErrUnavailable
		}
		files[f.Name()] = b
	}
	for name, b := range files {
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.pending") {
			total += len(b) + ResolutionReserve
			resolved := false
			if marker, ok := files[name+".resolved"]; ok {
				record, err := Decode(b)
				if err != nil {
					return "", ErrUnavailable
				}
				if _, err = readResolution(marker, record); err != nil {
					return "", ErrUnavailable
				}
				resolved = true
			}
			if !resolved {
				count++
			}
		} else {
			base := strings.TrimSuffix(strings.TrimSuffix(name, ".pending"), ".resolved")
			if _, ok := files[base]; !ok || len(b) > ResolutionReserve {
				return "", ErrUnavailable
			}
		}
	}
	if count >= MaxRecords || total+len(r.data)+ResolutionReserve > MaxBytes {
		return "", ErrUnavailable
	}
	name := r.Identity().OpID + ".json"
	pending := name + ".pending"
	if _, ok := files[name]; ok {
		return "", ErrUnavailable
	}
	if _, ok := files[pending]; ok {
		return "", ErrUnavailable
	}
	// The pending file is private at CREATE_NEW. Flush and independent readback
	// precede the exclusive no-replace move and final independent readback.
	if s.fs.write(pending, r.data) != nil {
		return "", ErrUnavailable
	}
	if windows.MoveFileEx(ptr(s.fs.path(pending)), ptr(s.fs.path(name)), windows.MOVEFILE_WRITE_THROUGH) != nil {
		return "", ErrUnavailable
	}
	b, e := s.fs.read(name)
	if e != nil || !bytes.Equal(b, r.data) {
		return "", ErrUnavailable
	}
	return s.fs.path(name), nil
}

// Resolve appends only a bounded validated class marker. The original semantic
// bytes never change. Admission reserved its entire footprint before dispatch.
func (s *WindowsStore) Resolve(r Record, t T.Terminal) error {
	b, e := resolutionBytes(r, t, time.Now())
	if e != nil {
		return ErrUnavailable
	}
	done, e := s.lock(false)
	if e != nil {
		return ErrUnavailable
	}
	defer done()
	name := r.Identity().OpID + ".json"
	original, e := s.fs.read(name)
	if e != nil || !bytes.Equal(original, r.data) {
		return ErrUnavailable
	}
	marker := name + ".resolved"
	if previous, e := s.fs.read(marker); e == nil {
		v, err := readResolution(previous, r)
		if err == nil && v.Outcome == string(t.Outcome) {
			return nil
		}
		return ErrUnavailable
	}
	if s.fs.write(marker+".pending", b) != nil {
		return ErrUnavailable
	}
	if windows.MoveFileEx(ptr(s.fs.path(marker+".pending")), ptr(s.fs.path(marker)), windows.MOVEFILE_WRITE_THROUGH) != nil {
		return ErrUnavailable
	}
	read, e := s.fs.read(marker)
	if e != nil || !bytes.Equal(read, b) {
		return ErrUnavailable
	}
	return nil
}

// ExpireResolved removes only validated records resolved at least seven days
// earlier. Unresolved/partial evidence is never automatically expired. There is
// no user-discard API: that separate action needs its explicit warning/consent.
func (s *WindowsStore) ExpireResolved(now time.Time) error {
	done, e := s.lock(false)
	if e != nil {
		return ErrUnavailable
	}
	defer done()
	entries, e := os.ReadDir(s.fs.root)
	if e != nil {
		return ErrUnavailable
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json.resolved") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".resolved")
		b, err := s.fs.read(name)
		if err != nil {
			return ErrUnavailable
		}
		r, err := Decode(b)
		if err != nil {
			return ErrUnavailable
		}
		b, err = s.fs.read(entry.Name())
		if err != nil {
			return ErrUnavailable
		}
		v, err := readResolution(b, r)
		if err != nil {
			return ErrUnavailable
		}
		at, _ := time.Parse("2006-01-02T15:04:05Z", v.ResolvedAt)
		if !now.Before(at.Add(7 * 24 * time.Hour)) {
			// Delete marker first: an interruption retains unresolved original
			// evidence, never a detached marker that can manufacture identity.
			if windows.DeleteFile(ptr(s.fs.path(entry.Name()))) != nil || windows.DeleteFile(ptr(s.fs.path(name))) != nil {
				return ErrUnavailable
			}
		}
	}
	return nil
}

func (s *WindowsStore) Load(path string) (Record, error) {
	if s == nil || s.fs == nil || filepath.Clean(path) != path || !strings.EqualFold(filepath.Dir(path), s.fs.root) {
		return Record{}, ErrUnavailable
	}
	done, e := s.lock(false)
	if e != nil {
		return Record{}, ErrUnavailable
	}
	defer done()
	b, e := s.fs.read(filepath.Base(path))
	if e != nil {
		return Record{}, ErrUnavailable
	}
	r, e := Decode(b)
	if e != nil || filepath.Base(path) != r.Identity().OpID+".json" {
		return Record{}, ErrUnavailable
	}
	return r, nil
}
