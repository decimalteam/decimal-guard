.PHONY: default build 
default: build 
build: cmd/gentx/gentx.go cmd/guard/guard.go
	go install ./cmd/gentx/gentx.go
	go install ./cmd/guard/guard.go
