// Package auth implements the Northbound bearer-token trust boundary. It does
// not accept Host Agent, backend, or database credentials.
package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/northbound/project"
)

var ErrInvalidToken = errors.New("invalid Northbound bearer token")

type Authenticator interface {
	Authenticate(*http.Request) (project.Principal, error)
}

type JWKSVerifier struct {
	Issuer, Audience string
	LoadJWKS         func() ([]byte, error)
	Now              func() time.Time
}

func NewFileJWKSVerifier(issuer, audience, path string) (*JWKSVerifier, error) {
	if issuer == "" || audience == "" || path == "" {
		return nil, errors.New("OIDC issuer, audience, and JWKS file are required")
	}
	return &JWKSVerifier{Issuer: issuer, Audience: audience, LoadJWKS: func() ([]byte, error) {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > 1<<20 {
			return nil, ErrInvalidToken
		}
		return raw, nil
	}}, nil
}

func (v *JWKSVerifier) Authenticate(request *http.Request) (project.Principal, error) {
	if v == nil || v.LoadJWKS == nil || v.Issuer == "" || v.Audience == "" {
		return project.Principal{}, ErrInvalidToken
	}
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || strings.Contains(strings.TrimPrefix(authorization, "Bearer "), " ") {
		return project.Principal{}, ErrInvalidToken
	}
	parts := strings.Split(strings.TrimPrefix(authorization, "Bearer "), ".")
	if len(parts) != 3 {
		return project.Principal{}, ErrInvalidToken
	}
	decode := base64.RawURLEncoding.DecodeString
	headerRaw, err := decode(parts[0])
	if err != nil {
		return project.Principal{}, ErrInvalidToken
	}
	claimsRaw, err := decode(parts[1])
	if err != nil {
		return project.Principal{}, ErrInvalidToken
	}
	signature, err := decode(parts[2])
	if err != nil {
		return project.Principal{}, ErrInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerRaw, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" || (header.Type != "JWT" && header.Type != "at+jwt") {
		return project.Principal{}, ErrInvalidToken
	}
	key, err := v.key(header.KeyID)
	if err != nil {
		return project.Principal{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return project.Principal{}, ErrInvalidToken
	}
	var claims struct {
		Issuer        string          `json:"iss"`
		Subject       string          `json:"sub"`
		Audience      json.RawMessage `json:"aud"`
		Expires       int64           `json:"exp"`
		NotBefore     int64           `json:"nbf"`
		PrincipalType string          `json:"principal_type"`
	}
	if json.Unmarshal(claimsRaw, &claims) != nil || claims.Issuer != v.Issuer || claims.Subject == "" || !audienceContains(claims.Audience, v.Audience) || (claims.PrincipalType != "HUMAN" && claims.PrincipalType != "AUTOMATION") {
		return project.Principal{}, ErrInvalidToken
	}
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	if claims.Expires <= now.Unix() || (claims.NotBefore != 0 && claims.NotBefore > now.Unix()) {
		return project.Principal{}, ErrInvalidToken
	}
	return project.Principal{Issuer: claims.Issuer, Subject: claims.Subject, Type: claims.PrincipalType}, nil
}

func (v *JWKSVerifier) key(kid string) (*rsa.PublicKey, error) {
	raw, err := v.LoadJWKS()
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			Type      string `json:"kty"`
			Use       string `json:"use"`
			Algorithm string `json:"alg"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(raw, &set) != nil {
		return nil, ErrInvalidToken
	}
	for _, candidate := range set.Keys {
		if candidate.KeyID != kid || candidate.Type != "RSA" || candidate.Use != "sig" || candidate.Algorithm != "RS256" {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(candidate.N)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(candidate.E)
		if nErr != nil || eErr != nil || len(nBytes) < 256 || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, ErrInvalidToken
		}
		exponent := 0
		for _, value := range eBytes {
			exponent = exponent<<8 | int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			return nil, ErrInvalidToken
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}, nil
	}
	return nil, fmt.Errorf("%w: signing key not found", ErrInvalidToken)
}

func audienceContains(raw json.RawMessage, expected string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == expected
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, value := range many {
		if value == expected {
			return true
		}
	}
	return false
}
