FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /opentrace ./cmd/opentrace

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /opentrace /opentrace

EXPOSE 8080

VOLUME /data
ENV OPENTRACE_DATA_DIR=/data

ENTRYPOINT ["/opentrace"]
