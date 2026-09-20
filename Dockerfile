# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
ARG SERVICE
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /app/${SERVICE} ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12
ARG SERVICE
# Renamed to a fixed path: ARG is not available to ENTRYPOINT's exec form, so
# every image built from this Dockerfile — whichever SERVICE it was built
# with — runs the same "/app/service" regardless of which binary that is.
COPY --from=build /app/${SERVICE} /app/service

# Both ports are harmless to expose on every image: the gateway only listens
# on 8080, the others only on 9090 (plus 8080 for their tiny admin server).
EXPOSE 8080 9090

ENTRYPOINT ["/app/service"]
