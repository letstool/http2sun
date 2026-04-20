#!/bin/bash

go build \
    -trimpath \
    -ldflags="-extldflags -static -s -w" \
    -tags netgo \
    -o ./out/http2sun ./cmd/http2sun
