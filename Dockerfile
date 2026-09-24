FROM alpine:3.24.2

RUN apk add --no-cache git

COPY ./autotag /autotag

ENTRYPOINT [ "/autotag" ]