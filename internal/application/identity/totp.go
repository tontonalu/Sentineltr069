package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	domain "github.com/celinet/sentinel-acs/internal/domain/identity"
	"github.com/celinet/sentinel-acs/internal/platform/crypto"
)

// TOTPService cuida do enrollment e verificação de 2FA.
//
// O secret cleartext só existe transitoriamente — durante o enroll,
// dentro do response HTML para o usuário ver o QR code. Após Confirm,
// é cifrado com o SecretBox e persistido em users.totp_secret.
type TOTPService struct {
	users  domain.UserRepository
	box    *crypto.SecretBox
	issuer string
}

func NewTOTPService(users domain.UserRepository, box *crypto.SecretBox, issuer string) *TOTPService {
	return &TOTPService{users: users, box: box, issuer: issuer}
}

// EnrollResult traz o que o handler precisa para mostrar a página de enrollment.
//
// Secret e OTPAuthURL são exibidos UMA VEZ — após Confirm bem sucedido,
// só o cifrado fica no banco e nem mesmo o admin consegue recuperar.
type EnrollResult struct {
	Secret     string // base32, exibido pra fallback manual
	OTPAuthURL string // otpauth://totp/...
	QRPng      []byte // PNG renderizado, embutível como data URL
}

// Enroll gera um novo secret e devolve materiais para a página de enrollment.
// NÃO persiste — só Confirm marca o user como TOTP-enabled.
func (t *TOTPService) Enroll(ctx context.Context, userID uuid.UUID) (*EnrollResult, error) {
	user, err := t.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      t.issuer,
		AccountName: user.Email,
		Period:      30,
		SecretSize:  32,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // SHA1 é o que Google Authenticator/Authy/etc esperam
	})
	if err != nil {
		return nil, fmt.Errorf("totp generate: %w", err)
	}

	img, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("totp qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("totp png: %w", err)
	}

	return &EnrollResult{
		Secret:     key.Secret(),
		OTPAuthURL: key.URL(),
		QRPng:      buf.Bytes(),
	}, nil
}

// Confirm valida o primeiro código contra o secret recém-gerado e persiste
// o secret cifrado. A partir daqui, login do user passa a exigir TOTP.
func (t *TOTPService) Confirm(ctx context.Context, userID uuid.UUID, secret, code string) error {
	if !totp.Validate(code, secret) {
		return domain.ErrTOTPInvalid
	}
	encrypted, err := t.box.EncryptString(secret)
	if err != nil {
		return fmt.Errorf("totp encrypt: %w", err)
	}
	return t.users.UpdateTOTP(ctx, userID, encrypted, true)
}

// Verify decifra o secret persistido e valida o código fornecido.
// Usado no fluxo de login (após senha) e em confirmações sensíveis.
func (t *TOTPService) Verify(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := t.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.TOTPEnabled || user.TOTPSecret == "" {
		return errors.New("totp: 2FA não habilitado para este usuário")
	}
	secret, err := t.box.DecryptString(user.TOTPSecret)
	if err != nil {
		return fmt.Errorf("totp decrypt: %w", err)
	}
	if !totp.Validate(code, secret) {
		return domain.ErrTOTPInvalid
	}
	return nil
}

// Disable remove o segundo fator. Action sensível — caller precisa garantir
// autenticação forte (re-validação de senha + TOTP atual).
func (t *TOTPService) Disable(ctx context.Context, userID uuid.UUID) error {
	return t.users.UpdateTOTP(ctx, userID, "", false)
}
