#!/bin/bash

cd clients
go build -o main.exe
cd ..
go test -v ./server_test.go  
cd clients
rm main.exe