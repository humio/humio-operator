package helpers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
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

type WebhookCertGenerator struct {
	CertPath    string
	CertName    string
	KeyName     string
	ServiceName string
	Namespace   string
	CertHash    string
}

func NewCertGenerator(certPath, certName, keyName, serviceName, namespace string) *WebhookCertGenerator {
	return &WebhookCertGenerator{
		CertPath:    certPath,
		CertName:    certName,
		KeyName:     keyName,
		ServiceName: serviceName,
		Namespace:   namespace,
		CertHash:    "",
	}
}

func (c *WebhookCertGenerator) GenerateIfNotExists() error {
	certFile := filepath.Join(c.CertPath, c.CertName)
	keyFile := filepath.Join(c.CertPath, c.KeyName)

	// Check if certificate already exists and is valid
	if c.certificatesValid(certFile, keyFile) {
		return nil
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(c.CertPath, 0750); err != nil {
		return fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Generate new certificate / pk
	certPEM, keyPEM, err := c.generateCertificate()
	if err != nil {
		return fmt.Errorf("failed to generate certificates: %w", err)
	}

	// Write certificate to file
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write certificate file: %w", err)
	}

	// Write PK to file
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
}

func (c *WebhookCertGenerator) certificatesValid(certFile, keyFile string) bool {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return false
	}

	// Read and parse certificate
	certPEM, err := os.ReadFile(filepath.Clean(certFile))
	if err != nil {
		return false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Check if certificate is still valid (not expired and not expiring within 30 days)
	now := time.Now()
	if now.After(cert.NotAfter) || now.Add(30*24*time.Hour).After(cert.NotAfter) {
		return false
	}

	c.CertHash = fmt.Sprintf("%x", sha256.Sum256(certPEM))

	return true
}

func (c *WebhookCertGenerator) generateCertificate() ([]byte, []byte, error) {
	if c.Namespace == "" {
		return nil, nil, fmt.Errorf("namespace field is mandatory for certificate issuance, received: %s", c.Namespace)
	}
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			SerialNumber: fmt.Sprintf("%d", time.Now().Unix()),
			CommonName:   c.ServiceName,
		},
		DNSNames: []string{
			c.ServiceName,
			fmt.Sprintf("%s.%s", c.ServiceName, c.Namespace),
			fmt.Sprintf("%s.%s.svc", c.ServiceName, c.Namespace),
			fmt.Sprintf("%s.%s.svc.cluster.local", c.ServiceName, c.Namespace),
		},
		IPAddresses: []net.IP{
			net.IPv4(127, 0, 0, 1),
			net.IPv6loopback,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // Valid for 10 year
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Generate certificate (self-signed)
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	c.CertHash = fmt.Sprintf("%x", sha256.Sum256(certPEM))

	return certPEM, privateKeyPEM, nil
}

// GetCABundle returns the CA certificate bundle (in this case, the self-signed cert)
func (c *WebhookCertGenerator) GetCABundle() ([]byte, error) {
	certFile := filepath.Join(c.CertPath, c.CertName)
	return os.ReadFile(filepath.Clean(certFile))
}
