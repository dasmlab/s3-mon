package s3creds_test

import (
	"testing"

	"github.com/dasmlab/s3-mon/internal/s3creds"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFromSecretDiscrete(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"endpoint":       []byte("minio:9000"),
			"access_key":     []byte("ak"),
			"secret_key":     []byte("sk"),
			"insecure":       []byte("true"),
			"forcePathStyle": []byte("true"),
		},
	}
	c, err := s3creds.FromSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "minio:9000" || c.AccessKey != "ak" || !c.Insecure || !c.ForcePathStyle {
		t.Fatalf("unexpected creds: %+v", c)
	}
}

func TestFromSecretThanosConfig(t *testing.T) {
	raw := `
type: s3
config:
  endpoint: s3.example:443
  access_key: A
  secret_key: B
  region: us-west-2
  insecure: false
`
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data:       map[string][]byte{"config": []byte(raw)},
	}
	c, err := s3creds.FromSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "s3.example:443" || c.Region != "us-west-2" || c.AccessKey != "A" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestFromSecretTLSKeys(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"endpoint":             []byte("s3.example:443"),
			"access_key":           []byte("ak"),
			"secret_key":           []byte("sk"),
			"insecure_skip_verify": []byte("true"),
			"ca.crt":               []byte("-----BEGIN CERTIFICATE-----"),
		},
	}
	c, err := s3creds.FromSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	if c.Insecure || !c.InsecureSkipVerify || len(c.CABundle) == 0 {
		t.Fatalf("unexpected creds: %+v", c)
	}
}

func TestFromSecretThanosYAMLKeySkipVerify(t *testing.T) {
	raw := `
type: s3
config:
  endpoint: s3.example:443
  access_key: A
  secret_key: B
  http_config:
    tls_config:
      insecure_skip_verify: true
`
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data:       map[string][]byte{"thanos.yaml": []byte(raw)},
	}
	c, err := s3creds.FromSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "s3.example:443" || c.Insecure || !c.InsecureSkipVerify {
		t.Fatalf("unexpected: %+v", c)
	}
}
