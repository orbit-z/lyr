# The docs site needs the Markdown in docs/ and the Lyr module itself
# (go.mod replaces it with ../..), so build from the repository root:
#
#	docker build -f docs/site/Dockerfile -t lyr-docs .
#	docker run -p 8080:8080 lyr-docs
#
# docs/site/Dockerfile.dockerignore applies to that context.

# Build stage: the Go version matches the toolchain line in go.mod.
FROM golang:1.27.3 AS build
WORKDIR /src

# Download modules first, so this layer is cached until go.mod or go.sum change.
COPY go.mod go.sum ./
COPY docs/site/go.mod docs/site/go.sum ./docs/site/
RUN cd docs/site && go mod download

COPY . .
WORKDIR /src/docs/site
# Copy docs/*.md into internal/content/md, so the binary embeds them.
RUN go generate ./internal/content
# No Node or Bun needed: esbuild runs inside the lyr CLI.
RUN CGO_ENABLED=0 go tool lyr build --embed

# Final stage: only the self-contained binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/docs/site/dist/server /server
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
