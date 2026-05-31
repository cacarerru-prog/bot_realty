# --- сборка ---
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
# Статический бинарник без CGO — поедет в любом минимальном образе.
RUN CGO_ENABLED=0 GOOS=linux go build -o /flatradar ./cmd/bot

# --- финальный образ ---
FROM gcr.io/distroless/static-debian12
WORKDIR /data
COPY --from=build /flatradar /flatradar
# config.json и seen.json монтируются как volume в /data при запуске.
ENV FLATRADAR_CONFIG=/data/config.json
ENTRYPOINT ["/flatradar"]
