package config

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/metadata"
	"github.com/go-webauthn/webauthn/metadata/providers/memory"
	"github.com/google/uuid"
)

const (
	defaultLocalMDSDir      = "fido-conformance-mds"
	defaultMDS3EndpointsURL = "https://mds3.fido.tools/getEndpoints"
	defaultMDS3RootCertURL  = "https://mds3.fido.tools/pki/MDS3ROOT.crt"
	mdsRefreshCooldown      = 2 * time.Second
)

type mdsEndpointState struct {
	URL         string
	Number      int
	NextUpdate  time.Time
	LastChecked time.Time
	Entries     []metadata.Entry
}

// ConformanceMDSProvider combines static authenticator metadata statements from fido-conformance-mds/
// with optional dynamic FIDO MDS3 conformance JWT blobs (including x5c, x5u, CRL, and nextUpdate support).
type ConformanceMDSProvider struct {
	mu sync.RWMutex

	staticAAGUID map[uuid.UUID]*metadata.Entry
	staticKeyIDs map[string]*metadata.Entry

	inner metadata.ExtendedProvider

	mds3Enabled   bool
	httpClient    *http.Client
	rootCert      *x509.Certificate
	rootCertB64   string
	decoder       *metadata.Decoder
	endpointState map[string]*mdsEndpointState
}

var _ metadata.ExtendedProvider = (*ConformanceMDSProvider)(nil)

// MDS3Config configures optional dynamic MDS3 blob loading for FIDO Conformance tests.
type MDS3Config struct {
	Enabled         bool
	EndpointsURL    string
	ConformanceURL  string
	RootCertURL     string
	RootCertPath    string
	CustomTransport http.RoundTripper
}

// NewConformanceMDSProvider loads static metadata JSON files from mdsDir and optionally bootstraps dynamic MDS3 blobs.
func NewConformanceMDSProvider(mdsDir string, mds3Cfg MDS3Config) (*ConformanceMDSProvider, error) {
	if strings.TrimSpace(mdsDir) == "" {
		mdsDir = defaultLocalMDSDir
	}

	staticAAGUID, staticKeyIDs, err := loadLocalMetadataDir(mdsDir)
	if err != nil {
		return nil, err
	}

	p := &ConformanceMDSProvider{
		staticAAGUID:  staticAAGUID,
		staticKeyIDs:  staticKeyIDs,
		mds3Enabled:   mds3Cfg.Enabled,
		endpointState: make(map[string]*mdsEndpointState),
	}

	if err := p.rebuildLocked(); err != nil {
		return nil, err
	}

	if mds3Cfg.Enabled {
		if err := p.initMDS3(mds3Cfg); err != nil {
			return nil, fmt.Errorf("failed to initialize dynamic MDS3: %w", err)
		}
	}

	return p, nil
}

func loadLocalMetadataDir(mdsDir string) (map[uuid.UUID]*metadata.Entry, map[string]*metadata.Entry, error) {
	entries, err := os.ReadDir(mdsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read FIDO MDS directory %q: %w", mdsDir, err)
	}

	byAAGUID := make(map[uuid.UUID]*metadata.Entry)
	byKeyID := make(map[string]*metadata.Entry)
	loaded := 0

	for _, de := range entries {
		if de.IsDir() || !strings.EqualFold(filepath.Ext(de.Name()), ".json") {
			continue
		}

		fullPath := filepath.Join(mdsDir, de.Name())
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read metadata file %q: %w", fullPath, err)
		}

		sanitized, err := sanitizeMetadataJSON(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to sanitize metadata statement %q: %w", fullPath, err)
		}

		var stmtJSON metadata.StatementJSON
		if err := json.Unmarshal(sanitized, &stmtJSON); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal metadata statement %q: %w", fullPath, err)
		}

		stmt, err := stmtJSON.Parse()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse metadata statement %q: %w", fullPath, err)
		}

		entry := &metadata.Entry{
			Aaid:                                 stmt.Aaid,
			AaGUID:                               stmt.AaGUID,
			AttestationCertificateKeyIdentifiers: slices.Clone(stmt.AttestationCertificateKeyIdentifiers),
			MetadataStatement:                    stmt,
			TimeOfLastStatusChange:               time.Now().UTC(),
		}

		if entry.AaGUID != uuid.Nil {
			byAAGUID[entry.AaGUID] = entry
		}
		for _, kid := range entry.AttestationCertificateKeyIdentifiers {
			norm := strings.ToLower(strings.TrimSpace(kid))
			if norm != "" {
				byKeyID[norm] = entry
			}
		}
		loaded++
	}

	if loaded == 0 {
		return nil, nil, fmt.Errorf("no metadata .json statements found in %q", mdsDir)
	}

	log.Printf("Loaded %d local FIDO metadata statements (%d AAGUIDs, %d key identifiers) from %s",
		loaded, len(byAAGUID), len(byKeyID), mdsDir)
	return byAAGUID, byKeyID, nil
}

