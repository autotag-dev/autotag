FROM alpine:3.24.4

RUN apk add --no-cache git

COPY ./autotag /autotag

ENTRYPOINT [ "/autotag" ]