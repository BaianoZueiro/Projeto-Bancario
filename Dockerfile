FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go schema.sql ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -o /finslet .
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /finslet /finslet
ENTRYPOINT ["/finslet"]
