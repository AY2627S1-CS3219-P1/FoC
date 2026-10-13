module github.com/AY2627S1-CS3219-P1/FoC/communication-service

go 1.27.0

require (
	connectrpc.com/connect v1.21.0
	github.com/AY2627S1-CS3219-P1/FoC/pkg v0.0.0
)

require google.golang.org/protobuf v1.36.12 // indirect

replace github.com/AY2627S1-CS3219-P1/FoC/pkg => ../pkg
