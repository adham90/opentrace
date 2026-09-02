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

# The bare-metal default is 127.0.0.1, which is right for a host install and
# useless in a container: the published port reaches the namespace, finds
# nothing listening on it, and `docker run -p 8080:8080` serves connection
# refused from a container that reports itself healthy. The network namespace
# is the isolation here, so binding all interfaces inside it is the safe form.
ENV OPENTRACE_LISTEN_ADDR=0.0.0.0:8080

ENTRYPOINT ["/opentrace"]
