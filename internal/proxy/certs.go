package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kyuyeonpark/reflet/internal/storage"
)

type certAuthority struct {
	caCert *x509.Certificate
	caKey  *rsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newCertAuthority(cfg storage.Config) (*certAuthority, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.CAPath), 0o700); err != nil {
		return nil, fmt.Errorf("create cert dir: %w", err)
	}

	if _, err := os.Stat(cfg.CAPath); os.IsNotExist(err) {
		if err := writeCA(cfg.CAPath, cfg.CAKeyPath); err != nil {
			return nil, err
		}
	}

	caPEM, err := os.ReadFile(cfg.CAPath)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	keyPEM, err := os.ReadFile(cfg.CAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read ca key: %w", err)
	}

	caPair, err := tls.X509KeyPair(caPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load ca pair: %w", err)
	}
	caCert, err := x509.ParseCertificate(caPair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse ca cert: %w", err)
	}
	key, ok := caPair.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unsupported ca private key type %T", caPair.PrivateKey)
	}

	return &certAuthority{
		caCert: caCert,
		caKey:  key,
		cache:  make(map[string]*tls.Certificate),
	}, nil
}

func (ca *certAuthority) CertificateForHost(host string) (*tls.Certificate, error) {
	host = normalizeHost(host)

	ca.mu.Lock()
	defer ca.mu.Unlock()

	if cert, ok := ca.cache[host]; ok {
		return cert, nil
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"reflet PoC"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
	}

	if ip := net.ParseIP(host); ip != nil {
		tpl.DNSNames = nil
		tpl.IPAddresses = []net.IP{ip}
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.caCert, &leafKey.PublicKey, ca.caKey)
	if err != nil {
		return nil, fmt.Errorf("create leaf cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load leaf pair: %w", err)
	}
	ca.cache[host] = &pair
	return &pair, nil
}

func writeCA(certPath, keyPath string) error {
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate ca key: %w", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "reflet PoC Root CA",
			Organization: []string{"reflet PoC"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		SubjectKeyId:          subjectKeyID(&key.PublicKey),
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create ca cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("write ca cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write ca key: %w", err)
	}
	return nil
}

func subjectKeyID(pub *rsa.PublicKey) []byte {
	sum := sha1.Sum(x509.MarshalPKCS1PublicKey(pub))
	return sum[:]
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
