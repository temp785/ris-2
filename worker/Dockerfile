# ----------------------------
# Build stage
# ----------------------------

FROM golang:1.25.1-alpine AS build

RUN apk add --no-cache build-base git musl-dev

WORKDIR /app

# Копируем модули, чтобы закэшировать установленные зависимости.
COPY ./go.mod ./go.sum .
RUN go mod download

# Копируем остальной код.
COPY . .

# Переменные окружения для сборки кода.
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=amd64

# Сборка кода
RUN go build -o main main.go

# ----------------------------
# Run stage
# ----------------------------

FROM alpine:latest

WORKDIR /
COPY --from=build /app/main /

CMD ["/main"]
