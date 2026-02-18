# Build stage
FROM golang:1.26 AS build

WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main main.go

# Final stage - distroless
FROM gcr.io/distroless/static-debian12

COPY --from=build /app/main /main
COPY --from=build /src/configs /configs

CMD ["/main"]
