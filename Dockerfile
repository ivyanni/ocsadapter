FROM golang:1.13 as builder
WORKDIR /go/src/github.com/ivyanni/ocsadapter/
COPY ./ .
RUN CGO_ENABLED=0 GOOS=linux \
go build -a -installsuffix cgo -v -o bin/ocsadapter ./ocsadapter/cmd/

FROM alpine:3.8
RUN apk --no-cache add ca-certificates
WORKDIR /bin/
COPY --from=builder /go/src/github.com/ivyanni/ocsadapter/bin/ocsadapter .
ENTRYPOINT [ "/bin/ocsadapter" ]
CMD [ "8000" ]
EXPOSE 8000