func (p *ConformanceMDSProvider) rebuildLocked() error {
	mergedAAGUID := make(map[uuid.UUID]*metadata.Entry, len(p.staticAAGUID))
	for k, v := range p.staticAAGUID {
		mergedAAGUID[k] = v
	}

	mergedKeyID := make(map[string]*metadata.Entry, len(p.staticKeyIDs))
	for k, v := range p.staticKeyIDs {
		mergedKeyID[k] = v
	}

	// Overlay dynamic MDS3 entries (which carry live StatusReports such as REVOKED / ATTESTATION_KEY_COMPROMISE)
	for _, st := range p.endpointState {
		for i := range st.Entries {
			e := st.Entries[i]
			entryCopy := e
			if entryCopy.AaGUID != uuid.Nil {
				mergedAAGUID[entryCopy.AaGUID] = &entryCopy
			}
			for _, kid := range entryCopy.AttestationCertificateKeyIdentifiers {
				norm := strings.ToLower(strings.TrimSpace(kid))
				if norm != "" {
					mergedKeyID[norm] = &entryCopy
				}
			}
			for _, kid := range entryCopy.MetadataStatement.AttestationCertificateKeyIdentifiers {
				norm := strings.ToLower(strings.TrimSpace(kid))
				if norm != "" {
					mergedKeyID[norm] = &entryCopy
				}
			}
		}
	}

	prov, err := memory.New(
		memory.WithMetadata(mergedAAGUID),
		memory.WithMetadataKeyIdentifiers(mergedKeyID),
		memory.WithValidateEntry(true),
		memory.WithValidateEntryKeyIdentifier(true),
		memory.WithValidateEntryPermitZeroAAGUID(true),
		memory.WithValidateTrustAnchor(true),
		memory.WithValidateStatus(true),
		memory.WithValidateStatusCertificateScope(true),
		memory.WithValidateAttestationTypes(true),
		memory.WithValidateAAGUID(true),
	)
	if err != nil {
		return err
	}

	ext, ok := prov.(metadata.ExtendedProvider)
	if !ok {
		return errors.New("memory metadata provider does not implement metadata.ExtendedProvider")
	}

	p.inner = ext
	return nil
}

