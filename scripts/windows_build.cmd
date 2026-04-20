@echo off
go build ^
    -trimpath ^
    -ldflags="-s -w" ^
    -tags netgo ^
    -o .\out\http2sun.exe .\cmd\http2sun
