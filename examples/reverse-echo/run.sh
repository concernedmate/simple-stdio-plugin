#!/bin/bash

cd ./plugin
go build -o main.exe
cd ..
go run main.go
cd ./plugin
rm main.exe 