func (p *ConformanceMDSProvider) initMDS3(cfg MDS3Config) error {
	endpointsURL := strings.TrimSpace(cfg.EndpointsURL)
	if endpointsURL == "" {
		endpointsURL = defaultMDS3EndpointsURL
	}
	rootCertURL := strings.TrimSpace(cfg.RootCertURL)
	if rootCertURL == "" {
		rootCertURL = defaultMDS3RootCertURL
	}
	conformanceURL := strings.TrimSpace(cfg.ConformanceURL)
	if conformanceURL == "" {
		conformanceURL = GetConformanceEndpoint()
	}

	baseClient := &http.Client{Timeout: metadata.DefaultMDSTimeout}
	if cfg.CustomTransport != nil {
		baseClient.Transport = cfg.CustomTransport
	}

	var rawRoot []byte
	var err error
	if path := strings.TrimSpace(cfg.RootCertPath); path != "" {
		rawRoot, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read MDS3 root certificate file %q: %w", path, err)
		}
	} else {
		resp, err := baseClient.Get(rootCertURL)
		if err != nil {
			return fmt.Errorf("failed to download MDS3 root certificate from %q: %w", rootCertURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status %d fetching MDS3 root certificate from %q", resp.StatusCode, rootCertURL)
		}
		rawRoot, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read MDS3 root certificate body: %w", err)
		}
	}

	rootCert, err := parseCertPEMOrDER(rawRoot)
	if err != nil {
		return fmt.Errorf("failed to parse MDS3 root certificate: %w", err)
	}
	p.rootCert = rootCert
	p.rootCertB64 = base64.StdEncoding.EncodeToString(rootCert.Raw)

	if cfg.CustomTransport == nil {
		sysPool, err := x509.SystemCertPool()
		if err != nil || sysPool == nil {
			sysPool = x509.NewCertPool()
		}
		sysPool.AddCert(rootCert)
		baseClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    sysPool,
				MinVersion: tls.VersionTLS12,
			},
		}
	}
	p.httpClient = baseClient

	decoder, err := metadata.NewDecoder(
		metadata.WithRootCertificate(p.rootCertB64),
		metadata.WithIgnoreEntryParsingErrors(),
	)
	if err != nil {
		return fmt.Errorf("failed to create MDS3 decoder: %w", err)
	}
	p.decoder = decoder

	reqBody, err := json.Marshal(metadata.MDSGetEndpointsRequest{Endpoint: conformanceURL})
	if err != nil {
		return err
	}

	resp, err := p.httpClient.Post(endpointsURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", endpointsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s returned HTTP %d", endpointsURL, resp.StatusCode)
	}

	var epResp metadata.MDSGetEndpointsResponse
	if err := json.NewDecoder(resp.Body).Decode(&epResp); err != nil {
		return fmt.Errorf("failed to decode getEndpoints response: %w", err)
	}
	if !strings.EqualFold(epResp.Status, "ok") {
		return fmt.Errorf("getEndpoints returned non-ok status %q", epResp.Status)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	validCount := 0
	for _, blobURL := range epResp.Result {
		parsed, err := p.fetchAndVerifyMDS3Blob(blobURL)
		if err != nil {
			// Conformance getEndpoints intentionally includes negative/poisoned blob URLs (bad sig, revoked cert, bad x5u).
			log.Printf("Skipping untrusted/invalid MDS3 blob %s: %v", blobURL, err)
			continue
		}
		p.endpointState[blobURL] = &mdsEndpointState{
			URL:         blobURL,
			Number:      parsed.Parsed.Number,
			NextUpdate:  parsed.Parsed.NextUpdate,
			LastChecked: time.Now(),
			Entries:     parsed.Parsed.Entries,
		}
		validCount++
	}

	log.Printf("MDS3 initialized: %d/%d blob endpoints validated", validCount, len(epResp.Result))
	return p.rebuildLocked()
}

func (p *ConformanceMDSProvider) refreshExpiredIfNeeded() {
	if !p.mds3Enabled || p.httpClient == nil {
		return
	}

	now := time.Now()

	p.mu.RLock()
	var due []string
	for u, st := range p.endpointState {
		if !st.NextUpdate.IsZero() && !now.Before(st.NextUpdate) && now.Sub(st.LastChecked) >= mdsRefreshCooldown {
			due = append(due, u)
		}
	}
	p.mu.RUnlock()

	if len(due) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	updated := false
	now = time.Now()
	for _, u := range due {
		st, ok := p.endpointState[u]
		if !ok || now.Sub(st.LastChecked) < mdsRefreshCooldown {
			continue
		}
		st.LastChecked = now

		parsed, err := p.fetchAndVerifyMDS3Blob(u)
		if err != nil {
			log.Printf("Ignoring invalid MDS3 refresh from %s: %v", u, err)
			continue
		}
		if parsed.Parsed.Number > st.Number {
			st.Number = parsed.Parsed.Number
			st.NextUpdate = parsed.Parsed.NextUpdate
			st.Entries = parsed.Parsed.Entries
			updated = true
		}
	}

	if updated {
		if err := p.rebuildLocked(); err != nil {
			log.Printf("Failed to rebuild MDS provider after refresh: %v", err)
		}
	}
}

func (p *ConformanceMDSProvider) fetchAndVerifyMDS3Blob(blobURL string) (*metadata.Metadata, error) {
	resp, err := p.httpClient.Get(blobURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	rawJWT, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	rawJWT = bytes.TrimSpace(rawJWT)

	parts := strings.Split(string(rawJWT), ".")
	if len(parts) != 3 {
		return nil, errors.New("MDS3 blob is not a 3-part JWS")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid JWS header encoding: %w", err)
	}

	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("invalid JWS header JSON: %w", err)
	}

	payload, err := p.verifyMDS3JWS(blobURL, header, parts)
	if err != nil {
		return nil, err
	}
	return p.decoder.Parse(payload)
}

