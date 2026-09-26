#!/usr/bin/env bash
# Spin a kind cluster, load the operator image, deploy S3Mock demo + S3Endpoint.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER="${KIND_CLUSTER:-s3-mon}"
IMG="${IMG:-s3-mon:dev}"

cd "$ROOT"

echo "==> ensuring kind cluster ${CLUSTER}"
if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  kind create cluster --name "${CLUSTER}"
fi
kubectl cluster-info --context "kind-${CLUSTER}" >/dev/null

echo "==> building image ${IMG}"
docker build -t "${IMG}" --build-arg VERSION="$(tr -d '[:space:]' < .localbuild)" .

echo "==> loading into kind"
kind load docker-image "${IMG}" --name "${CLUSTER}"

# Optional: preload adobe/s3mock + aws-cli if present locally
for img in adobe/s3mock:3.12.0 amazon/aws-cli:2.17.0; do
  if docker image inspect "$img" >/dev/null 2>&1; then
    kind load docker-image "$img" --name "${CLUSTER}" || true
  fi
done

echo "==> apply CRDs"
kubectl apply -f config/crd/bases/

echo "==> apply operator"
kubectl apply -f config/deploy/operator.yaml

echo "==> apply demo (s3mock + secret + S3Endpoint)"
kubectl apply -f config/samples/demo-minio.yaml

echo "==> wait for operator"
kubectl -n s3-mon-system rollout status deploy/s3-mon-controller --timeout=120s

echo "==> wait for s3mock + seed"
kubectl -n s3-mon-demo rollout status deploy/s3mock --timeout=180s || true
kubectl -n s3-mon-demo wait --for=condition=complete job/s3mock-seed --timeout=180s || true

echo "==> S3Endpoint status"
sleep 5
kubectl -n s3-mon-demo get s3endpoint -o wide || true

echo "==> recent operator logs (polling)"
kubectl -n s3-mon-system logs deploy/s3-mon-controller --tail=80

echo "Done. Tail logs with:"
echo "  kubectl -n s3-mon-system logs -f deploy/s3-mon-controller"
