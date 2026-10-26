//go:build windows && amd64

package setup

import "crypto/tls"

// PlanTrust performs no trust mutation and prints nothing. The adapter must show
// every public plan field on stderr before obtaining Confirmation. It never
// accepts a certificate/name from the caller. Removal uses public-only validation
// so exact intact issuer trust can be retired after private material loss/expiry.
func (m *Manager) PlanTrust(operation, id, pin string) (out TrustPlan, err error) {
	defer func() {
		if err != nil {
			err = failure(err)
		}
	}()
	if operation != "enroll" && operation != "remove" {
		return TrustPlan{}, fail("INVOCATION_OR_IO", 1)
	}
	c, e := m.fs.rootPin(false)
	if e != nil {
		return TrustPlan{}, e
	}
	defer c()
	mat, e := m.coherent(operation == "remove")
	if e != nil {
		return TrustPlan{}, e
	}
	if e = identity(mat, id, pin); e != nil {
		return TrustPlan{}, e
	}
	if operation == "enroll" {
		now := m.time()
		if now.Before(mat.issuer.NotBefore) || mat.issuer.NotAfter.Sub(now) < day {
			return TrustPlan{}, fail("ISSUER_REPLACEMENT_REQUIRED", 2)
		}
	}
	return TrustPlan{InstallationID: id, Authority: mat.context().Authority, IssuerSHA256: pin, ExpiresAt: stamp(mat.issuer.NotAfter), StoreScope: "CurrentUser Root writable local physical store", Consequence: "Root trust is broad browser trust; other stores, profiles, caches and existing connections may retain trust.", ConfirmationLine: operation + " " + id + " " + pin, issuerDER: append([]byte(nil), mat.issuer.Raw...), operation: operation}, nil
}

// ApplyTrust executes only through the injected TrustStore. No native store
// implementation is linked into setup; qualification must supply the deliberately
// approved current-user physical-store adapter later. Terminal confirmation is a
// cooperative owner control, not proof of a human or meaningful review.
func (m *Manager) ApplyTrust(plan TrustPlan, c Confirmation) Response {
	command := "trust." + plan.operation
	r := emptyResult()
	if plan.operation != "enroll" && plan.operation != "remove" {
		return response(command, r, fail("INVOCATION_OR_IO", 1))
	}
	if e := confirm(c, plan.operation+" "+plan.InstallationID+" "+plan.IssuerSHA256); e != nil {
		return response(command, r, e)
	}
	closeRoot, e := m.fs.rootPin(false)
	if e != nil {
		return response(command, r, e)
	}
	defer closeRoot()
	unlock, e := m.fs.lock(false)
	if e != nil {
		return response(command, r, e)
	}
	defer unlock()
	current, e := m.PlanTrust(plan.operation, plan.InstallationID, plan.IssuerSHA256)
	if e != nil {
		return response(command, r, e)
	}
	if hash(current.issuerDER) != hash(plan.issuerDER) || current.ConfirmationLine != plan.ConfirmationLine || current.Authority != plan.Authority || current.ExpiresAt != plan.ExpiresAt || current.StoreScope != plan.StoreScope || current.Consequence != plan.Consequence {
		return response(command, r, fail("IDENTITY_MISMATCH", 2))
	}
	if m.store == nil {
		return response(command, r, fail("TRUST_STORE_UNAVAILABLE", 3))
	}
	present, e := m.store.Membership(current.issuerDER)
	if e != nil {
		return response(command, r, fail("TRUST_STORE_UNAVAILABLE", 3))
	}
	change := (plan.operation == "enroll") != present
	if change {
		if plan.operation == "enroll" {
			e = m.store.Enroll(current.issuerDER)
		} else {
			e = m.store.Remove(current.issuerDER)
		}
		if e != nil {
			return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
		}
	}
	present, e = m.store.Membership(current.issuerDER)
	if e != nil || present != (plan.operation == "enroll") {
		return response(command, r, fail("SETUP_OUTCOME_UNKNOWN", 4))
	}
	mat, validationErr := m.coherent(false)
	if validationErr == nil && mat != nil {
		r = m.result(mat, nil, m.time())
		r.Changed = change
		r.CurrentUserRoot = "absent"
		if present {
			r.CurrentUserRoot = "present"
		}
		return response(command, r, nil)
	}
	// Public-only retirement is confirmed without pretending private material was
	// validated. Its exact public context remains on the reviewed TrustPlan.
	out := response(command, r, nil)
	out.Envelope.Result = nil
	return out
}

// TLSConfig enforces leaf expiry on each new server handshake, including a
// running server whose certificate lifetime ends. No hot replacement is implied.
func (s *ServerLease) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		now := s.now()
		if now.Before(s.notBefore) || !now.Before(s.notAfter) {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		return &s.certificate, nil
	}}
}