func (p *ConformanceMDSProvider) verifyMDS3JWS(blobURL string, header map[string]any, parts []string) (*metadata.PayloadJSON, error) {
	var certs []*x509.Certificate

	if x5uRaw, hasX5U := header[metadata.HeaderX509URI]; hasX5U {
		x5uStr, ok := x5uRaw.(string)
		if !ok || strings.TrimSpace(x5uStr) == "" {
			return nil, errors.New("invalid x5u header in MDS3 JWS")
		}

		blobParsed, err := url.Parse(blobURL)
		if err != nil {
			return nil, fmt.Errorf("invalid blob URL: %w", err)
		}
		x5uParsed, err := url.Parse(x5uStr)
		if err != nil {
			return nil, fmt.Errorf("invalid x5u URL: %w", err)
		}

		// FIDO MDS3 §3.1.2: x5u MUST use HTTPS and share the exact web-origin (scheme + host) of the metadata BLOB URL.
		if !strings.EqualFold(x5uParsed.Scheme, "https") ||
			!strings.EqualFold(x5uParsed.Scheme, blobParsed.Scheme) ||
			!strings.EqualFold(x5uParsed.Host, blobParsed.Host) {
			return nil, fmt.Errorf("x5u origin %q does not match HTTPS blob origin %q", x5uStr, blobURL)
		}

		resp, err := p.httpClient.Get(x5uStr)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch x5u chain: %w", err)
		}
		chainBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("x5u endpoint returned HTTP %d", resp.StatusCode)
		}

		certs, err = parseCertChainPEMOrDER(chainBytes)
		if err != nil || len(certs) == 0 {
			return nil, fmt.Errorf("failed to parse x5u certificate chain: %w", err)
		}
	} else if x5cRaw, hasX5C := header[metadata.HeaderX509Certificate]; hasX5C {
		x5cList, ok := x5cRaw.([]any)
		if !ok || len(x5cList) == 0 {
			return nil, errors.New("invalid x5c header in MDS3 JWS")
		}
		certs = make([]*x509.Certificate, 0, len(x5cList))
		for _, item := range x5cList {
			b64Str, ok := item.(string)
			if !ok || strings.TrimSpace(b64Str) == "" {
				return nil, errors.New("non-string entry in x5c header")
			}
			der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Str))
			if err != nil {
				return nil, fmt.Errorf("invalid base64 certificate in x5c: %w", err)
			}
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("invalid X.509 certificate in x5c: %w", err)
			}
			certs = append(certs, c)
		}
	} else {
		// FIDO MDS3 §3.1.3: if neither x5u nor x5c is present, the trust anchor is the signing certificate.
		certs = []*x509.Certificate{p.rootCert}
	}

	roots := x509.NewCertPool()
	roots.AddCert(p.rootCert)
	intermediates := x509.NewCertPool()
	for _, ic := range certs[1:] {
		if !ic.Equal(p.rootCert) {
			intermediates.AddCert(ic)
		}
	}

	chains, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil || len(chains) == 0 {
		return nil, fmt.Errorf("MDS3 certificate chain verification failed: %w", err)
	}

	// Check CRL distribution points along the verified chain
	verifiedChain := chains[0]
	for i := 0; i < len(verifiedChain)-1; i++ {
		if err := p.checkCertCRL(verifiedChain[i], verifiedChain[i+1]); err != nil {
			return nil, err
		}
	}

	alg, _ := header["alg"].(string)
	signingInput := []byte(parts[0] + "." + parts[1])
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid JWS signature encoding: %w", err)
	}

	if err := verifyJWSSignature(alg, certs[0].PublicKey, signingInput, sigBytes); err != nil {
		return nil, fmt.Errorf("MDS3 JWS signature verification failed: %w", err)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWS payload encoding: %w", err)
	}

	sanitizedPayload, err := sanitizeMetadataJSON(payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize MDS3 payload JSON: %w", err)
	}

	var payload metadata.PayloadJSON
	if err := json.Unmarshal(sanitizedPayload, &payload); err != nil {
		return nil, fmt.Errorf("invalid MDS3 payload JSON: %w", err)
	}

	return &payload, nil
}

// sanitizeMetadataJSON clamps IEEE-754 float64-rounded uint64 overflows in FIDO conformance metadata
// (such as vendorPrototypeConfigCommands: [18446744073709552000]) so Go's json.Unmarshal into uint succeeds.
func sanitizeMetadataJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	sanitizeStatementMap(root)
	if entries, ok := root["entries"].([]any); ok {
		for _, e := range entries {
			if em, ok := e.(map[string]any); ok {
				if ms, ok := em["metadataStatement"].(map[string]any); ok {
					sanitizeStatementMap(ms)
				}
			}
		}
	}
	return json.Marshal(root)
}

