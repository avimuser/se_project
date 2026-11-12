FROM oven/bun:1 AS frontend
WORKDIR /ui
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build

FROM golang:1.27.1-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN go build -o /main .

FROM gcr.io/distroless/static-debian13
COPY --from=builder /main /main
COPY --from=frontend /ui/dist /frontend
CMD ["/main"]
