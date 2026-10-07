package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/fasthttp/router"
	json "github.com/goccy/go-json"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/openim"
)

const openIMTokenAudience = "pingmessenger/openim-token"

func (a *API) registerOpenIMRoutes(r *router.Router) {
	r.POST("/v1/openim/device-key", a.registerOpenIMDeviceKey)
	r.POST("/v1/openim/token/challenge", a.openIMTokenChallenge)
	r.POST("/v1/openim/token/issue", a.issueOpenIMToken)
}

type publicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}
type deviceKeyRequest struct {
	PublicKey publicJWK `json:"public_key" validate:"required"`
}
type deviceProofRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Nonce       string `json:"nonce" validate:"required,len=43"`
	Signature   string `json:"signature" validate:"required,max=1024"`
	Fingerprint string `json:"fingerprint" validate:"required,max=128"`
}

func parseP256Key(j publicJWK) (*ecdsa.PublicKey, string, string, error) {
	if j.Kty != "EC" || j.Crv != "P-256" {
		return nil, "", "", errInvalidKey
	}
	x, ex := base64.RawURLEncoding.DecodeString(j.X)
	y, ey := base64.RawURLEncoding.DecodeString(j.Y)
	if ex != nil || ey != nil || len(x) != 32 || len(y) != 32 {
		return nil, "", "", errInvalidKey
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, "", "", errInvalidKey
	}
	canonical, _ := json.Marshal(publicJWK{Kty: "EC", Crv: "P-256", X: j.X, Y: j.Y})
	raw := append(append([]byte{4}, x...), y...)
	return pub, auth.Fingerprint(string(raw)), string(canonical), nil
}

var errInvalidKey = &keyError{}

type keyError struct{}

func (*keyError) Error() string { return "invalid P-256 public key" }

func (a *API) registerOpenIMDeviceKey(ctx *fasthttp.RequestCtx) {
	userID, sessionID, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req deviceKeyRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	_, fingerprint, canonical, err := parseP256Key(req.PublicKey)
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "public_key must be a P-256 JWK"})
		return
	}
	if current, err := a.users.ActiveOpenIMDeviceToken(context.Background(), sessionID); err == nil {
		plain, err := a.deviceTokenCipher.Decrypt(current.Ciphertext)
		if err != nil {
			log.Printf("openim decrypt prior device token failed: %v", err)
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not invalidate previous OpenIM token"})
			return
		}
		if err := a.openIM.KickTokens(context.Background(), []string{plain}); err != nil {
			log.Printf("openim exact-token kick failed: %v", err)
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not invalidate previous OpenIM token"})
			return
		}
		_ = a.users.RevokeOpenIMDeviceToken(context.Background(), sessionID)
	}
	if err := a.users.UpsertDeviceKey(context.Background(), sessionID, canonical, fingerprint); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not store device public key"})
		return
	}
	_ = userID
	writeJSON(ctx, http.StatusOK, map[string]string{"fingerprint": fingerprint, "status": "registered"})
}

func (a *API) openIMTokenChallenge(ctx *fasthttp.RequestCtx) {
	_, sessionID, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if _, err := a.users.DeviceKey(context.Background(), sessionID); err != nil {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "register a device public key first"})
		return
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create challenge"})
		return
	}
	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	expiry := time.Now().Add(time.Minute)
	id, err := a.users.CreateDeviceProofChallenge(context.Background(), sessionID, auth.Fingerprint(nonceText), openIMTokenAudience, expiry)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not store challenge"})
		return
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{"challenge_id": id, "nonce": nonceText, "audience": openIMTokenAudience, "expires_at": expiry})
}

