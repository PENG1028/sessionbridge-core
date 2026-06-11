package crypto

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
)

// TestGenerateInternalTLS verifies TLS certificate generation works.
func TestGenerateInternalTLS(t *testing.T) {
	cert, caPool, caPEM, err := GenerateInternalTLS([]string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("GenerateInternalTLS: %v", err)
	}

	if cert.Certificate == nil {
		t.Fatal("cert.Certificate is nil")
	}
	if len(cert.Certificate) != 2 {
		t.Fatalf("expected 2 certs (server + CA), got %d", len(cert.Certificate))
	}

	if caPool == nil {
		t.Fatal("caPool is nil")
	}
	if len(caPEM) == 0 {
		t.Fatal("caPEM is empty")
	}

	t.Logf("CA PEM: %d bytes", len(caPEM))
	t.Logf("Server cert: %d bytes", len(cert.Certificate[0]))
}

// TestGenerateInternalTLS_ValidHostnames verifies IP and DNS name handling.
func TestGenerateInternalTLS_ValidHostnames(t *testing.T) {
	hosts := []string{"192.168.1.1", "hub.example.com", "10.0.0.1"}
	cert, _, _, err := GenerateInternalTLS(hosts)
	if err != nil {
		t.Fatalf("GenerateInternalTLS: %v", err)
	}

	// Parse the server certificate to verify hosts
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}

	foundIP := false
	foundDNS := false
	for _, ip := range x509Cert.IPAddresses {
		if ip.Equal(net.ParseIP("192.168.1.1")) {
			foundIP = true
		}
	}
	for _, dns := range x509Cert.DNSNames {
		if dns == "hub.example.com" {
			foundDNS = true
		}
	}
	if !foundIP {
		t.Error("missing IP SAN 192.168.1.1")
	}
	if !foundDNS {
		t.Error("missing DNS SAN hub.example.com")
	}
}

// TestGenerateInternalTLS_ServeTLS verifies the cert can be used for TLS serving.
func TestGenerateInternalTLS_ServeTLS(t *testing.T) {
	cert, _, _, err := GenerateInternalTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("GenerateInternalTLS: %v", err)
	}

	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	if config.Certificates[0].Leaf == nil {
		// Force leaf parse
		config.Certificates[0].Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	if config.Certificates[0].Leaf == nil {
		t.Fatal("failed to parse leaf certificate")
	}

	// Verify basic TLS properties
	leaf := config.Certificates[0].Leaf
	if !leaf.IsCA {
		t.Log("leaf cert is not a CA (expected for server cert)")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("server cert missing digital signature key usage")
	}
	if len(leaf.ExtKeyUsage) == 0 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Error("server cert missing server auth extended key usage")
	}
	t.Logf("server cert: CN=%q, valid until %s", leaf.Subject.CommonName, leaf.NotAfter)
}

// TestSaveCACert verifies CA PEM persistence.
func TestSaveCACert(t *testing.T) {
	_, _, caPEM, err := GenerateInternalTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("GenerateInternalTLS: %v", err)
	}

	path, err := SaveCACert(t.TempDir(), caPEM)
	if err != nil {
		t.Fatalf("SaveCACert: %v", err)
	}

	// Verify it wrote a valid PEM file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block found")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM type = %q, want CERTIFICATE", block.Type)
	}
}
