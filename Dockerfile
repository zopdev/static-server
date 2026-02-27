# Build stage
FROM golang:1.26 AS build

WORKDIR /src

# Copy dependency files first (cached unless go.mod/go.sum change)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code (this layer changes frequently)
COPY . .

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /app/main main.go

# Final stage - distroless
FROM gcr.io/distroless/static-debian12

COPY --from=build /app/main /main
COPY --from=build /src/configs /configs

USER nonroot:nonroot

CMD ["/main"]
