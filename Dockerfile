FROM golang:1.25-alpine AS build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /lex ./cmd/lex

FROM alpine:latest
RUN apk add --no-cache git ca-certificates
COPY --from=build /lex /lex
ENV LEX_ROOT=/data
ENV LEX_TRANSPORT=http
ENV LEX_ADDR=:8082
VOLUME /data
EXPOSE 8082
ENTRYPOINT ["/lex", "serve"]
