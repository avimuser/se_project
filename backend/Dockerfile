FROM golang:1.27.1-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /main .

FROM gcr.io/distroless/static-debian13
COPY --from=builder /main /main