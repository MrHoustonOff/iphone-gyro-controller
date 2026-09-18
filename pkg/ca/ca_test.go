package ca

import (
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertificateManager_CreationAndVerification(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyrobridge_ca_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testIPs := []net.IP{net.ParseIP("192.168.1.50"), net.ParseIP("10.0.0.2")}
	testHosts := []string{"gamepad.local", "custom.host"}

	cm, err := NewCertificateManager(tmpDir, testIPs, testHosts)
	if err != nil {
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	// 1. Verify Root CA properties
	if cm.RootCert == nil {
		t.Fatal("RootCert is nil")
	}
	if !cm.RootCert.IsCA {
		t.Error("RootCert IsCA should be true")
	}
	if cm.RootCert.Subject.CommonName != "GyroBridge Root CA" {
		t.Errorf("unexpected CommonName: %s", cm.RootCert.Subject.CommonName)
	}

	// 2. Verify Leaf certificate against Root CA
	if cm.LeafCert == nil || len(cm.LeafCert.Certificate) == 0 {
		t.Fatal("LeafCert is invalid")
	}

	leafX509, err := x509.ParseCertificate(cm.LeafCert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf certificate: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(cm.RootCert)

	opts := x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: time.Now(),
		DNSName:     "gamepad.local",
	}
	if _, err := leafX509.Verify(opts); err != nil {
		t.Errorf("leaf certificate failed verification against Root CA: %v", err)
	}

	// 3. Verify SANs
	foundIP := false
	for _, ip := range leafX509.IPAddresses {
		if ip.String() == "192.168.1.50" {
			foundIP = true
			break
		}
	}
	if !foundIP {
		t.Error("leaf SAN does not contain 192.168.1.50")
	}

	foundDNS := false
	for _, dns := range leafX509.DNSNames {
		if dns == "gamepad.local" {
			foundDNS = true
			break
		}
	}
	if !foundDNS {
		t.Error("leaf SAN does not contain gamepad.local")
	}
}

func TestCertificateManager_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyrobridge_persist_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testIPs := []net.IP{net.ParseIP("192.168.1.50")}

	// First initialization
	cm1, err := NewCertificateManager(tmpDir, testIPs, nil)
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	rootSerial1 := cm1.RootCert.SerialNumber.String()

	// Ensure files exist
	if _, err := os.Stat(filepath.Join(tmpDir, "ca.crt")); err != nil {
		t.Errorf("ca.crt does not exist: %v", err)
	}

	// Second initialization should reload the same Root CA
	cm2, err := NewCertificateManager(tmpDir, testIPs, nil)
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}
	rootSerial2 := cm2.RootCert.SerialNumber.String()

	if rootSerial1 != rootSerial2 {
		t.Errorf("root CA was regenerated instead of reloaded! %s != %s", rootSerial1, rootSerial2)
	}
}

func TestCertificateManager_MobileConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyrobridge_mc_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cm, err := NewCertificateManager(tmpDir, nil, nil)
	if err != nil {
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	mcBytes, err := cm.GenerateMobileConfig()
	if err != nil {
		t.Fatalf("GenerateMobileConfig failed: %v", err)
	}

	mcStr := string(mcBytes)
	if !strings.Contains(mcStr, "<key>PayloadType</key>") {
		t.Error("mobileconfig missing PayloadType key")
	}
	if !strings.Contains(mcStr, "com.apple.security.root") {
		t.Error("mobileconfig missing com.apple.security.root payload type")
	}
	if !strings.Contains(mcStr, "com.gyrobridge.ca.profile") {
		t.Error("mobileconfig missing profile identifier")
	}
}

func TestCertificateManager_DynamicSAN(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyrobridge_dyn_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cm, err := NewCertificateManager(tmpDir, []net.IP{net.ParseIP("192.168.1.10")}, nil)
	if err != nil {
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	// Dynamically add a new IP (e.g. from DHCP change or USB tethering)
	newIP := net.ParseIP("172.20.10.2")
	cm.AddHostIPs([]net.IP{newIP})

	cert, err := cm.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate failed: %v", err)
	}

	leafX509, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf: %v", err)
	}

	found := false
	for _, ip := range leafX509.IPAddresses {
		if ip.String() == "172.20.10.2" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 172.20.10.2 to be present in dynamic leaf cert SAN")
	}
}

