FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /opentrace ./cmd/opentrace

FROM alpine:3.20

RUN apk add --no-cache ca-certificates
COPY --from=builder /opentrace /opentrace
COPY migrations /migrations

EXPOSE 8080

ENTRYPOINT ["/opentrace"]
