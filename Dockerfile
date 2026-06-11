# yserve — self-hosted Yjs sync server, single static binary.
#
#   docker build -t yserve .
#   docker run -p 8080:8080 -v yserve-data:/data yserve
#
# The image is FROM scratch: ygo is pure Go (no CGO, SQLite included
# via modernc.org/sqlite), so the binary needs no libc and the final
# image is just the binary plus CA roots.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /yserve ./cmd/yserve

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /yserve /yserve
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/yserve"]
CMD ["-addr", ":8080", "-store", "/data/ygo.db", "-version-interval", "10m", "-keep-versions", "10"]
