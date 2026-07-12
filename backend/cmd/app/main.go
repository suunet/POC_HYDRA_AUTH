package main

import (
	"context"
	"os"

	applog "poc-app-hydra/backend/common/log"
)

func main() {
	logger := applog.New(os.Stdout, "app-service")
	ctx := applog.ContextWithLogger(context.Background(), logger)

	applog.FromContext(ctx).InfoContext(ctx, "app-service starting", "ctx", "bootstrap")
}
