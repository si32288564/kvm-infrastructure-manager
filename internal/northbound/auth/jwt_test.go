package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWKSVerifierHumanAutomationAndFailures(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	jwks := testJWKS(t, &key.PublicKey)
	verifier := &JWKSVerifier{Issuer: "https://issuer.example", Audience: "kim-api", LoadJWKS: func() ([]byte, error) { return jwks, nil }, Now: func() time.Time { return now }}

	for _, principalType := range []string{"HUMAN", "AUTOMATION"} {
		request := httptest.NewRequest("GET", "/api/v1/projects", nil)
		request.Header.Set("Authorization", "Bearer "+testToken(t, key, map[string]any{"iss": verifier.Issuer, "sub": "principal-" + principalType, "aud": []string{"other", verifier.Audience}, "exp": now.Add(time.Minute).Unix(), "principal_type": principalType}))
		principal, err := verifier.Authenticate(request)
		if err != nil || principal.Type != principalType {
			t.Fatalf("%s principal=%+v err=%v", principalType, principal, err)
		}
	}

	tests := []struct {
		name, authorization string
	}{
		{name: "missing"},
		{name: "malformed", authorization: "Bearer not-a-jwt"},
		{name: "expired", authorization: "Bearer " + testToken(t, key, map[string]any{"iss": verifier.Issuer, "sub": "expired", "aud": verifier.Audience, "exp": now.Add(-time.Second).Unix(), "principal_type": "HUMAN"})},
		{name: "wrong audience", authorization: "Bearer " + testToken(t, key, map[string]any{"iss": verifier.Issuer, "sub": "wrong", "aud": "other", "exp": now.Add(time.Minute).Unix(), "principal_type": "HUMAN"})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/projects", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if _, err := verifier.Authenticate(request); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}

func testJWKS(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()
	exponent := big.NewInt(int64(key.E)).Bytes()
	raw, err := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "test", "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(exponent)}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test", "typ": "at+jwt"})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}
