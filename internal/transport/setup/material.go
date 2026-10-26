package setup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
var hostRE = regexp.MustCompile(`^[0-9a-f]{32}\.localhost$`)
var materialNames = []string{"issuer.pem", "issuer-key.pem", "server.pem", "server-key.pem", "owner.json"}

func fmtPort(p int) string { return strconv.Itoa(p) }
func uuid(r io.Reader) (string, error) {
	var b [16]byte
	if _, e := io.ReadFull(r, b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
func serial(r io.Reader) (*big.Int, error) {
	for {
		var b [16]byte
		if _, e := io.ReadFull(r, b[:]); e != nil {
			return nil, e
		}
		x := new(big.Int).SetBytes(b[:])
		if x.Sign() > 0 {
			return x, nil
		}
	}
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return append(b, '\n') }

// jsonDecode rejects duplicate keys at every depth, unknown fields, noncanonical
// Unicode, CR/BOM and trailing values before decoding local versioned formats.
func jsonDecode(b []byte, v any) error {
	bad := fail("UNSAFE_LOCAL_MATERIAL", 2)
	if !utf8.Valid(b) || len(b) == 0 || b[len(b)-1] != '\n' || bytes.Contains(b, []byte{'\r'}) || bytes.HasPrefix(b, []byte{239, 187, 191}) {
		return bad
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func() error
	walk = func() error {
		t, e := d.Token()
		if e != nil {
			return bad
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || seen[s] {
						return bad
					}
					seen[s] = true
					if e = walk(); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim('}') {
					return bad
				}
			case '[':
				for d.More() {
					if e = walk(); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim(']') {
					return bad
				}
			default:
				return bad
			}
		}
		return nil
	}
	if e := walk(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return bad
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		return bad
	}
	typ := reflect.TypeOf(v).Elem()
	if typ.Kind() != reflect.Struct || len(fields) != typ.NumField() {
		return bad
	}
	for i := 0; i < typ.NumField(); i++ {
		if _, ok := fields[typ.Field(i).Tag.Get("json")]; !ok {
			return bad
		}
	}
	// encoding/json replaces unpaired escaped surrogates. Reject these rather
	// than accepting a different identity than the stored JSON actually says.
	for i := 0; i+5 < len(b); i++ {
		if b[i] == '\\' && b[i+1] == 'u' {
			n, e := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
			if e == nil && n >= 0xd800 && n <= 0xdfff {
				return bad
			}
		}
	}
	return nil
}
func certificatePEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}
func keyPEM(k any) ([]byte, error) {
	b, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		return nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}), nil
}
func parsePEM(b []byte, types ...string) ([][]byte, error) {
	var out [][]byte
	for _, typ := range types {
		block, rest := pem.Decode(b)
		if block == nil || block.Type != typ || len(block.Headers) != 0 {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		if !bytes.Equal(b[:len(b)-len(rest)], pem.EncodeToMemory(block)) {
			return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
		}
		out = append(out, block.Bytes)
		b = rest
	}
	if len(b) != 0 {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return out, nil
}
func newOwner(r io.Reader, m manifest, t time.Time) (ownerFile, error) {
	var b [32]byte
	if _, e := io.ReadFull(r, b[:]); e != nil {
		return ownerFile{}, e
	}
	return ownerFile{1, m.InstallationID, m.OwnerActorID, base64.RawURLEncoding.EncodeToString(b[:]), stamp(t), stamp(t.Add(30 * day))}, nil
}
func (m *material) setOwner(o ownerFile) {
	m.owner = o
	m.bytes["owner.json"] = jsonBytes(o)
	token, _ := base64.RawURLEncoding.DecodeString(o.Token)
	m.manifest.OwnerTokenSHA256 = hash(token)
	m.manifest.OwnerIssuedAt = o.IssuedAt
	m.manifest.OwnerExpiresAt = o.ExpiresAt
}
func issueLeaf(r io.Reader, m *material, t time.Time) error {
	if m.issuer.NotAfter.Sub(t) < day {
		return fail("ISSUER_REPLACEMENT_REQUIRED", 2)
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), r)
	if e != nil {
		return e
	}
	s, e := serial(r)
	if e != nil {
		return e
	}
	end := t.Add(30 * day)
	if end.After(m.issuer.NotAfter) {
		end = m.issuer.NotAfter
	}
	tpl := &x509.Certificate{SerialNumber: s, Subject: pkix.Name{CommonName: m.manifest.Hostname}, DNSNames: []string{m.manifest.Hostname}, NotBefore: t.Add(-5 * time.Minute), NotAfter: end, SignatureAlgorithm: x509.ECDSAWithSHA256, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(r, tpl, m.issuer, &k.PublicKey, m.issuerKey)
	if e != nil {
		return e
	}
	leaf, e := x509.ParseCertificate(der)
	if e != nil {
		return e
	}
	m.leaf = leaf
	m.leafKey = k
	m.bytes["server.pem"] = append(certificatePEM(leaf), certificatePEM(m.issuer)...)
	m.bytes["server-key.pem"], e = keyPEM(k)
	if e != nil {
		return e
	}
	m.manifest.LeafSHA256 = hash(der)
	m.manifest.LeafNotBefore = stamp(leaf.NotBefore)
	m.manifest.LeafNotAfter = stamp(leaf.NotAfter)
	return nil
}
func freshMaterial(r io.Reader, t time.Time, gen string, port int) (*material, error) {
	id, e := uuid(r)
	if e != nil {
		return nil, e
	}
	actor, e := uuid(r)
	if e != nil {
		return nil, e
	}
	var label [16]byte
	if _, e = io.ReadFull(r, label[:]); e != nil {
		return nil, e
	}
	m := &material{manifest: manifest{SchemaVersion: 1, Generation: gen, InstallationID: id, OwnerActorID: actor, Hostname: hex.EncodeToString(label[:]) + ".localhost", Port: port, CreatedAt: stamp(t), GenerationCreatedAt: stamp(t)}, bytes: map[string][]byte{}}
	k, e := ecdsa.GenerateKey(elliptic.P256(), r)
	if e != nil {
		return nil, e
	}
	s, e := serial(r)
	if e != nil {
		return nil, e
	}
	tpl := &x509.Certificate{SerialNumber: s, Subject: pkix.Name{CommonName: "localswim " + id + " local issuer"}, NotBefore: t.Add(-5 * time.Minute), NotAfter: t.Add(3650 * day), SignatureAlgorithm: x509.ECDSAWithSHA256, BasicConstraintsValid: true, IsCA: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, e := x509.CreateCertificate(r, tpl, tpl, &k.PublicKey, k)
	if e != nil {
		return nil, e
	}
	m.issuer, e = x509.ParseCertificate(der)
	if e != nil {
		return nil, e
	}
	m.issuerKey = k
	m.bytes["issuer.pem"] = certificatePEM(m.issuer)
	m.bytes["issuer-key.pem"], e = keyPEM(k)
	if e != nil {
		return nil, e
	}
	m.manifest.IssuerSHA256 = hash(der)
	m.manifest.IssuerNotBefore = stamp(m.issuer.NotBefore)
	m.manifest.IssuerNotAfter = stamp(m.issuer.NotAfter)
	if e = issueLeaf(r, m, t); e != nil {
		return nil, e
	}
	o, e := newOwner(r, m.manifest, t)
	if e != nil {
		return nil, e
	}
	m.setOwner(o)
	m.finish()
	return m, nil
}
func (m *material) finish() {
	m.manifest.Files = map[string]string{}
	for _, s := range materialNames {
		m.manifest.Files[s] = hash(m.bytes[s])
	}
	m.bytes["manifest.json"] = jsonBytes(m.manifest)
}
func readCert(b []byte) (*x509.Certificate, error) {
	x, e := parsePEM(b, "CERTIFICATE")
	if e != nil {
		return nil, e
	}
	c, e := x509.ParseCertificate(x[0])
	if e != nil {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return c, nil
}
func keyMatch(b []byte, c *x509.Certificate) (any, error) {
	x, e := parsePEM(b, "PRIVATE KEY")
	if e != nil {
		return nil, e
	}
	k, e := x509.ParsePKCS8PrivateKey(x[0])
	if e != nil {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	p, ok := k.(*ecdsa.PrivateKey)
	q, ok2 := c.PublicKey.(*ecdsa.PublicKey)
	if !ok || !ok2 || p.Curve != elliptic.P256() || q.Curve != elliptic.P256() || !p.PublicKey.Equal(q) {
		return nil, fail("UNSAFE_LOCAL_MATERIAL", 2)
	}
	return p, nil
}
func critical(c *x509.Certificate, oid string) bool {
	for _, e := range c.Extensions {
		if e.Id.String() == oid {
			return e.Critical
		}
	}
	return false
}
func baseCertificate(c *x509.Certificate) bool {
	key, ok := c.PublicKey.(*ecdsa.PublicKey)
	return ok && key.Curve == elliptic.P256() && c.SignatureAlgorithm == x509.ECDSAWithSHA256 && c.SerialNumber.Sign() > 0 && c.SerialNumber.BitLen() <= 128 && c.BasicConstraintsValid && critical(c, "2.5.29.19") && critical(c, "2.5.29.15") && len(c.UnhandledCriticalExtensions) == 0 && len(c.OCSPServer) == 0 && len(c.IssuingCertificateURL) == 0 && len(c.CRLDistributionPoints) == 0 && len(c.IPAddresses) == 0 && len(c.EmailAddresses) == 0 && len(c.URIs) == 0
}
func validateMaterial(b map[string][]byte, gen, manifestHash string, publicOnly bool) (*material, error) {
	bad := fail("UNSAFE_LOCAL_MATERIAL", 2)
	var x manifest
	if e := jsonDecode(b["manifest.json"], &x); e != nil {
		return nil, e
	}
	if x.SchemaVersion != 1 {
		return nil, fail("UNSUPPORTED_VERSION", 2)
	}
	if !uuidRE.MatchString(gen) || x.Generation != gen || hash(b["manifest.json"]) != manifestHash || !uuidRE.MatchString(x.InstallationID) || !uuidRE.MatchString(x.OwnerActorID) || !hostRE.MatchString(x.Hostname) || x.Port < 1024 || x.Port > 65535 || !hashRE.MatchString(x.IssuerSHA256) || !hashRE.MatchString(x.LeafSHA256) || !hashRE.MatchString(x.OwnerTokenSHA256) || len(x.Files) != 5 {
		return nil, bad
	}
	for _, name := range materialNames {
		if !hashRE.MatchString(x.Files[name]) || (!publicOnly && hash(b[name]) != x.Files[name]) {
			return nil, bad
		}
	}
	for _, s := range []string{x.CreatedAt, x.GenerationCreatedAt, x.IssuerNotBefore, x.IssuerNotAfter, x.LeafNotBefore, x.LeafNotAfter, x.OwnerIssuedAt, x.OwnerExpiresAt} {
		if _, e := parseTime(s); e != nil {
			return nil, e
		}
	}
	c, e := readCert(b["issuer.pem"])
	if e != nil {
		return nil, e
	}
	created, _ := parseTime(x.CreatedAt)
	generated, _ := parseTime(x.GenerationCreatedAt)
	if generated.Before(created) || !c.NotBefore.Equal(created.Add(-5*time.Minute)) || !c.NotAfter.Equal(created.Add(3650*day)) {
		return nil, bad
	}
	if hash(b["issuer.pem"]) != x.Files["issuer.pem"] || hash(c.Raw) != x.IssuerSHA256 || !baseCertificate(c) || !c.IsCA || c.MaxPathLen != 0 || !c.MaxPathLenZero || c.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign || len(c.ExtKeyUsage) != 0 || len(c.DNSNames) != 0 || c.Subject.CommonName != "localswim "+x.InstallationID+" local issuer" || stamp(c.NotBefore) != x.IssuerNotBefore || stamp(c.NotAfter) != x.IssuerNotAfter || !bytes.Equal(c.RawIssuer, c.RawSubject) || c.CheckSignatureFrom(c) != nil {
		return nil, bad
	}
	m := &material{manifest: x, bytes: b, issuer: c}
	if publicOnly {
		return m, nil
	}
	m.issuerKey, e = keyMatch(b["issuer-key.pem"], c)
	if e != nil {
		return nil, e
	}
	chain, e := parsePEM(b["server.pem"], "CERTIFICATE", "CERTIFICATE")
	if e != nil {
		return nil, e
	}
	leaf, e := x509.ParseCertificate(chain[0])
	if e != nil {
		return nil, bad
	}
	issuedLeaf := leaf.NotBefore.Add(5 * time.Minute)
	leafEnd := issuedLeaf.Add(30 * day)
	if leafEnd.After(c.NotAfter) {
		leafEnd = c.NotAfter
	}
	leafKey, isECDSA := leaf.PublicKey.(*ecdsa.PublicKey)
	if !isECDSA || issuedLeaf.Before(created) || issuedLeaf.After(generated) || !leaf.NotAfter.Equal(leafEnd) || leafKey.Equal(c.PublicKey) {
		return nil, bad
	}
	if !bytes.Equal(chain[1], c.Raw) || hash(leaf.Raw) != x.LeafSHA256 || !baseCertificate(leaf) || leaf.IsCA || leaf.KeyUsage != x509.KeyUsageDigitalSignature || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth || len(leaf.UnknownExtKeyUsage) != 0 || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != x.Hostname || leaf.Subject.CommonName != x.Hostname || stamp(leaf.NotBefore) != x.LeafNotBefore || stamp(leaf.NotAfter) != x.LeafNotAfter || leaf.NotAfter.After(c.NotAfter) || leaf.CheckSignatureFrom(c) != nil {
		return nil, bad
	}
	m.leaf = leaf
	m.leafKey, e = keyMatch(b["server-key.pem"], leaf)
	if e != nil {
		return nil, e
	}
	var o ownerFile
	if e = jsonDecode(b["owner.json"], &o); e != nil {
		return nil, e
	}
	if o.SchemaVersion != 1 {
		return nil, fail("UNSUPPORTED_VERSION", 2)
	}
	token, e := base64.RawURLEncoding.Strict().DecodeString(o.Token)
	issued, e1 := parseTime(o.IssuedAt)
	expires, e2 := parseTime(o.ExpiresAt)
	if e != nil || e1 != nil || e2 != nil || len(token) != 32 || base64.RawURLEncoding.EncodeToString(token) != o.Token || hash(token) != x.OwnerTokenSHA256 || o.InstallationID != x.InstallationID || o.ActorID != x.OwnerActorID || o.IssuedAt != x.OwnerIssuedAt || o.ExpiresAt != x.OwnerExpiresAt || !expires.Equal(issued.Add(30*day)) {
		return nil, bad
	}
	m.owner = o
	return m, nil
}

// crypto/rand is the production source; tests may replace the reader only in a
// private synthetic manager. There is no command or environment override.
var productionRandom io.Reader = rand.Reader
