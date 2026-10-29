package config

import (
	"log"
	"os"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
)

var (
	WebAuthn            *webauthn.WebAuthn
	ConformanceWebAuthn *webauthn.WebAuthn
	ConformanceMDS      *ConformanceMDSProvider
)

func resolveRPID() string {
	rpID := strings.TrimSpace(os.Getenv("RP_ID"))
	if rpID == "" {
		rpID = "localhost" // Default for local development
		log.Println("RP_ID not set, defaulting to 'localhost'")
	}
	return rpID
}

func InitWebAuthn() {
	var err error
	rpID := resolveRPID()

	// Create WebAuthn configuration
	wconfig := &webauthn.Config{
		RPDisplayName: "Appsensi Passkeys", // Display Name for your site
		RPID:          rpID,                // Generally the FQDN for your site
		RPOrigins:     GetAllowedOrigins(), // The origin URLs allowed for WebAuthn requests
		// RPIcon: "https://your-site.com/logo.png", // Optional
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			AuthenticatorAttachment: protocol.CrossPlatform,
			RequireResidentKey:      protocol.ResidentKeyRequired(),
			ResidentKey:             protocol.ResidentKeyRequirementRequired,
			UserVerification:        protocol.VerificationPreferred,
		},
	}

	// Initialize WebAuthn
	WebAuthn, err = webauthn.New(wconfig)
	if err != nil {
		log.Fatalf("Failed to initialize WebAuthn: %v", err)
	}

	log.Println("WebAuthn configured successfully.")
}

// ConformanceCredentialParameters returns all COSE algorithms exercised by the FIDO2 Conformance Test Suite,
// adding ES256K (-47) and RS1 (-65535) to go-webauthn's default list.
func ConformanceCredentialParameters() []protocol.CredentialParameter {
	params := webauthn.CredentialParametersDefault()
	params = append(params,
		protocol.CredentialParameter{
			Type:      protocol.PublicKeyCredentialType,
			Algorithm: webauthncose.AlgES256K,
		},
		protocol.CredentialParameter{
			Type:      protocol.PublicKeyCredentialType,
			Algorithm: webauthncose.AlgRS1,
		},
	)
	return params
}

// InitConformanceWebAuthn initializes the dedicated WebAuthn instance and MDS provider for FIDO Conformance testing.
func InitConformanceWebAuthn() {
	mdsDir := strings.TrimSpace(os.Getenv("FIDO_MDS_DIR"))
	if mdsDir == "" {
		mdsDir = defaultLocalMDSDir
	}

	mds3Flag := strings.TrimSpace(os.Getenv("FIDO_MDS3_ENABLED"))
	mds3Enabled := strings.EqualFold(mds3Flag, "true") || mds3Flag == "1"

	mdsProvider, err := NewConformanceMDSProvider(mdsDir, MDS3Config{
		Enabled:        mds3Enabled,
		EndpointsURL:   os.Getenv("FIDO_MDS3_ENDPOINTS_URL"),
		ConformanceURL: GetConformanceEndpoint(),
		RootCertURL:    os.Getenv("FIDO_MDS3_ROOT_CERT_URL"),
		RootCertPath:   os.Getenv("FIDO_MDS3_ROOT_CERT_PATH"),
	})
	if err != nil {
		log.Fatalf("Failed to initialize FIDO Conformance MDS provider: %v", err)
	}
	ConformanceMDS = mdsProvider

	rpID := resolveRPID()
	wconfig := &webauthn.Config{
		RPDisplayName:         "Appsensi Passkeys",
		RPID:                  rpID,
		RPOrigins:             GetAllowedOrigins(),
		AttestationPreference: protocol.PreferNoAttestation,
		MDS:                   ConformanceMDS,
		Attestation: protocol.AttestationPolicy{
			AndroidKey: protocol.AndroidKeyPolicy{
				// Required to accept FIDO Conformance's "FAKE Android Keystore Software Attestation Root".
				AuthorizationScope: protocol.AndroidKeyAuthorizationScopeUnion,
			},
		},
	}

	ConformanceWebAuthn, err = webauthn.New(wconfig)
	if err != nil {
		log.Fatalf("Failed to initialize Conformance WebAuthn: %v", err)
	}

	log.Printf("FIDO Conformance WebAuthn initialized (RPID=%q, Origins=%v, MDSDir=%q, MDS3=%v).",
		rpID, wconfig.RPOrigins, mdsDir, mds3Enabled)
}
