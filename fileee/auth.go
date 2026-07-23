package fileee

import (
	"fmt"
	"time"

	"github.com/pquerna/otp/totp"
)

// currentTOTP generiert den aktuellen 6-stelligen TOTP-Code aus einem RFC-6238-Base32-Seed
// (Umbrella-Spec §4.2). Ein leerer Seed liefert einen leeren Code (Konten ohne 2FA) statt
// eines Fehlers — der Aufrufer (login-Handshake, Task 7) lässt two-factor-token dann weg.
func currentTOTP(seed string) (string, error) {
	if seed == "" {
		return "", nil
	}
	code, err := totp.GenerateCode(seed, time.Now())
	if err != nil {
		return "", fmt.Errorf("fileee: totp generate: %w", err)
	}
	return code, nil
}
