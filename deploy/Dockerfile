# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=builder /out/server /server

EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/server"]
