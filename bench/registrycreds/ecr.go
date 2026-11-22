package registrycreds

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ECRClient fetches ECR pull tokens with GetAuthorizationToken, signed
// with AWS Signature Version 4. Tokens are cached until shortly before they
// expire (AWS issues them for 12 hours). The zero value calls AWS.
type ECRClient struct {
	HTTP *http.Client
	// Endpoint returns the API URL of a region (tests); nil is
	// https://api.ecr.<region>.amazonaws.com.
	Endpoint func(region string) string
	Now      func() time.Time

	mu    sync.Mutex
	cache map[string]ecrToken
}

type ecrToken struct {
	auth    schemas.RegistryAuth
	expires time.Time
}

func (c *ECRClient) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Token returns a pull login for host from the access key.
func (c *ECRClient) Token(ctx context.Context, host, region, accessKey, secretKey string) (schemas.RegistryAuth, error) {
	sum := sha256.Sum256([]byte(host + "\x00" + accessKey + "\x00" + secretKey))
	ck := hex.EncodeToString(sum[:])
	c.mu.Lock()
	if t, ok := c.cache[ck]; ok && c.now().Before(t.expires.Add(-30*time.Minute)) {
		c.mu.Unlock()
		return t.auth, nil
	}
	c.mu.Unlock()
	auth, expires, err := c.fetch(ctx, host, region, accessKey, secretKey)
	if err != nil {
		return schemas.RegistryAuth{}, err
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]ecrToken{}
	}
	c.cache[ck] = ecrToken{auth: auth, expires: expires}
	c.mu.Unlock()
	return auth, nil
}

func (c *ECRClient) fetch(ctx context.Context, host, region, accessKey, secretKey string) (schemas.RegistryAuth, time.Time, error) {
	endpoint := "https://api.ecr." + region + ".amazonaws.com/"
	if strings.HasSuffix(host, ".amazonaws.com.cn") {
		endpoint = "https://api.ecr." + region + ".amazonaws.com.cn/"
	}
	if c.Endpoint != nil {
		endpoint = c.Endpoint(region)
	}
	body := []byte("{}")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return schemas.RegistryAuth{}, time.Time{}, fmt.Errorf("ECR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken")
	signV4(req, body, region, "ecr", accessKey, secretKey, c.now())
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return schemas.RegistryAuth{}, time.Time{}, errors.New("ECR did not answer: " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := strings.TrimSpace(e.Type + " " + e.Message)
		if msg == "" {
			msg = resp.Status
		}
		return schemas.RegistryAuth{}, time.Time{}, errors.New("ECR refused the access key: " + msg)
	}
	var out struct {
		AuthorizationData []struct {
			AuthorizationToken string  `json:"authorizationToken"`
			ExpiresAt          float64 `json:"expiresAt"`
		} `json:"authorizationData"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.AuthorizationData) == 0 {
		return schemas.RegistryAuth{}, time.Time{}, errors.New("ECR answered without a token")
	}
	d := out.AuthorizationData[0]
	dec, err := base64.StdEncoding.DecodeString(d.AuthorizationToken)
	if err != nil {
		return schemas.RegistryAuth{}, time.Time{}, errors.New("ECR token is not base64")
	}
	user, pass, ok := strings.Cut(string(dec), ":")
	if !ok || pass == "" {
		return schemas.RegistryAuth{}, time.Time{}, errors.New("ECR token is not user:password")
	}
	expires := c.now().Add(12 * time.Hour)
	if d.ExpiresAt > 0 {
		expires = time.Unix(int64(d.ExpiresAt), 0).UTC()
	}
	return schemas.RegistryAuth{Host: host, Username: user, Password: pass}, expires, nil
}

func hmacSHA256(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// signV4 signs req (Signature Version 4, headers content-type, host,
// x-amz-date and x-amz-target).
func signV4(req *http.Request, body []byte, region, service, accessKey, secretKey string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	host := req.URL.Host
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	signed := "content-type;host;x-amz-date;x-amz-target"
	canonHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + host + "\n" +
		"x-amz-date:" + amzDate + "\n" +
		"x-amz-target:" + req.Header.Get("X-Amz-Target") + "\n"
	canon := strings.Join([]string{req.Method, path, req.URL.RawQuery, canonHeaders, signed, sha256Hex(body)}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canon))
	k := hmacSHA256([]byte("AWS4"+secretKey), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}
