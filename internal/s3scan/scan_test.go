package s3scan

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dasmlab/s3-mon/internal/s3creds"
)

const listBucketsXML = `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Owner><ID>test</ID></Owner><Buckets></Buckets></ListAllMyBucketsResult>`

func tlsS3(t *testing.T) (*httptest.Server, s3creds.Creds) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(listBucketsXML))
	}))
	t.Cleanup(srv.Close)
	return srv, s3creds.Creds{
		Endpoint:       strings.TrimPrefix(srv.URL, "https://"),
		AccessKey:      "a",
		SecretKey:      "b",
		Region:         "us-east-1",
		ForcePathStyle: true,
	}
}

func scan(t *testing.T, c s3creds.Creds) error {
	t.Helper()
	cli, err := New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = cli.Scan(ctx, nil, 1)
	return err
}

func TestSelfSignedRejectedByDefault(t *testing.T) {
	_, c := tlsS3(t)
	err := scan(t, c)
	if err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("want x509 error, got %v", err)
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	_, c := tlsS3(t)
	c.InsecureSkipVerify = true
	if err := scan(t, c); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func TestCustomCABundle(t *testing.T) {
	srv, c := tlsS3(t)
	c.CABundle = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := scan(t, c); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func TestInvalidCABundle(t *testing.T) {
	_, c := tlsS3(t)
	c.CABundle = []byte("not a cert")
	if _, err := New(c); err == nil {
		t.Fatal("want error for invalid CA bundle")
	}
}
