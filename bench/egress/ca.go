package egress

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

// CA signs the per-host certificates the proxy shows to clients.
type CA struct {
	cert    *x509.Certificate
	key     crypto.Signer
	certPEM []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	now    func() time.Time
}

// NewCA makes a certificate authority valid for ten years and returns the
// certificate and its key as one PEM file.
func NewCA(name string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Heimdall recorded egress " + name, Organization: []string{"Heimdall"}},
		NotBefore:             now.Add(-48 * time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})...), nil
}

// CertOnly returns the CERTIFICATE blocks of a PEM file: what is safe to
// put in a task image.
func CertOnly(pemBytes []byte) []byte {
	var out []byte
	for rest := pemBytes; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return out
		}
		if b.Type == "CERTIFICATE" {
			out = append(out, pem.EncodeToMemory(b)...)
		}
	}
}

// LoadCA reads a certificate and key PEM file made by NewCA.
func LoadCA(pemBytes []byte) (*CA, error) {
	c := &CA{leaves: map[string]*tls.Certificate{}, now: time.Now}
	for rest := pemBytes; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch b.Type {
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(b.Bytes)
			if err != nil {
				return nil, fmt.Errorf("egress ca: %w", err)
			}
			c.cert, c.certPEM = cert, pem.EncodeToMemory(b)
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
			if err != nil {
				return nil, fmt.Errorf("egress ca key: %w", err)
			}
			s, ok := k.(crypto.Signer)
			if !ok {
				return nil, errors.New("egress ca key cannot sign")
			}
			c.key = s
		}
	}
	if c.cert == nil || c.key == nil || !c.cert.IsCA {
		return nil, errors.New("egress ca: want a CA certificate and its PRIVATE KEY in one PEM file")
	}
	return c, nil
}

// CertPEM is the CA certificate.
func (c *CA) CertPEM() []byte { return c.certPEM }

// Fingerprint is the sha256 of the certificate, hex.
func (c *CA) Fingerprint() string { return fmt.Sprintf("%x", sha256.Sum256(c.cert.Raw)) }

// Leaf returns a certificate for host signed by the CA, made once per host.
func (c *CA) Leaf(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.leaves[host]; ok {
		return l, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, err
	}
	now := c.now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-48 * time.Hour),
		NotAfter:     now.AddDate(0, 0, 90),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if c.cert.NotAfter.Before(tmpl.NotAfter) {
		tmpl.NotAfter = c.cert.NotAfter
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	l := &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: key}
	c.leaves[host] = l
	return l, nil
}
