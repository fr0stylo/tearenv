FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tearenvd ./cmd/tearenvd

FROM scratch

COPY --from=build /out/tearenvd /usr/local/bin/tearenvd
USER 65532:65532
EXPOSE 2222 8080 9090
ENTRYPOINT ["/usr/local/bin/tearenvd"]
CMD ["serve"]
