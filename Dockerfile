FROM golang:1.25-alpine AS build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lex ./cmd/lex

FROM scratch
COPY --from=build /lex /lex
ENV LEX_TRANSPORT=http
ENV LEX_ADDR=:8082
EXPOSE 8082
ENTRYPOINT ["/lex", "serve"]
