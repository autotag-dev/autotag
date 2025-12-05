FROM alpine:3.23.0

RUN apk add --no-cache git

COPY ./autotag /autotag

ENTRYPOINT [ "/autotag" ]