package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 stores pictures in an S3-compatible bucket with path-style addressing (<endpoint>/<bucket>/<key>), which Cloudflare R2, Google
// Cloud Storage (HMAC keys, https://storage.googleapis.com) and MinIO all answer. Requests are signed with AWS Signature V4 using
// only the standard library, so no SDK is pulled in for two calls.
type S3 struct {
	Endpoint        string // no trailing slash
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	PublicBaseURL   string // no trailing slash
	Client          *http.Client
	Now             func() time.Time
}

func (s *S3) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (s *S3) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *S3) URL(key string) string { return s.PublicBaseURL + "/" + key }

func (s *S3) Put(ctx context.Context, key, contentType, cacheControl string, body []byte) error {
	if !validKey(key) {
		return fmt.Errorf("media: bad key %q", key)
	}
	h := http.Header{}
	h.Set("Content-Type", contentType)
	if cacheControl != "" {
		h.Set("Cache-Control", cacheControl)
	}
	return s.do(ctx, http.MethodPut, key, h, body)
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if !validKey(key) {
		return fmt.Errorf("media: bad key %q", key)
	}
	return s.do(ctx, http.MethodDelete, key, http.Header{}, nil)
}

func (s *S3) do(ctx context.Context, method, key string, header http.Header, body []byte) error {
	target := s.Endpoint + "/" + s.Bucket + "/" + key
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	signV4(req, s.AccessKeyID, s.SecretAccessKey, s.Region, "s3", payloadHash, s.now())

	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("media: %s %s: %w", method, key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 || (method == http.MethodDelete && resp.StatusCode == http.StatusNotFound) {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("media: %s %s: storage answered %d: %s", method, key, resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// signV4 signs req in place (AWS Signature Version 4, header form): it sets Host-independent x-amz-date, x-amz-content-sha256 and
// Authorization. Every header already on the request is signed, so a header set before the call cannot be changed after it.
func signV4(req *http.Request, accessKey, secretKey, region, service, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	host := req.URL.Host
	names := []string{"host"}
	values := map[string]string{"host": host}
	for k, v := range req.Header {
		name := strings.ToLower(k)
		if name == "authorization" {
			continue
		}
		names = append(names, name)
		values[name] = strings.TrimSpace(strings.Join(v, ","))
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, n := range names {
		canonicalHeaders.WriteString(n + ":" + values[n] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	crSum := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(crSum[:])}, "\n")

	key := hmacSHA256([]byte("AWS4"+secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", accessKey, scope, signedHeaders, signature))
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalURI encodes each path segment once (S3 does not double-encode).
func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = seg
		}
		segments[i] = awsEscape(decoded)
	}
	return strings.Join(segments, "/")
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape is URL encoding as AWS wants it: only unreserved characters stay.
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
