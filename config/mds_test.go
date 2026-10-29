package config

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/google/uuid"
)

func TestLocalMetadataLoadingAndAlgorithms(t *testing.T) {
	prov, err := NewConformanceMDSProvider("../fido-conformance-mds", MDS3Config{Enabled: false})
	if err != nil {
		t.Fatalf("NewConformanceMDSProvider failed: %v", err)
	}

	ctx := context.Background()

	// 1. Verify FIDO2 AAGUID lookup from Virtual FIDO2 EdDSA25519 authenticator JSON
	edAAGUID := uuid.MustParse("91dfead7-959e-4475-ad26-9b0d482be089")
	entry, err := prov.GetEntry(ctx, edAAGUID)
	if err != nil || entry == nil {
		t.Fatalf("expected metadata entry for AAGUID %s, got entry=%v err=%v", edAAGUID, entry, err)
	}

	// 2. Verify U2F key identifier lookup (case-insensitive)
	u2fKID := "564DF7C0F8C655B6A11F6C4D19F3BF41E2FD0179"
	u2fEntry, err := prov.GetEntryByKeyIdentifier(ctx, u2fKID)
	if err != nil || u2fEntry == nil {
		t.Fatalf("expected U2F metadata entry for key identifier %s, got entry=%v err=%v", u2fKID, u2fEntry, err)
	}

	// 3. Verify ValidateZeroAAGUIDAttestationCert rejects untrusted leaf cert on zero AAGUID
	untrustedKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(999),
		Subject:      pkix.Name{CommonName: "Untrusted U2F Leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &untrustedKey.PublicKey, untrustedKey)
	if err != nil {
		t.Fatal(err)
	}
	zeroAAGUID := make([]byte, 16)
	if err := prov.ValidateZeroAAGUIDAttestationCert(ctx, zeroAAGUID, map[string]any{
		"x5c": []any{leafDER},
	}); err == nil {
		t.Fatal("expected ValidateZeroAAGUIDAttestationCert to reject unknown leaf certificate")
	}

	// Surrogate / self-attestation (no x5c) with zero AAGUID should pass
	if err := prov.ValidateZeroAAGUIDAttestationCert(ctx, zeroAAGUID, map[string]any{"sig": []byte{1, 2, 3}}); err != nil {
		t.Fatalf("expected zero AAGUID without x5c to pass, got: %v", err)
	}

	// 4. Verify ConformanceCredentialParameters includes ES256K (-47) and RS1 (-65535)
	params := ConformanceCredentialParameters()
	var algs []webauthncose.COSEAlgorithmIdentifier
	for _, p := range params {
		algs = append(algs, p.Algorithm)
	}
	if !slices.Contains(algs, webauthncose.AlgES256K) {
		t.Fatalf("expected ConformanceCredentialParameters to include ES256K (-47), got %v", algs)
	}
	if !slices.Contains(algs, webauthncose.AlgRS1) {
		t.Fatalf("expected ConformanceCredentialParameters to include RS1 (-65535), got %v", algs)
	}
}

func signES256JWS(t *testing.T, priv *ecdsa.PrivateKey, header map[string]any, payload map[string]any) string {
	t.Helper()
	hBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	pBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hBytes) + "." + base64.RawURLEncoding.EncodeToString(pBytes)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeMDSBlobPayload(no int, nextUpdate string, aaguid string, status string) map[string]any {
	return map[string]any{
		"legalHeader": "https://fidoalliance.org/metadata/metadata-statement-legal-header/",
		"no":          no,
		"nextUpdate":  nextUpdate,
		"entries": []any{
			map[string]any{
				"aaguid": aaguid,
				"statusReports": []any{
					map[string]any{
						"status":        status,
						"effectiveDate": "2025-01-01",
					},
				},
				"timeOfLastStatusChange": "2025-01-01",
				"metadataStatement": map[string]any{
					"legalHeader":              "https://fidoalliance.org/metadata/metadata-statement-legal-header/",
					"aaguid":                   aaguid,
					"description":              "Dynamic MDS3 Test Authenticator " + aaguid,
					"authenticatorVersion":     1,
					"protocolFamily":           "fido2",
					"schema":                   3,
					"upv":                      []any{map[string]any{"major": 1, "minor": 0}},
					"authenticationAlgorithms": []any{"secp256r1_ecdsa_sha256_raw"},
					"publicKeyAlgAndEncodings": []any{"cose"},
					"attestationTypes":         []any{"basic_full"},
					"userVerificationDetails": []any{
						[]any{map[string]any{"userVerificationMethod": "none"}},
					},
					"keyProtection":               []any{"hardware"},
					"matcherProtection":           []any{"on_chip"},
					"cryptoStrength":              128,
					"attachmentHint":              []any{"external"},
					"tcDisplay":                   []any{},
					"attestationRootCertificates": []any{},
				},
			},
		},
	}
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (rt handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestDynamicMDS3BlobsAndRefresh(t *testing.T) {
	// Build MDS3 PKI: Root CA -> Intermediate CA -> Valid Leaf & Revoked Leaf
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test MDS3 Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test MDS3 Intermediate CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootCert, &interKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	interCert, err := x509.ParseCertificate(interDER)
	if err != nil {
		t.Fatal(err)
	}

	var baseURL string
	var x5cVersion atomic.Int32
	x5cVersion.Store(1)

	validX5CAAGUID := "11111111-2222-3333-4444-555555555555"
	validX5UAAGUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	revokedX5UAAGUID := "99999999-8888-7777-6666-555555555555"
	badOriginAAGUID := "cccccccc-dddd-eeee-ffff-000000000000"
	badSigAAGUID := "dddddddd-eeee-ffff-0000-111111111111"

	var validLeafKey, revokedLeafKey *ecdsa.PrivateKey
	var validChainPEM, revokedChainPEM, crlDER []byte
	var validLeafDER []byte

	mux := http.NewServeMux()
	baseURL = "https://mds3.fido.tools"

	crlURL := baseURL + "/crl/inter.crl"

	validLeafKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	validLeafTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "Valid MDS3 Signer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		CRLDistributionPoints: []string{crlURL},
	}
	validLeafDER, err = x509.CreateCertificate(rand.Reader, validLeafTmpl, interCert, &validLeafKey.PublicKey, interKey)
	if err != nil {
		t.Fatal(err)
	}

	revokedLeafKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	revokedLeafTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(200),
		Subject:               pkix.Name{CommonName: "Revoked MDS3 Signer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		CRLDistributionPoints: []string{crlURL},
	}
	revokedLeafDER, err := x509.CreateCertificate(rand.Reader, revokedLeafTmpl, interCert, &revokedLeafKey.PublicKey, interKey)
	if err != nil {
		t.Fatal(err)
	}

	validChainPEM = append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: validLeafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER})...,
	)
	revokedChainPEM = append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: revokedLeafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER})...,
	)

	crlDER, err = x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:     big.NewInt(1),
		ThisUpdate: time.Now().Add(-time.Minute),
		NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{
			{
				SerialNumber:   big.NewInt(200),
				RevocationTime: time.Now().Add(-10 * time.Minute),
			},
		},
	}, interCert, interKey)
	if err != nil {
		t.Fatal(err)
	}

	mux.HandleFunc("/pki/MDS3ROOT.crt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rootDER)
	})
	mux.HandleFunc("/crl/inter.crl", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(crlDER)
	})
	mux.HandleFunc("/certs/valid.pem", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validChainPEM)
	})
	mux.HandleFunc("/certs/revoked.pem", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(revokedChainPEM)
	})
	mux.HandleFunc("/getEndpoints", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"result": []string{
				baseURL + "/blobs/valid-x5c",
				baseURL + "/blobs/valid-x5u",
				baseURL + "/blobs/revoked-x5u",
				baseURL + "/blobs/bad-origin-x5u",
				baseURL + "/blobs/bad-sig",
			},
		})
	})

	x5cChain := []string{
		base64.StdEncoding.EncodeToString(validLeafDER),
		base64.StdEncoding.EncodeToString(interDER),
	}

	mux.HandleFunc("/blobs/valid-x5c", func(w http.ResponseWriter, r *http.Request) {
		ver := int(x5cVersion.Load())
		status := "FIDO_CERTIFIED"
		nextUpd := "2020-01-01" // past date so refreshExpiredIfNeeded triggers when cooldown elapses
		if ver > 1 {
			status = "REVOKED"
			nextUpd = time.Now().Add(24 * time.Hour).Format("2006-01-02")
		}
		jws := signES256JWS(t, validLeafKey, map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5c": x5cChain,
		}, makeMDSBlobPayload(ver, nextUpd, validX5CAAGUID, status))
		_, _ = w.Write([]byte(jws))
	})

	mux.HandleFunc("/blobs/valid-x5u", func(w http.ResponseWriter, r *http.Request) {
		jws := signES256JWS(t, validLeafKey, map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5u": baseURL + "/certs/valid.pem",
		}, makeMDSBlobPayload(1, "2099-01-01", validX5UAAGUID, "FIDO_CERTIFIED"))
		_, _ = w.Write([]byte(jws))
	})

	mux.HandleFunc("/blobs/revoked-x5u", func(w http.ResponseWriter, r *http.Request) {
		jws := signES256JWS(t, revokedLeafKey, map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5u": baseURL + "/certs/revoked.pem",
		}, makeMDSBlobPayload(1, "2099-01-01", revokedX5UAAGUID, "FIDO_CERTIFIED"))
		_, _ = w.Write([]byte(jws))
	})

	mux.HandleFunc("/blobs/bad-origin-x5u", func(w http.ResponseWriter, r *http.Request) {
		jws := signES256JWS(t, validLeafKey, map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5u": "http://evil.example.com/certs/valid.pem",
		}, makeMDSBlobPayload(1, "2099-01-01", badOriginAAGUID, "FIDO_CERTIFIED"))
		_, _ = w.Write([]byte(jws))
	})

	mux.HandleFunc("/blobs/bad-sig", func(w http.ResponseWriter, r *http.Request) {
		jws := signES256JWS(t, revokedLeafKey, map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5c": x5cChain, // signed with wrong key
		}, makeMDSBlobPayload(1, "2099-01-01", badSigAAGUID, "FIDO_CERTIFIED"))
		_, _ = w.Write([]byte(jws))
	})

	prov, err := NewConformanceMDSProvider("../fido-conformance-mds", MDS3Config{
		Enabled:         true,
		EndpointsURL:    baseURL + "/getEndpoints",
		ConformanceURL:  "http://localhost:8080",
		RootCertURL:     baseURL + "/pki/MDS3ROOT.crt",
		CustomTransport: handlerRoundTripper{handler: mux},
	})
	if err != nil {
		t.Fatalf("NewConformanceMDSProvider with MDS3 failed: %v", err)
	}

	ctx := context.Background()

	// Valid x5c and x5u blobs must be loaded
	e1, err := prov.GetEntry(ctx, uuid.MustParse(validX5CAAGUID))
	if err != nil || e1 == nil {
		t.Fatalf("expected valid x5c entry, got entry=%v err=%v", e1, err)
	}
	if err := prov.ValidateStatusReports(ctx, e1.StatusReports); err != nil {
		t.Fatalf("expected FIDO_CERTIFIED status to pass, got %v", err)
	}

	e2, err := prov.GetEntry(ctx, uuid.MustParse(validX5UAAGUID))
	if err != nil || e2 == nil {
		t.Fatalf("expected valid x5u entry, got entry=%v err=%v", e2, err)
	}

	// Poisoned/invalid blobs must have been skipped without failing provider startup
	for _, rejected := range []string{revokedX5UAAGUID, badOriginAAGUID, badSigAAGUID} {
		got, _ := prov.GetEntry(ctx, uuid.MustParse(rejected))
		if got != nil {
			t.Fatalf("expected poisoned MDS3 entry %s to be rejected, but it was loaded", rejected)
		}
	}

	// Simulate nextUpdate refresh advancing valid-x5c from FIDO_CERTIFIED (no=1) to REVOKED (no=2)
	x5cVersion.Store(2)
	prov.mu.Lock()
	prov.endpointState[baseURL+"/blobs/valid-x5c"].LastChecked = time.Now().Add(-10 * time.Second)
	prov.mu.Unlock()

	refreshed, err := prov.GetEntry(ctx, uuid.MustParse(validX5CAAGUID))
	if err != nil || refreshed == nil {
		t.Fatalf("expected refreshed entry, got entry=%v err=%v", refreshed, err)
	}
	if err := prov.ValidateStatusReports(ctx, refreshed.StatusReports); err == nil {
		t.Fatal("expected refreshed REVOKED status report to be rejected by ValidateStatusReports")
	}
}
