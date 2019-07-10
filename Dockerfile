FROM golang:1.12 AS build

WORKDIR $GOPATH/src/github.com/Harrison-Miller/xpack-license-monitor

ADD . .

RUN go get -d -v ./...

RUN GOARCH=386 CGO_ENABLED=0 GOOS=linux go build -o xpack-license-monitor -i -v ./...

RUN cp xpack-license-monitor / && cp -r templates /


FROM alpine

COPY --from=build /xpack-license-monitor /

RUN mkdir -p /config /templates
COPY --from=build /templates/ /templates

VOLUME /config

ENV DOMAIN="example.com"

EXPOSE 8080

CMD ["/xpack-license-monitor"]