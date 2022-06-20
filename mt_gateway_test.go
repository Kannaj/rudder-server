package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/require"

	"github.com/rudderlabs/rudder-server/app"
	"github.com/rudderlabs/rudder-server/testhelper/health"
	"github.com/rudderlabs/rudder-server/testhelper/rand"
	whUtil "github.com/rudderlabs/rudder-server/testhelper/webhook"
	"github.com/rudderlabs/rudder-server/testhelper/workspaceConfig"
	"github.com/rudderlabs/rudder-server/utils/types/deployment"
)

func TestMultiTenantGateway(t *testing.T) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-c; cancel() }()
	defer cancel()

	// TODO bootstrap jobsdb

	httpPort, err := freeport.GetFreePort()
	require.NoError(t, err)
	httpAdminPort, err := freeport.GetFreePort()
	require.NoError(t, err)

	webhook := whUtil.NewRecorder()
	t.Cleanup(webhook.Close)

	writeKey := rand.String(27)
	workspaceID := rand.String(27)
	workspaceConfigPath := workspaceConfig.CreateTempFile(t,
		"testdata/mtGatewayWorkspaceConfigTemplate.json",
		map[string]string{
			"webhookUrl":  webhook.Server.URL,
			"writeKey":    writeKey,
			"workspaceId": workspaceID,
		},
	)
	t.Log("workspace config path:", workspaceConfigPath)

	rudderTmpDir, err := os.MkdirTemp("", "rudder_server_*_test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(rudderTmpDir) })

	require.NoError(t, os.Setenv("APP_TYPE", app.GATEWAY))
	require.NoError(t, os.Setenv("RSERVER_GATEWAY_WEB_PORT", strconv.Itoa(httpPort)))
	require.NoError(t, os.Setenv("RSERVER_GATEWAY_ADMIN_WEB_PORT", strconv.Itoa(httpAdminPort)))
	require.NoError(t, os.Setenv("RSERVER_ENABLE_STATS", "false"))
	require.NoError(t, os.Setenv("RSERVER_BACKEND_CONFIG_CONFIG_JSONPATH", workspaceConfigPath))
	require.NoError(t, os.Setenv("RUDDER_TMPDIR", rudderTmpDir))
	require.NoError(t, os.Setenv("HOSTED_MULTITENANT_SERVICE_SECRET", "so-secret"))
	require.NoError(t, os.Setenv("DEPLOYMENT_TYPE", string(deployment.MultiTenantType)))

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx)
	}()

	serviceHealthEndpoint := fmt.Sprintf("http://localhost:%d/health", httpPort)
	t.Log("serviceHealthEndpoint", serviceHealthEndpoint)
	health.WaitUntilReady(ctx, t,
		serviceHealthEndpoint,
		time.Minute,
		250*time.Millisecond,
		"serviceHealthEndpoint",
	)

	<-done
}
