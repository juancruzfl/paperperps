# Production image: built frontend + Go server in one container. Railway builds this
# automatically because it sits at the repo root.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/paperperps ./cmd/paperperps

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=server /out/paperperps /app/paperperps
COPY --from=web /src/web/dist /app/web
ENV WEB_DIR=/app/web
EXPOSE 8080
ENTRYPOINT ["/app/paperperps"]
