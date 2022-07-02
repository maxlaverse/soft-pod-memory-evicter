FROM golang:1.18 AS builder
WORKDIR /build/
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY ./ ./
RUN CGO_ENABLED=0 go build -mod=readonly -o pod-soft-memory-evicter

FROM scratch
ENTRYPOINT ["/pod-soft-memory-evicter"]
COPY --from=builder /build/pod-soft-memory-evicter /pod-soft-memory-evicter