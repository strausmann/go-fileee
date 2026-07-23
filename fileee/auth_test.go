package fileee

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestCurrentTOTPLeererSeedLiefertLeerenCode(t *testing.T) {
	code, err := currentTOTP("")
	if err != nil {
		t.Fatalf("currentTOTP(\"\"): %v", err)
	}
	if code != "" {
		t.Fatalf("erwartet leeren Code bei leerem Seed, bekommen %q", code)
	}
}

func TestCurrentTOTPGueltigerSeedLiefertPasssendenCode(t *testing.T) {
	// Test-Seed, kein echter Fileee-Account-Seed (secret-safe).
	const testSeed = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	code, err := currentTOTP(testSeed)
	if err != nil {
		t.Fatalf("currentTOTP: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("Code hat Länge %d, erwartet 6", len(code))
	}
	want, err := totp.GenerateCode(testSeed, time.Now())
	if err != nil {
		t.Fatalf("Referenz-GenerateCode: %v", err)
	}
	if code != want {
		t.Fatalf("code = %q, Referenz-Code = %q", code, want)
	}
}

func TestCurrentTOTPUngueltigerSeedLiefertFehler(t *testing.T) {
	// "1" ist kein gültiges Base32: die Ziffer "1" gehört nicht zum RFC-4648-Base32-
	// Alphabet (A–Z, 2–7) und muss beim Dekodieren fehlschlagen statt still einen
	// Code zu erzeugen.
	code, err := currentTOTP("1")
	if err == nil {
		t.Fatalf("erwartet Fehler bei ungültigem Seed, bekommen Code %q", code)
	}
	if code != "" {
		t.Fatalf("erwartet leeren Code bei Fehler, bekommen %q", code)
	}
}
