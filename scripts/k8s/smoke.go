package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const moderationGatewayHealthPath = "/moderation/grpc.health.v1.Health/Check"
var publicHealthPaths = []string{
	"/user.v1.HealthService/Check",
	"/supplier.v1.HealthService/Check",
	"/order.v1.HealthService/Check",
	"/communication.v1.HealthService/Check",
	"/grpc.health.v1.Health/Check",
	moderationGatewayHealthPath,
}
