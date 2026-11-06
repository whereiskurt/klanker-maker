package httpproxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"net/http"
	"testing"
)

func TestDecodeForMetering(t *testing.T) {
	const plain = `{"usage":{"input_tokens":1,"output_tokens":2}}`
	var gz, zl, raw bytes.Buffer
	gw := gzip.NewWriter(&gz)
	_, _ = gw.Write([]byte(plain))
	_ = gw.Close()
	zw := zlib.NewWriter(&zl)
	_, _ = zw.Write([]byte(plain))
	_ = zw.Close()
	fw, _ := flate.NewWriter(&raw, flate.DefaultCompression)
	_, _ = fw.Write([]byte(plain))
	_ = fw.Close()

	for name, tc := range map[string]struct {
		body []byte
		ce   string
	}{
		"identity":    {[]byte(plain), ""},
		"gzip":        {gz.Bytes(), "gzip"},
		"x-gzip":      {gz.Bytes(), "X-Gzip"},
		"zlib":        {zl.Bytes(), "deflate"},
		"raw deflate": {raw.Bytes(), "deflate"},
	} {
		got, err := decodeForMetering(tc.body, tc.ce)
		if err != nil || string(got) != plain {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
	if _, err := decodeForMetering([]byte("x"), "br"); !errors.Is(err, errUnknownEncoding) {
		t.Errorf("br: err = %v, want errUnknownEncoding", err)
	}
	if _, err := decodeForMetering([]byte("not gzip"), "gzip"); err == nil || errors.Is(err, errUnknownEncoding) {
		t.Errorf("corrupt gzip: err = %v, want a decode error", err)
	}
}

func TestSigV4SignsAcceptEncoding(t *testing.T) {
	for authz, want := range map[string]bool{
		"":                                       false,
		"Bearer x SignedHeaders=accept-encoding": false,
		"AWS4-HMAC-SHA256 Credential=a/b, SignedHeaders=accept-encoding;host, Signature=s":   true,
		"AWS4-HMAC-SHA256 Credential=a/b, SignedHeaders=host;Accept-Encoding, Signature=s":   true,
		"AWS4-HMAC-SHA256 Credential=a/b, SignedHeaders=host;x-amz-date, Signature=s":        false,
		"AWS4-HMAC-SHA256 Credential=a/b, SignedHeaders=host;x-accept-encoding, Signature=s": false,
		"AWS4-HMAC-SHA256 Credential=a/b, Signature=s accept-encoding":                       false,
	} {
		r, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
		if authz != "" {
			r.Header.Set("Authorization", authz)
		}
		if got := sigV4SignsAcceptEncoding(r); got != want {
			t.Errorf("%q: got %v, want %v", authz, got, want)
		}
	}
}
