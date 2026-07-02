#!/bin/bash

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

cd $SCRIPT_DIR/tests/clients
go build -o main.exe
go build -o main2.exe
cd $SCRIPT_DIR/tests
go test -v ./server_test.go  
cd $SCRIPT_DIR/tests/clients
rm main.exe main2.exe