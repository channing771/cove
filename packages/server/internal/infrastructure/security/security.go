package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/boxify/api-go/internal/xerr"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type SecretCipher struct {
	aead cipher.AEAD
}

func NewSecretCipher(key string) (*SecretCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret cipher key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretCipher{aead: aead}, nil
}

func (c *SecretCipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *SecretCipher) Decrypt(encoded string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func MaskSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 4 {
		return strings.Repeat("*", len(secret))
	}
	return strings.Repeat("*", len(secret)-4) + secret[len(secret)-4:]
}

func HashPassword(password string) (string, error) {
	out, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(out), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type TokenIssuer struct {
	secret        []byte
	ttl           time.Duration
	allowDevToken bool
}

type Claims struct {
	UserID uuid.UUID
}

type jwtClaims struct {
	UserID string `json:"uid"`
	jwt.RegisteredClaims
}

// TokenIssuerOption 配置 TokenIssuer 的可选行为。
type TokenIssuerOption func(*TokenIssuer)

// WithDevToken 开启 "dev-token" 万能令牌，仅用于本地开发与测试。
//
// 默认关闭：生产装配不传该选项，因此 "dev-token" 不会绕过 JWT 校验。
func WithDevToken() TokenIssuerOption {
	return func(i *TokenIssuer) { i.allowDevToken = true }
}

func NewTokenIssuer(secret string, ttl time.Duration, opts ...TokenIssuerOption) *TokenIssuer {
	issuer := &TokenIssuer{secret: []byte(secret), ttl: ttl}
	for _, opt := range opts {
		if opt != nil {
			opt(issuer)
		}
	}
	return issuer
}

func (i *TokenIssuer) IssueAccessToken(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		UserID: userID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID.String(),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
}

func (i *TokenIssuer) Parse(tokenValue string) (Claims, error) {
	claims := jwtClaims{}
	token, err := jwt.ParseWithClaims(tokenValue, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", token.Method.Alg())
		}
		return i.secret, nil
	})
	if err != nil {
		return Claims{}, err
	}
	if !token.Valid {
		return Claims{}, errors.New("invalid token")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return Claims{}, err
	}
	return Claims{UserID: userID}, nil
}

func (i *TokenIssuer) VerifyAccessToken(ctx context.Context, token string) (uuid.UUID, error) {
	// dev-token 是仅用于本地开发/测试的万能令牌，默认关闭，避免成为生产后门。
	if i.allowDevToken && token == "dev-token" {
		return uuid.MustParse("00000000-0000-0000-0000-000000000001"), nil
	}
	claims, err := i.Parse(token)
	if err != nil {
		return uuid.Nil, xerr.InvalidToken()
	}
	return claims.UserID, nil
}
