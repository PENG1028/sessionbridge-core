package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// GenerateInternalTLS creates a self-signed CA and uses it to issue a server
// TLS certificate for the given hostnames/IPs.
//
// cert is used for tls.Config.Certificates (server-side).
// caPool is used for tls.Config.RootCAs (client-side verification).
// caPEM is printed to logs so users can install it in their browser.
func GenerateInternalTLS(hosts []string) (cert tls.Certificate, caPool *x509.CertPool, caPEM []byte, err error) {
	// 1. Generate CA key + self-signed CA certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		IsCA:                  true,
		BasicConstraintsValid: true,
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		Subject:               pkix.Name{CommonName: "SessionBridge Internal CA"},
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("create CA cert: %w", err)
	}

	// 2. Issue server certificate signed by CA
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("generate server key: %w", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(1 * 365 * 24 * time.Hour), // 1 year
		Subject:      pkix.Name{CommonName: "SessionBridge Node"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			serverTemplate.IPAddresses = append(serverTemplate.IPAddresses, ip)
		} else {
			serverTemplate.DNSNames = append(serverTemplate.DNSNames, h)
		}
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("create server cert: %w", err)
	}

	// 3. Assemble
	cert = tls.Certificate{
		Certificate: [][]byte{serverDER, caDER},
		PrivateKey:  serverKey,
	}
	caPool = x509.NewCertPool()
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caPool.AppendCertsFromPEM(caPEM)

	return cert, caPool, caPEM, nil
}

// SaveCACert writes the CA PEM to a file and returns the path.
func SaveCACert(dataDir string, caPEM []byte) (string, error) {
	caPath := filepath.Join(dataDir, "ca.crt")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
		return "", fmt.Errorf("write CA cert: %w", err)
	}
	return caPath, nil
}
