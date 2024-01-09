FROM golang:1.21 AS builder
WORKDIR /build/
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY ./ ./
RUN CGO_ENABLED=0 go build -mod=readonly -o soft-pod-memory-evicter

FROM scratch
ENTRYPOINT ["/soft-pod-memory-evicter"]
COPY --from=builder /build/soft-pod-memory-evicter /soft-pod-memory-evicter