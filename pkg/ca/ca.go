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
	"sync"
	"time"
)

// CertificateManager handles creation and persistence of the Root CA and Leaf certificates.
type CertificateManager struct {
	storageDir string
	RootCert   *x509.Certificate
	RootKey    *ecdsa.PrivateKey
	LeafCert   *tls.Certificate
	leafKey    *ecdsa.PrivateKey
	mu         sync.RWMutex
	knownIPs   map[string]net.IP
	knownDNS   map[string]bool
}

// NewCertificateManager creates or loads the local Root CA and signs a Leaf certificate.
func NewCertificateManager(storageDir string, hostIPs []net.IP, hostnames []string) (*CertificateManager, error) {
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create cert storage dir: %w", err)
	}

	cm := &CertificateManager{
		storageDir: storageDir,
		knownIPs:   make(map[string]net.IP),
		knownDNS:   make(map[string]bool),
	}

	if err := cm.loadOrGenerateRootCA(); err != nil {
		return nil, fmt.Errorf("root CA error: %w", err)
	}

	// Register defaults
	cm.knownIPs["127.0.0.1"] = net.ParseIP("127.0.0.1")
	cm.knownDNS["localhost"] = true
	cm.knownDNS["gamepad.local"] = true

	for _, ip := range hostIPs {
		if ip != nil && !ip.IsLoopback() {
			if ip4 := ip.To4(); ip4 != nil {
				cm.knownIPs[ip4.String()] = ip4
			} else {
				cm.knownIPs[ip.String()] = ip
			}
		}
	}

	// Proactively register all non-loopback IPv4 addresses on all host interfaces
	for _, ip := range scanHostIPv4s() {
		cm.knownIPs[ip.String()] = ip
	}

	for _, h := range hostnames {
		h = strings.TrimSpace(h)
		if h != "" {
			cm.knownDNS[h] = true
		}
	}

	cm.mu.Lock()
	err := cm.generateLeafCertLocked()
	cm.mu.Unlock()
	if err != nil {
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

func (cm *CertificateManager) generateLeafCertLocked() error {
	if cm.leafKey == nil {
		leafKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			return fmt.Errorf("failed to generate leaf key: %w", err)
		}
		cm.leafKey = leafKey
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate leaf serial: %w", err)
	}

	var ips []net.IP
	for _, ip := range cm.knownIPs {
		if ip != nil {
			ips = append(ips, ip)
		}
	}

	var dnsNames []string
	for name := range cm.knownDNS {
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

	leafDER, err := x509.CreateCertificate(rand.Reader, &template, cm.RootCert, &cm.leafKey.PublicKey, cm.RootKey)
	if err != nil {
		return fmt.Errorf("failed to sign leaf certificate: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{leafDER, cm.RootCert.Raw},
		PrivateKey:  cm.leafKey,
	}

	cm.LeafCert = &tlsCert
	return nil
}

// GetCertificate dynamically inspects incoming TLS ClientHello to ensure the connecting IP is in the leaf cert SAN.
func (cm *CertificateManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var connIP net.IP
	if hello != nil && hello.Conn != nil {
		if tcpAddr, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok && tcpAddr.IP != nil {
			if ip4 := tcpAddr.IP.To4(); ip4 != nil {
				connIP = ip4
			} else {
				connIP = tcpAddr.IP
			}
		}
	}

	serverName := ""
	if hello != nil {
		serverName = strings.TrimSpace(hello.ServerName)
	}

	// Fast path: check under read lock
	cm.mu.RLock()
	ipKnown := (connIP == nil) || (cm.knownIPs[connIP.String()] != nil)
	dnsKnown := (serverName == "") || cm.knownDNS[serverName]
	if ipKnown && dnsKnown && cm.LeafCert != nil {
		cert := cm.LeafCert
		cm.mu.RUnlock()
		return cert, nil
	}
	cm.mu.RUnlock()

	// Slow path: update known IPs/DNS and regenerate leaf certificate
	cm.mu.Lock()
	defer cm.mu.Unlock()

	needRebuild := false
	if connIP != nil && cm.knownIPs[connIP.String()] == nil {
		cm.knownIPs[connIP.String()] = connIP
		needRebuild = true
	}
	if serverName != "" && !cm.knownDNS[serverName] {
		cm.knownDNS[serverName] = true
		needRebuild = true
	}

	for _, hostIP := range scanHostIPv4s() {
		if cm.knownIPs[hostIP.String()] == nil {
			cm.knownIPs[hostIP.String()] = hostIP
			needRebuild = true
		}
	}

	if needRebuild || cm.LeafCert == nil {
		if err := cm.generateLeafCertLocked(); err != nil {
			if cm.LeafCert != nil {
				return cm.LeafCert, nil
			}
			return nil, err
		}
	}

	return cm.LeafCert, nil
}

// AddHostIPs dynamically registers new host IPs and regenerates the leaf cert if necessary.
func (cm *CertificateManager) AddHostIPs(ips []net.IP) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	changed := false
	for _, ip := range ips {
		if ip != nil && !ip.IsLoopback() {
			ipStr := ip.String()
			if cm.knownIPs[ipStr] == nil {
				if ip4 := ip.To4(); ip4 != nil {
					cm.knownIPs[ipStr] = ip4
				} else {
					cm.knownIPs[ipStr] = ip
				}
				changed = true
			}
		}
	}

	if changed {
		_ = cm.generateLeafCertLocked()
	}
}

// EnsureIP checks if a specific IP is already in SAN, and regenerates leaf cert if not.
func (cm *CertificateManager) EnsureIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ipStr := ip.String()

	cm.mu.RLock()
	if cm.knownIPs[ipStr] != nil && cm.LeafCert != nil {
		cm.mu.RUnlock()
		return true
	}
	cm.mu.RUnlock()

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if ip4 := ip.To4(); ip4 != nil {
		cm.knownIPs[ipStr] = ip4
	} else {
		cm.knownIPs[ipStr] = ip
	}
	return cm.generateLeafCertLocked() == nil
}

func scanHostIPv4s() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				ip4 := ip.To4()
				if ip4 != nil && !ip4.IsLoopback() && !ip4.IsUnspecified() {
					ips = append(ips, ip4)
				}
			}
		}
	}
	return ips
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
