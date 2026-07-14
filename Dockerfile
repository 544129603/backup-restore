FROM --platform=$BUILDPLATFORM golang:1.23.12 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local
WORKDIR /workspace
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /manager ./cmd/manager && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /webui ./cmd/webui

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /manager /manager
COPY --from=builder /webui /webui
USER 65532:65532
ENTRYPOINT ["/manager"]
