FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w \
      -X github.com/adham90/opentrace/internal/version.Version=${VERSION} \
      -X github.com/adham90/opentrace/internal/version.Commit=${COMMIT} \
      -X github.com/adham90/opentrace/internal/version.Date=${DATE}" \
    -o /opentrace ./cmd/opentrace

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /opentrace /opentrace

EXPOSE 8080

VOLUME /data
ENV OPENTRACE_DATA_DIR=/data

ENTRYPOINT ["/opentrace"]
