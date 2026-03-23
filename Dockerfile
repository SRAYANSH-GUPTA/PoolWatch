FROM golang:1.23 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/poolwatch ./cmd/poolwatch

FROM gcr.io/distroless/base-debian12

WORKDIR /app

COPY --from=build /out/poolwatch /app/poolwatch

EXPOSE 8080 6433

ENTRYPOINT ["/app/poolwatch"]
