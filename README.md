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

## UI + CRUD API

Served on `--http-bind-address` (`:8090`) by default. With `--ui-bind-address=127.0.0.1:8091`
the UI/API move to their own loopback listener (for an auth proxy) and `:8090` serves only
`/metrics` and `/healthz`. The UI has no auth of its own and acts with the operator's
cluster-wide permissions, so never expose it without a proxy.

- HTML UI: `GET /` — table of S3Endpoints with create/edit/delete
- JSON API:
  - `GET /api/v1/s3endpoints[?namespace=]`
  - `GET /api/v1/s3endpoints/:namespace/:name`
  - `POST /api/v1/s3endpoints`
  - `PUT /api/v1/s3endpoints/:namespace/:name`
  - `DELETE /api/v1/s3endpoints/:namespace/:name`

Handler tests live in `internal/httpserver`.

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

## Deploying on the ACM hub (ConfigurationPolicy)

| File | What |
|------|------|
| `deploy/acm/policy-hub-s3-mon.yaml` | `Policy hub-s3-mon` (generated — edit the `.tmpl.yaml`) |
| `deploy/acm/placement-hub-s3-mon.yaml` | `Placement` + `PlacementBinding` → `local-cluster` |

The policy has three ConfigurationPolicies:

1. `hub-s3-mon-crd` — the CRD (`pruneObjectBehavior: None`, so removing the policy never deletes your S3Endpoints).
2. `hub-s3-mon-config` — waits for (1), then Namespace `s3-mon`, SA, RBAC, Deployment
   (manager + `oauth-proxy`), Service, reencrypt Route, ServiceMonitor, and the MCO
   `observability-metrics-custom-allowlist`.
3. `hub-s3-mon-dashboard` — the Grafana dashboard ConfigMap in `open-cluster-management-observability`.

Hub prerequisites:

```bash
# User Workload Monitoring must be on (the ServiceMonitor lives in s3-mon)
oc -n openshift-monitoring get cm cluster-monitoring-config -o yaml | grep enableUserWorkload
# MCO addon must be running on local-cluster
oc -n open-cluster-management-addon-observability get pods
```

Apply (or add both files to the hub GitOps folder):

```bash
oc apply -f deploy/acm/policy-hub-s3-mon.yaml -f deploy/acm/placement-hub-s3-mon.yaml
oc -n open-cluster-management get policy hub-s3-mon
oc -n s3-mon get pods,route
```

UI: open the `s3-mon` Route and log in with OpenShift. Access requires permission to create
S3Endpoints in all namespaces (`--openshift-sar`).

S3 credentials Secrets and `S3Endpoint` CRs stay out of the policy (oneshot / ESO), same as Thanos.
`./commitme.sh` re-renders the policy so it always pins the image to the tag being cut.

## Grafana (Red Hat ACM Observability / MCO)

Metrics path: operator `/metrics` → UWM Prometheus (ServiceMonitor, `honorLabels: true`) →
MCO metrics-collector (only allowlisted `s3mon_*` names) → hub Thanos → MCO Grafana (`Observatorium`
datasource, adds the `cluster` label).

The dashboard (`deploy/grafana/s3-mon-dashboard.json`, uid `s3-mon-overview`) is delivered by
ConfigurationPolicy (3) above. The MCO `grafana-dashboard-loader` sidecar imports any ConfigMap in
`open-cluster-management-observability` labelled `grafana-custom-dashboard: "true"`; it lands in the
**S3** folder (annotation `observability.open-cluster-management.io/dashboard-folder`).

Without the policy:

```bash
oc apply -f deploy/grafana/s3-mon-dashboard-configmap.yaml
```

Check it:

```bash
oc -n open-cluster-management-observability get cm s3-mon-dashboard --show-labels
oc -n open-cluster-management-observability logs deploy/observability-grafana -c grafana-dashboard-loader --tail=20
```

Then ACM console → Infrastructure → Clusters → Grafana link (or the `grafana` Route in
`open-cluster-management-observability`) → Dashboards → S3 → **S3 Monitor - Buckets & Folders**.

MCO Grafana is read-only for provisioned dashboards. To change the dashboard, edit the JSON (or edit a copy
in a Grafana dev instance and export it), then run `./hack/render-grafana-configmap.sh` and
`./hack/render-acm-policy.sh`. Expect data up to one collector interval (default 5m) behind the operator.

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
