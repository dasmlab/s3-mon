# Multi-stage: generate CRDs + build manager. CRDs land in the image at /crds
# so CI/GitOps can extract them without a separate make manifests host step.

FROM golang:1.25 AS builder
ENV GOTOOLCHAIN=auto
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /workspace

# Tooling for CRD generation (pinned via go.mod tools pattern / direct install).
RUN go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
COPY hack/boilerplate.go.txt hack/boilerplate.go.txt

# Generate CRDs + DeepCopy into the build context.
RUN controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..." \
 && controller-gen rbac:roleName=manager-role crd webhook paths="./..." \
      output:crd:artifacts:config=config/crd/bases \
 && mkdir -p /out/crds && cp -a config/crd/bases/. /out/crds/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags "-X main.version=${VERSION}" -o manager cmd/main.go

# --- runtime ---
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /out/crds /crds
USER 65532:65532
EXPOSE 8080 8090 8081
ENTRYPOINT ["/manager"]