func sanitizeStatementMap(stmt map[string]any) {
	agInfo, ok := stmt["authenticatorGetInfo"].(map[string]any)
	if !ok {
		return
	}
	cmds, ok := agInfo["vendorPrototypeConfigCommands"].([]any)
	if !ok {
		return
	}
	for i, v := range cmds {
		if num, ok := v.(json.Number); ok {
			if _, err := strconv.ParseUint(num.String(), 10, 64); err != nil {
				cmds[i] = uint64(math.MaxUint)
			}
		}
	}
}

func (p *ConformanceMDSProvider) checkCertCRL(cert, issuer *x509.Certificate) error {
	for _, dp := range cert.CRLDistributionPoints {
		resp, err := p.httpClient.Get(dp)
		if err != nil {
			return fmt.Errorf("failed to fetch CRL %q: %w", dp, err)
		}
		crlBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("failed to read CRL %q: %w", dp, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("CRL %q returned HTTP %d", dp, resp.StatusCode)
		}

		crl, err := parseCRL(crlBytes)
		if err != nil {
			return fmt.Errorf("failed to parse CRL %q: %w", dp, err)
		}
		if err := crl.CheckSignatureFrom(issuer); err != nil {
			return fmt.Errorf("CRL signature check failed for %q: %w", dp, err)
		}
		for _, rev := range crl.RevokedCertificateEntries {
			if rev.SerialNumber.Cmp(cert.SerialNumber) == 0 {
				return fmt.Errorf("certificate serial %s is revoked per CRL %q", cert.SerialNumber.String(), dp)
			}
		}
	}
	return nil
}

func parseCRL(raw []byte) (*x509.RevocationList, error) {
	trimmed := bytes.TrimSpace(raw)
	if block, _ := pem.Decode(trimmed); block != nil {
		return x509.ParseRevocationList(block.Bytes)
	}
	return x509.ParseRevocationList(trimmed)
}

func parseCertPEMOrDER(raw []byte) (*x509.Certificate, error) {
	certs, err := parseCertChainPEMOrDER(raw)
	if err != nil || len(certs) == 0 {
		return nil, fmt.Errorf("no valid X.509 certificate found: %w", err)
	}
	return certs[0], nil
}

func parseCertChainPEMOrDER(raw []byte) ([]*x509.Certificate, error) {
	trimmed := bytes.TrimSpace(raw)
	var certs []*x509.Certificate
	rest := trimmed
	for len(rest) > 0 {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	if len(certs) > 0 {
		return certs, nil
	}

	if decoded, err := base64.StdEncoding.DecodeString(string(trimmed)); err == nil {
		if c, err := x509.ParseCertificate(decoded); err == nil {
			return []*x509.Certificate{c}, nil
		}
	}
	return x509.ParseCertificates(trimmed)
}

func verifyJWSSignature(alg string, pubKey any, signingInput, sig []byte) error {
	var hash crypto.Hash
	var keySize int

	switch alg {
	case "ES256", "RS256":
		hash = crypto.SHA256
		keySize = 32
	case "ES384", "RS384":
		hash = crypto.SHA384
		keySize = 48
	case "ES512", "RS512":
		hash = crypto.SHA512
		keySize = 66
	default:
		return fmt.Errorf("unsupported JWS alg %q", alg)
	}

	var digest []byte
	switch hash {
	case crypto.SHA256:
		s := sha256.Sum256(signingInput)
		digest = s[:]
	case crypto.SHA384:
		s := sha512.Sum384(signingInput)
		digest = s[:]
	case crypto.SHA512:
		s := sha512.Sum512(signingInput)
		digest = s[:]
	}

	if strings.HasPrefix(alg, "ES") {
		ecKey, ok := pubKey.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("expected ECDSA public key for ES* JWS")
		}
		if len(sig) != 2*keySize {
			return fmt.Errorf("invalid ECDSA JWS signature length %d", len(sig))
		}
		r := new(big.Int).SetBytes(sig[:keySize])
		s := new(big.Int).SetBytes(sig[keySize:])
		if !ecdsa.Verify(ecKey, digest, r, s) {
			return errors.New("ECDSA JWS signature invalid")
		}
		return nil
	}

	rsaKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("expected RSA public key for RS* JWS")
	}
	return rsa.VerifyPKCS1v15(rsaKey, hash, digest, sig)
}

