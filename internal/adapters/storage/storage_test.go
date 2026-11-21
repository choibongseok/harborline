package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiskPutGetRoundTrip(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put(context.Background(), "q1/a.txt", []byte("안녕")); err != nil {
		t.Fatal(err)
	}
	rc, err := d.Get(context.Background(), "q1/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "안녕" {
		t.Fatalf("%q", b)
	}
}

func TestDiskRejectsPathTraversal(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	for _, k := range []string{"../x", "a/../../x", ""} {
		if err := d.Put(context.Background(), k, []byte("x")); err == nil {
			t.Errorf("%q 는 거부해야 함", k)
		}
	}
}

// AWS Signature V4 문서의 GET Object 예시(Range 헤더 포함)와 같은 서명이 나와야 한다.
func TestS3SignatureMatchesPublishedExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	SignV4(req, nil, "us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", now)
	got := req.Header.Get("Authorization")
	want := "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("서명이 문서 예시와 다름:\n got %s\nwant ...%s", got, want)
	}
}

func TestS3PutGetAgainstFakeServer(t *testing.T) {
	var mu sync.Mutex
	objs := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") || r.Header.Get("x-amz-content-sha256") == "" {
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			b, _ := io.ReadAll(r.Body)
			objs[r.URL.Path] = b
		case "GET":
			b, ok := objs[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		}
	}))
	defer srv.Close()
	s := &S3{Endpoint: srv.URL, Bucket: "att", Region: "us-east-1", AccessKey: "AK", SecretKey: "SK", Client: srv.Client()}
	if err := s.Put(context.Background(), "q/한글 파일.txt", []byte("data")); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(context.Background(), "q/한글 파일.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "data" {
		t.Fatalf("%q", b)
	}
	if _, err := s.Get(context.Background(), "none"); err == nil {
		t.Fatal("없는 객체는 오류")
	}
}
