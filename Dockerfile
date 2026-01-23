FROM alpine:3.23.2

RUN apk add --no-cache git

COPY ./autotag /autotag

ENTRYPOINT [ "/autotag" ]