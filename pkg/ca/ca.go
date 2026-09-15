package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CertificateManager handles creation and persistence of the Root CA and Leaf certificates.
type CertificateManager struct {
	storageDir string
	RootCert   *x509.Certificate
	RootKey    *ecdsa.PrivateKey
	LeafCert   *tls.Certificate
}

// NewCertificateManager creates or loads the local Root CA and signs a Leaf certificate.
func NewCertificateManager(storageDir string, hostIPs []net.IP, hostnames []string) (*CertificateManager, error) {
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create cert storage dir: %w", err)
	}

	cm := &CertificateManager{storageDir: storageDir}

	if err := cm.loadOrGenerateRootCA(); err != nil {
		return nil, fmt.Errorf("root CA error: %w", err)
	}

	if err := cm.generateLeafCert(hostIPs, hostnames); err != nil {
		return nil, fmt.Errorf("leaf certificate error: %w", err)
	}

	return cm, nil
}

func (cm *CertificateManager) loadOrGenerateRootCA() error {
	caCertPath := filepath.Join(cm.storageDir, "ca.crt")
	caKeyPath := filepath.Join(cm.storageDir, "ca.key")

	if fileExists(caCertPath) && fileExists(caKeyPath) {
		certPEM, err := os.ReadFile(caCertPath)
		if err != nil {
			return err
		}
		keyPEM, err := os.ReadFile(caKeyPath)
		if err != nil {
			return err
		}

		block, _ := pem.Decode(certPEM)
		if block == nil {
			return fmt.Errorf("failed to decode root cert PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}

		keyBlock, _ := pem.Decode(keyPEM)
		if keyBlock == nil {
			return fmt.Errorf("failed to decode root key PEM")
		}
		key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return err
		}

		cm.RootCert = cert
		cm.RootKey = key
		return nil
	}

	// Generate new Root CA using ECDSA P-384
	privKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate CA private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	now := time.Now().Add(-1 * time.Hour)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:       []string{"GyroBridge"},
			OrganizationalUnit: []string{"Local Motion Controller"},
			CommonName:         "GyroBridge Root CA",
		},
		NotBefore:             now,
		NotAfter:              now.Add(3650 * 24 * time.Hour), // 10 years validity
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		return fmt.Errorf("failed to create root certificate: %w", err)
	}

	parsedCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return err
	}

	// Save to disk
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(caCertPath, certPEM, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(caKeyPath, keyPEM, 0600); err != nil {
		return err
	}

	cm.RootCert = parsedCert
	cm.RootKey = privKey
	return nil
}

func (cm *CertificateManager) generateLeafCert(hostIPs []net.IP, hostnames []string) error {
	leafKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate leaf key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate leaf serial: %w", err)
	}

	// Deduplicate and filter IPs
	ipMap := make(map[string]net.IP)
	ipMap["127.0.0.1"] = net.ParseIP("127.0.0.1")
	for _, ip := range hostIPs {
		if ip != nil && !ip.IsLoopback() {
			ipMap[ip.String()] = ip
		}
	}
	var ips []net.IP
	for _, ip := range ipMap {
		ips = append(ips, ip)
	}

	// Deduplicate and filter DNS names
	dnsMap := make(map[string]bool)
	dnsMap["localhost"] = true
	dnsMap["gamepad.local"] = true
	for _, h := range hostnames {
		h = strings.TrimSpace(h)
		if h != "" {
			dnsMap[h] = true
		}
	}
	var dnsNames []string
	for name := range dnsMap {
		dnsNames = append(dnsNames, name)
	}

	now := time.Now().Add(-1 * time.Hour)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"GyroBridge"},
			CommonName:   "gamepad.local",
		},
		NotBefore:             now,
		NotAfter:              now.Add(365 * 24 * time.Hour), // 365 days (strictly <= 398 days per Apple TLS policy)
		KeyUsage:              x509.KeyUsageDigitalSignature, // ECDSA requires only digitalSignature
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		IPAddresses:           ips,
		DNSNames:              dnsNames,
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, &template, cm.RootCert, &leafKey.PublicKey, cm.RootKey)
	if err != nil {
		return fmt.Errorf("failed to sign leaf certificate: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{leafDER, cm.RootCert.Raw},
		PrivateKey:  leafKey,
	}

	cm.LeafCert = &tlsCert
	return nil
}

// GenerateMobileConfig produces an Apple .mobileconfig XML profile containing the Root CA certificate.
func (cm *CertificateManager) GenerateMobileConfig() ([]byte, error) {
	if cm.RootCert == nil {
		return nil, fmt.Errorf("root certificate not initialized")
	}

	certBase64 := base64.StdEncoding.EncodeToString(cm.RootCert.Raw)
	profileUUID, err := newUUID()
	if err != nil {
		return nil, err
	}
	payloadUUID, err := newUUID()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>GyroBridgeRootCA.cer</string>
			<key>PayloadContent</key>
			<data>
`)
	// Write wrapped base64
	for i := 0; i < len(certBase64); i += 64 {
		end := i + 64
		if end > len(certBase64) {
			end = len(certBase64)
		}
		buf.WriteString("\t\t\t" + certBase64[i:end] + "\n")
	}

	buf.WriteString(fmt.Sprintf(`			</data>
			<key>PayloadDescription</key>
			<string>Installs the local GyroBridge Root CA for motion controller connectivity.</string>
			<key>PayloadDisplayName</key>
			<string>GyroBridge Root CA</string>
			<key>PayloadIdentifier</key>
			<string>com.gyrobridge.ca.credential</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>%s</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Enables secure local HTTPS for motion sensors on iOS devices.</string>
	<key>PayloadDisplayName</key>
	<string>GyroBridge Controller Profile</string>
	<key>PayloadIdentifier</key>
	<string>com.gyrobridge.ca.profile</string>
	<key>PayloadOrganization</key>
	<string>GyroBridge</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>%s</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`, payloadUUID, profileUUID))

	return buf.Bytes(), nil
}

// RootCertPEM returns the Root CA certificate in PEM format.
func (cm *CertificateManager) RootCertPEM() []byte {
	if cm.RootCert == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cm.RootCert.Raw})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
