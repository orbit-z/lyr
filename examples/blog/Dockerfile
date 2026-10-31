# Build stage: the Go version matches the toolchain line in go.mod.
FROM golang:1.27.3 AS build
WORKDIR /src

# Download modules first, so this layer is cached until go.mod or go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# No Node or Bun needed: esbuild runs inside the lyr CLI.
RUN CGO_ENABLED=0 go tool lyr build --embed

# Final stage: only the self-contained binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/dist/server /server
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
