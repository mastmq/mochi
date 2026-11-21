FROM golang:1.27-alpine AS builder

RUN apk update
RUN apk add git

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . ./

RUN go build -o /app/mochi ./cmd/docker

FROM alpine

WORKDIR /
COPY --from=builder /app/mochi .

ENTRYPOINT [ "/mochi" ]
CMD ["/cmd/docker", "--config", "config.yaml"]