package storage

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

// S3 는 S3 호환 저장소(경로 방식, SigV4)다. 클라우드 프로필에서만 쓴다.
type S3 struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey string
	Client                                         *http.Client
	Now                                            func() time.Time
}

func (s *S3) Kind() string { return "s3" }

func hmacSHA(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// SignV4 는 요청에 AWS Signature V4 헤더를 붙인다. extra 헤더도 서명 대상이다.
func SignV4(req *http.Request, body []byte, region, access, secret string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	payloadHash := hexSHA(body)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	host := req.URL.Host
	req.Header.Set("Host", host)

	var names []string
	hdr := map[string]string{"host": host}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "range" || strings.HasPrefix(lk, "x-amz-") {
			hdr[lk] = strings.TrimSpace(req.Header.Get(k))
		}
	}
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + hdr[k] + "\n")
	}
	signed := strings.Join(names, ";")
	canon := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, canonHeaders.String(), signed, payloadHash}, "\n")
	scope := date + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA([]byte(canon))
	k := hmacSHA([]byte("AWS4"+secret), date)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", access, scope, signed, sig))
}

func (s *S3) objURL(key string) string {
	return strings.TrimRight(s.Endpoint, "/") + "/" + s.Bucket + "/" + (&url.URL{Path: key}).EscapedPath()
}

func (s *S3) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objURL(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	SignV4(req, data, s.Region, s.AccessKey, s.SecretKey, s.now())
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objURL(key), nil)
	if err != nil {
		return nil, err
	}
	SignV4(req, nil, s.Region, s.AccessKey, s.SecretKey, s.now())
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("s3 get %d", resp.StatusCode)
	}
	return resp.Body, nil
}
