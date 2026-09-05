FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" -o /kube-oom-policy ./cmd/reconciler

FROM scratch
LABEL org.opencontainers.image.title="kube-oom-policy" \
    org.opencontainers.image.description="Configure cgroup v2 group OOM behavior for selected Kubernetes containers" \
    org.opencontainers.image.source="https://github.com/ignacyr/kube-oom-policy" \
    org.opencontainers.image.licenses="MIT"
COPY --from=build /kube-oom-policy /kube-oom-policy
COPY LICENSE /LICENSE
COPY THIRD_PARTY_NOTICES /THIRD_PARTY_NOTICES
USER 0:0
ENTRYPOINT ["/kube-oom-policy"]
