#!/bin/bash
go get -u "github.com/jessevdk/go-flags"
env GOOS=linux GOARCH=amd64 go build -o bin/linux/acp acp.go
env GOODS=osx GOARCH=amd64 go build -o bin/osx/acp acp.go
env GOOS=windows GOARCH=amd64 go build -o bin/windows/acp acp.go
