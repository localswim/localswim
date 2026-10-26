//go:build windows && amd64

package recovery

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestAttachmentCandidateQuotaFacade(t *testing.T) {
	f := newFixture(t)
	if RegistryFor(f.store) != f.svc.registry {
		t.Fatal("separate per-store pin universe")
	}
	g := testGuard(t, f.svc.registry)
	l, e := f.store.Reader(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(tc(t), func(c *sql.Conn) error {
		r := readAccess{tc(t), c}
		if (g.CheckQuota(r) == nil) != (g.CheckQuotaWith(r) == nil) {
			t.Fatal("empty candidate changed existing check")
		}
		facts := []A.ByteFact{}
		for n := 0; n < 7; n++ {
			size := A.Number(10485760)
			if n == 6 {
				size = 4194304
			}
			facts = append(facts, A.ByteFact{SHA256: A.Digest(fmt.Sprintf("%064x", n+1)), ByteLength: size})
		}
		if e := g.CheckQuotaWith(r, facts...); e != nil {
			t.Fatal("inclusive 64MiB", e)
		}
		facts = append(facts, facts[0], A.ByteFact{SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ByteLength: 0})
		if e := g.CheckQuotaWith(r, facts...); e != nil {
			t.Fatal("duplicate/zero union", e)
		}
		if e := g.CheckQuotaWith(r, append(facts, A.ByteFact{SHA256: A.Digest(fmt.Sprintf("%064x", 99)), ByteLength: 1})...); !errors.Is(e, ErrByteQuota) {
			t.Fatal("extra byte quota", e)
		}
		if e := g.CheckQuotaWith(r, append(facts, A.ByteFact{SHA256: facts[0].SHA256, ByteLength: 2})...); e == nil || errors.Is(e, ErrByteQuota) {
			t.Fatal("digest length damage disguised as quota")
		}
		if e := g.CheckQuotaWith(r, A.ByteFact{SHA256: "invalid", ByteLength: 0}); e == nil {
			t.Fatal("invalid digest")
		}
		if e := g.CheckQuotaWith(r, A.ByteFact{SHA256: facts[0].SHA256, ByteLength: 10485761}); e == nil {
			t.Fatal("file cap")
		}
		return nil
	})
	ce := l.Close()
	g.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	if g.CheckQuotaWith(nil) == nil {
		t.Fatal("closed guard admitted")
	}
}
