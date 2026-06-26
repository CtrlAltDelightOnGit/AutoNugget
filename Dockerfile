FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o nugs-dl .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates ffmpeg
WORKDIR /app
COPY --from=builder /build/nugs-dl .
ENTRYPOINT ["/app/nugs-dl"]
CMD ["poll"]
