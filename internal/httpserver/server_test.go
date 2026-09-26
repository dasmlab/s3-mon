package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	s3monv1alpha1 "github.com/dasmlab/s3-mon/api/v1alpha1"
	"github.com/dasmlab/s3-mon/internal/httpserver"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := s3monv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func seed(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(scheme(t)).WithObjects(objs...).Build()
}

func router(t *testing.T, c client.Client) http.Handler {
	t.Helper()
	s := &httpserver.Server{Client: c, Reg: prometheus.NewRegistry()}
	return s.NewRouter()
}

func TestHealthzAndMetrics(t *testing.T) {
	r := router(t, seed(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 200 {
		t.Fatalf("metrics: %d", w.Code)
	}
}

func TestUIListsEndpoints(t *testing.T) {
	ep := &s3monv1alpha1.S3Endpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "lab"},
		Spec: s3monv1alpha1.S3EndpointSpec{
			CredentialsSecretRef: s3monv1alpha1.SecretReference{Name: "creds"},
		},
		Status: s3monv1alpha1.S3EndpointStatus{Ready: true, ObservedBuckets: 2},
	}
	r := router(t, seed(t, ep))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 {
		t.Fatalf("ui: %d", w.Code)
	}
	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("demo")) || !bytes.Contains([]byte(body), []byte("lab")) {
		t.Fatalf("ui missing endpoint rows: %s", body)
	}
	if !bytes.Contains([]byte(body), []byte("/api/v1/s3endpoints")) {
		t.Fatalf("ui missing api wiring")
	}
}

func TestListCreateGetUpdateDelete(t *testing.T) {
	r := router(t, seed(t))

	// empty list
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/s3endpoints", nil))
	if w.Code != 200 {
		t.Fatalf("list empty: %d %s", w.Code, w.Body.String())
	}
	var listed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	items, _ := listed["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("want 0 items, got %v", items)
	}

	createBody := `{
		"name":"alpha","namespace":"demo",
		"spec":{"credentialsSecretRef":{"name":"s3"},"endpoint":"minio:9000","interval":"2m","folderDepth":2,"suspend":false}
	}`
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/s3endpoints", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/s3endpoints?namespace=demo", nil))
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	items, _ = listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %v", listed)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/s3endpoints/demo/alpha", nil))
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["secretName"] != "s3" || got["endpoint"] != "minio:9000" {
		t.Fatalf("unexpected get: %v", got)
	}

	updateBody := `{
		"name":"alpha","namespace":"demo",
		"spec":{"credentialsSecretRef":{"name":"s3-new"},"endpoint":"minio:9001","interval":"10m","folderDepth":3,"suspend":true}
	}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/s3endpoints/demo/alpha", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["secretName"] != "s3-new" || got["suspend"] != true {
		t.Fatalf("unexpected update: %v", got)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/s3endpoints/demo/alpha", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/s3endpoints/demo/alpha", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", w.Code)
	}
}

func TestCreateValidation(t *testing.T) {
	r := router(t, seed(t))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/s3endpoints", bytes.NewBufferString(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
}
