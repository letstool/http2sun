#!/bin/bash

IMAGE_TAG=letstool/http2sun:latest

docker build \
	-t "$IMAGE_TAG" \
       -f build/Dockerfile \
       .