func (a *API) issueOpenIMToken(ctx *fasthttp.RequestCtx) {
	userID, sessionID, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req deviceProofRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	challenge, err := a.users.DeviceProofChallenge(context.Background(), req.ChallengeID)
	if err != nil || challenge.SessionID != sessionID || challenge.Consumed || time.Now().After(challenge.ExpiresAt) || challenge.Audience != openIMTokenAudience {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired device proof"})
		return
	}
	key, err := a.users.DeviceKey(context.Background(), sessionID)
	if err != nil || key.Fingerprint != req.Fingerprint {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid device proof"})
		return
	}
	var jwk publicJWK
	if json.Unmarshal([]byte(key.PublicJWK), &jwk) != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "stored device key is invalid"})
		return
	}
	pub, fingerprint, _, err := parseP256Key(jwk)
	if err != nil || fingerprint != req.Fingerprint {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid device proof"})
		return
	}
	if auth.Fingerprint(req.Nonce) != challenge.NonceHash || !verifyDeviceSignature(pub, "v1|"+challenge.ID+"|"+req.Nonce+"|"+sessionID+"|"+openIMTokenAudience, req.Signature) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid device proof"})
		return
	}
	// The compare-and-set consumption makes concurrent replay attempts fail
	// before a replacement OpenIM token can be minted.
	if err := a.users.ConsumeDeviceProofChallenge(context.Background(), challenge.ID, sessionID); err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired device proof"})
		return
	}
	platformName, err := a.users.SessionPlatform(context.Background(), userID, sessionID)
	if err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "inactive device session"})
		return
	}
	platformID, valid := openIMPlatform(platformName)
	if !valid {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported OpenIM device platform"})
		return
	}
	// OpenIM must receive the raw prior token to target revocation. It is held
	// encrypted at rest and never returned by a read endpoint.
	if current, err := a.users.ActiveOpenIMDeviceToken(context.Background(), sessionID); err == nil {
		plain, err := a.deviceTokenCipher.Decrypt(current.Ciphertext)
		if err != nil || a.openIM.KickTokens(context.Background(), []string{plain}) != nil {
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not invalidate previous OpenIM token"})
			return
		}
		if err := a.users.RevokeOpenIMDeviceToken(context.Background(), sessionID); err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not record token revocation"})
			return
		}
	}
	openIMUserID, err := a.openIMIdentity(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not prepare OpenIM account"})
		return
	}
	issued, err := a.openIM.GetUserToken(context.Background(), &openim.GetUserTokenRequest{UserID: openIMUserID, PlatformID: platformID})
	if err != nil || issued.Token == "" {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not issue OpenIM token"})
		return
	}
	ciphertext, err := a.deviceTokenCipher.Encrypt(issued.Token)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not protect OpenIM token"})
		return
	}
	expiresAt := time.Now().Add(time.Duration(issued.ExpireTimeSeconds) * time.Second)
	if err := a.users.ReplaceOpenIMDeviceToken(context.Background(), sessionID, auth.Fingerprint(issued.Token), ciphertext, int(platformID), expiresAt); err != nil {
		_ = a.openIM.KickTokens(context.Background(), []string{issued.Token})
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not record OpenIM token"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"token": issued.Token, "platform_id": platformID, "expires_at": expiresAt})
}

// openIMIdentity repairs an interrupted signup provision on demand. The
// derived ID is deterministic and belongs only to this platform user. A prior
// successful upstream register may have raced before our local assignment;
// in that case setting the local mapping is still safe and GetUserToken below
// remains the authoritative existence check.
func (a *API) openIMIdentity(ctx context.Context, userID string) (string, error) {
	if id, err := a.users.OpenIMUserID(ctx, userID); err == nil && id != "" {
		return id, nil
	}
	username, err := a.users.Username(ctx, userID)
	if err != nil {
		return "", err
	}
	id := "pm" + strings.ReplaceAll(userID, "-", "")
	if _, err := a.openIM.ProvisionUser(ctx, &openim.ProvisionUserRequest{UserID: id, Nickname: username}); err != nil {
		// A duplicate response means the first signup attempt reached OpenIM but
		// failed before persisting this local mapping. The derived ID prevents a
		// cross-account collision, so it is safe to continue to the assignment.
		if !strings.Contains(strings.ToLower(err.Error()), "already") {
			return "", err
		}
	}
	if err := a.users.SetOpenIMUserID(ctx, userID, id); err != nil {
		if current, readErr := a.users.OpenIMUserID(ctx, userID); readErr == nil && current == id {
			return current, nil
		}
		return "", err
	}
	return id, nil
}

// verifyDeviceSignature accepts WebCrypto's 64-byte r||s output and Android's
// ASN.1 DER SHA256withECDSA output.
func verifyDeviceSignature(pub *ecdsa.PublicKey, payload, encoded string) bool {
	sig, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(payload))
	if len(sig) == 64 {
		return ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
	}
	var der struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(sig, &der)
	return err == nil && len(rest) == 0 && der.R != nil && der.S != nil && ecdsa.Verify(pub, digest[:], der.R, der.S)
}
func openIMPlatform(platform string) (int32, bool) {
	switch strings.ToLower(platform) {
	case "ios":
		return 1, true
	case "android":
		return 2, true
	case "windows":
		return 3, true
	case "macos", "mac":
		return 4, true
	case "web":
		return 5, true
	case "linux":
		return 7, true
	default:
		return 0, false
	}
}
