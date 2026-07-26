package cmd

import "helm-deep-pack/internal/push"

var (
	pushState = newPushState()
)

var pushRun = push.PushImages

var pushCmd = newPushCommand("push [REGISTRY]", &pushState)
