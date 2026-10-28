package config

import (
	"log"
	"os"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

var WebAuthn *webauthn.WebAuthn

func InitWebAuthn() {
	var err error
	var rpID = os.Getenv("RP_ID")
	if rpID == "" {
		rpID = "localhost" // Default for local development
		log.Println("RP_ID not set, defaulting to 'localhost'")
	}
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
