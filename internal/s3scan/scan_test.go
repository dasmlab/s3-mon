package s3scan

import (
	"context"
	"encoding/pem"
	"fmt"
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

func TestScanSkipsUnavailableBucket(t *testing.T) {
	const (
		gone = "ag-acm-alertmonitoring"
		keep = "keep"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch strings.Trim(r.URL.Path, "/") {
		case "":
			_, _ = w.Write([]byte(bucketsXML(gone, keep)))
		case gone:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(s3ErrorXML("InvalidBucketState", "The bucket's root directory is either missing or is not a directory.")))
		default:
			_, _ = w.Write([]byte(objectsXML(keep, 10)))
		}
	}))
	t.Cleanup(srv.Close)

	res := mustScan(t, srv, nil)
	if len(res.Buckets) != 1 || res.Buckets[0].Name != keep || res.Buckets[0].Bytes != 10 {
		t.Fatalf("buckets = %+v", res.Buckets)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Name != gone || !strings.Contains(res.Skipped[0].Reason, "InvalidBucketState") {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestScanSkipsNoSuchBucket(t *testing.T) {
	const (
		gone = "removed"
		keep = "keep"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if strings.Trim(r.URL.Path, "/") == gone {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")))
			return
		}
		_, _ = w.Write([]byte(objectsXML(keep, 4)))
	}))
	t.Cleanup(srv.Close)

	res := mustScan(t, srv, []string{gone, keep})
	if len(res.Buckets) != 1 || res.Buckets[0].Name != keep {
		t.Fatalf("buckets = %+v", res.Buckets)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Name != gone {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestScanAllBucketsUnavailable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(s3ErrorXML("InvalidBucketState", "The bucket's root directory is either missing or is not a directory.")))
	}))
	t.Cleanup(srv.Close)

	cli := mustClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cli.Scan(ctx, []string{"gone"}, 1)
	if err == nil || !strings.Contains(err.Error(), "all buckets unavailable") {
		t.Fatalf("want all buckets unavailable, got %v", err)
	}
}

func TestScanOtherBucketErrorsFail(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(s3ErrorXML("AccessDenied", "Access Denied")))
	}))
	t.Cleanup(srv.Close)

	cli := mustClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cli.Scan(ctx, []string{"secret"}, 1)
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("want AccessDenied, got %v", err)
	}
}

func mustClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cli, err := New(s3creds.Creds{
		Endpoint:           strings.TrimPrefix(srv.URL, "https://"),
		AccessKey:          "a",
		SecretKey:          "b",
		Region:             "us-east-1",
		ForcePathStyle:     true,
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cli
}

func mustScan(t *testing.T, srv *httptest.Server, allow []string) Result {
	t.Helper()
	cli := mustClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cli.Scan(ctx, allow, 1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return res
}

func bucketsXML(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Owner><ID>test</ID><DisplayName>test</DisplayName></Owner><Buckets>`)
	for _, n := range names {
		b.WriteString(`<Bucket><Name>` + n + `</Name><CreationDate>2026-01-01T00:00:00.000Z</CreationDate></Bucket>`)
	}
	b.WriteString(`</Buckets></ListAllMyBucketsResult>`)
	return b.String()
}

func objectsXML(bucket string, size int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>%s</Name>
<KeyCount>1</KeyCount>
<MaxKeys>1000</MaxKeys>
<IsTruncated>false</IsTruncated>
<Contents>
<Key>a.txt</Key>
<LastModified>2026-01-01T00:00:00.000Z</LastModified>
<ETag>"abc"</ETag>
<Size>%d</Size>
<StorageClass>STANDARD</StorageClass>
</Contents>
</ListBucketResult>`, bucket, size)
}

func s3ErrorXML(code, message string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>` + code + `</Code><Message>` + message + `</Message><RequestId>req</RequestId><HostId>host</HostId></Error>`
}
