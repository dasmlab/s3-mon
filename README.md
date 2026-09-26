# s3-mon

Kubernetes operator that scrapes S3-compatible endpoints and exports Prometheus metrics for Grafana.

## CRD: `S3Endpoint`

```yaml
apiVersion: s3mon.dasmlab.org/v1alpha1
kind: S3Endpoint
metadata:
  name: lab-minio
  namespace: monitoring
spec:
  credentialsSecretRef:
    name: lab-minio-creds
  interval: 5m
  folderDepth: 3          # prefix ("folder") walk depth
  insecure: true          # HTTP (MinIO)
  forcePathStyle: true
```

### Secret format (preferred — Thanos-style, all in one place)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: lab-minio-creds
stringData:
  endpoint: minio.example:9000
  access_key: ...
  secret_key: ...
  region: us-east-1
  insecure: "true"
  forcePathStyle: "true"
```

Also accepted: `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`, and a Thanos objstore YAML blob under key `config`.

## Metrics (Gin `:8090/metrics`)

| Metric | Labels |
|--------|--------|
| `s3mon_bucket_size_bytes` | endpoint_namespace, endpoint, bucket |
| `s3mon_bucket_objects` | endpoint_namespace, endpoint, bucket |
| `s3mon_folder_count` | endpoint_namespace, endpoint, bucket, depth |
| `s3mon_prefix_size_bytes` | endpoint_namespace, endpoint, bucket, prefix, depth |
| `s3mon_prefix_objects` | endpoint_namespace, endpoint, bucket, prefix, depth |
| `s3mon_scrape_success` | endpoint_namespace, endpoint |
| `s3mon_scrape_duration_seconds` | endpoint_namespace, endpoint |
| `s3mon_last_scrape_timestamp` | endpoint_namespace, endpoint |

## Build / CRDs in CI

The Dockerfile **generates CRDs during the image build** (`controller-gen`) and copies them to `/crds` in the runtime image. GitHub Actions also runs `make manifests` and uploads `dist/crds` as an artifact so GitOps does not depend on a developer laptop.

Locally:

```bash
make generate manifests
make docker-build IMG=s3-mon:dev
./scripts/kind-demo.sh
```

## SemVer

```bash
./commitme.sh point "short why message"
```

## Layout

Kubebuilder v4 (`go.kubebuilder.io/v4`) — domain `dasmlab.org`, group `s3mon`, kind `S3Endpoint`.
