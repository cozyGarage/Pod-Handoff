FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY test/e2e/canary/main.go ./main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /canary ./main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /canary /canary
USER 65532:65532
ENTRYPOINT ["/canary"]
