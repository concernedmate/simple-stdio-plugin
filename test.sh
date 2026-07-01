#!/bin/bash

cd ./tests/clients
go build -o main.exe
go build -o main2.exe
cd ..
go test -v ./server_test.go  
cd ./clients
rm main.exe main2.exe