// ValidateZeroAAGUIDAttestationCert ensures that when an attestation carries a zero AAGUID and an x5c chain
// (such as FIDO U2F full attestation), the leaf certificate maps to a known metadata entry by key identifier.
func (p *ConformanceMDSProvider) ValidateZeroAAGUIDAttestationCert(ctx context.Context, aaguid []byte, attStmt map[string]any) error {
	if len(aaguid) != 0 {
		parsedUUID, err := uuid.FromBytes(aaguid)
		if err == nil && parsedUUID != uuid.Nil {
			return nil
		}
	}
	if len(attStmt) == 0 {
		return nil
	}
	rawX5C, ok := attStmt["x5c"]
	if !ok || rawX5C == nil {
		return nil
	}
	x5cSlice, ok := rawX5C.([]any)
	if !ok || len(x5cSlice) == 0 {
		return nil
	}
	leafBytes, ok := x5cSlice[0].([]byte)
	if !ok || len(leafBytes) == 0 {
		return errors.New("invalid x5c leaf certificate in attestation statement")
	}
	cert, err := x509.ParseCertificate(leafBytes)
	if err != nil {
		return fmt.Errorf("failed to parse attestation leaf certificate: %w", err)
	}

	for _, kid := range extractCertKeyIdentifiers(cert) {
		entry, err := p.GetEntryByKeyIdentifier(ctx, kid)
		if err != nil {
			return err
		}
		if entry != nil {
			return nil
		}
	}
	return errors.New("untrusted attestation certificate: no matching key identifier in FIDO metadata")
}

func extractCertKeyIdentifiers(cert *x509.Certificate) []string {
	var ids []string
	if len(cert.SubjectKeyId) != 0 {
		ids = append(ids, hex.EncodeToString(cert.SubjectKeyId))
	}
	var spki struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}
	if _, err := asn1.Unmarshal(cert.RawSubjectPublicKeyInfo, &spki); err == nil {
		sum := sha1.Sum(spki.PublicKey.Bytes)
		kid := hex.EncodeToString(sum[:])
		if !slices.Contains(ids, kid) {
			ids = append(ids, kid)
		}
	}
	return ids
}

// Provider & ExtendedProvider delegations (thread-safe with automatic nextUpdate refresh)

func (p *ConformanceMDSProvider) GetEntry(ctx context.Context, aaguid uuid.UUID) (*metadata.Entry, error) {
	p.refreshExpiredIfNeeded()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetEntry(ctx, aaguid)
}

func (p *ConformanceMDSProvider) GetEntryByKeyIdentifier(ctx context.Context, keyIdentifier string) (*metadata.Entry, error) {
	p.refreshExpiredIfNeeded()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetEntryByKeyIdentifier(ctx, strings.ToLower(keyIdentifier))
}

func (p *ConformanceMDSProvider) GetValidateEntry(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateEntry(ctx)
}

func (p *ConformanceMDSProvider) GetValidateEntryPermitZeroAAGUID(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateEntryPermitZeroAAGUID(ctx)
}

func (p *ConformanceMDSProvider) GetValidateTrustAnchor(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateTrustAnchor(ctx)
}

func (p *ConformanceMDSProvider) GetValidateStatus(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateStatus(ctx)
}

func (p *ConformanceMDSProvider) GetValidateAttestationTypes(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateAttestationTypes(ctx)
}

func (p *ConformanceMDSProvider) ValidateStatusReports(ctx context.Context, reports []metadata.StatusReport) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.ValidateStatusReports(ctx, reports)
}

func (p *ConformanceMDSProvider) GetValidateEntryKeyIdentifier(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateEntryKeyIdentifier(ctx)
}

func (p *ConformanceMDSProvider) GetValidateStatusCertificateScope(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateStatusCertificateScope(ctx)
}

func (p *ConformanceMDSProvider) GetValidateAAGUID(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateAAGUID(ctx)
}

func (p *ConformanceMDSProvider) GetValidateAttestationFormats(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateAttestationFormats(ctx)
}

func (p *ConformanceMDSProvider) GetValidateAlgorithms(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateAlgorithms(ctx)
}

func (p *ConformanceMDSProvider) GetValidateBackupEligibility(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateBackupEligibility(ctx)
}

func (p *ConformanceMDSProvider) GetValidateExtensions(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateExtensions(ctx)
}

func (p *ConformanceMDSProvider) GetValidateUserVerification(ctx context.Context) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.GetValidateUserVerification(ctx)
}
