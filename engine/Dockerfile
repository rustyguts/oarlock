FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 oarlock
USER oarlock
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 9000
ENTRYPOINT ["api"]
