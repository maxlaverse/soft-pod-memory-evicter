ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
WORKDIR /build/
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY ./ ./
ARG TARGETPLATFORM
RUN export GOOS=$(echo $TARGETPLATFORM | cut -d'/' -f1) \
    && export GOARCH=$(echo $TARGETPLATFORM | cut -d'/' -f2) \
    && export CGO_ENABLED=0 \
    && echo "Building for GOOS=$GOOS, GOARCH=$GOARCH" \
    && go build -mod=readonly -o soft-pod-memory-evicter

FROM scratch
ENTRYPOINT ["/soft-pod-memory-evicter"]
COPY --from=builder /build/soft-pod-memory-evicter /soft-pod-memory-evicter
