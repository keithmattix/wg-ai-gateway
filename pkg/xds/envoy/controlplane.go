/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Inspired by the kgateway CNCF project: https://github.com/kgateway-dev/kgateway/blob/main/pkg/kgateway/setup/controlplane.go#L93

package envoy

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"sync/atomic"

	envoy_service_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	envoy_service_endpoint_v3 "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	envoy_service_listener_v3 "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	envoy_service_route_v3 "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	envoycache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	envoylog "github.com/envoyproxy/go-control-plane/pkg/log"
	envoyresource "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	xdsserver "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"k8s.io/klog/v2"
	"sigs.k8s.io/wg-ai-gateway/pkg/constants"
)

type ControlPlane interface {
	PushXDS(ctx context.Context)
}

type controlPlane struct {
	server          xdsserver.Server
	cache           envoycache.SnapshotCache
	snapshotVersion atomic.Uint64
}

// slogAdapterForEnvoy adapts *slog.Logger to envoylog.Logger interface
type slogAdapterForEnvoy struct {
	logger *slog.Logger
}

// Ensure it implements the interface
var _ envoylog.Logger = (*slogAdapterForEnvoy)(nil)

func (s *slogAdapterForEnvoy) Debugf(format string, args ...any) {
	if s.logger.Enabled(context.Background(), slog.LevelDebug) {
		s.logger.Debug(fmt.Sprintf(format, args...)) //nolint:sloglint // ignore formatting
	}
}

func (s *slogAdapterForEnvoy) Infof(format string, args ...any) {
	if s.logger.Enabled(context.Background(), slog.LevelInfo) {
		s.logger.Info(fmt.Sprintf(format, args...)) //nolint:sloglint // ignore formatting
	}
}

func (s *slogAdapterForEnvoy) Warnf(format string, args ...any) {
	if s.logger.Enabled(context.Background(), slog.LevelWarn) {
		s.logger.Warn(fmt.Sprintf(format, args...)) //nolint:sloglint // ignore formatting
	}
}

func (s *slogAdapterForEnvoy) Errorf(format string, args ...any) {
	if s.logger.Enabled(context.Background(), slog.LevelError) {
		s.logger.Error(fmt.Sprintf(format, args...)) //nolint:sloglint // ignore formatting
	}
}

func NewControlPlane(
	ctx context.Context,
) ControlPlane {
	baseLogger := slog.Default().With("component", "envoy-controlplane")
	envoyLoggerAdapter := &slogAdapterForEnvoy{logger: baseLogger}

	snapshotCache := envoycache.NewSnapshotCache(false, envoycache.IDHash{}, envoyLoggerAdapter)
	xdsServer := xdsserver.NewServer(ctx, snapshotCache, &callbacks{})

	return &controlPlane{
		server:          xdsServer,
		cache:           snapshotCache,
		snapshotVersion: atomic.Uint64{},
	}
}

func (cp *controlPlane) Run(ctx context.Context) error {
	// This is a prototype server, so we don't bother with auth or TLS.
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(math.MaxInt32),
		grpc.StreamInterceptor(
			grpc_middleware.ChainStreamServer(
				grpc_zap.StreamServerInterceptor(zap.NewNop()),
				func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
					slog.Debug("gRPC call", "method", info.FullMethod)
					return handler(srv, ss)
				},
			)),
	}
	grpcServer := grpc.NewServer(opts...)

	// Register reflection
	reflection.Register(grpcServer)

	// Register xDS services
	envoy_service_endpoint_v3.RegisterEndpointDiscoveryServiceServer(grpcServer, cp.server)
	envoy_service_cluster_v3.RegisterClusterDiscoveryServiceServer(grpcServer, cp.server)
	envoy_service_route_v3.RegisterRouteDiscoveryServiceServer(grpcServer, cp.server)
	envoy_service_listener_v3.RegisterListenerDiscoveryServiceServer(grpcServer, cp.server)
	envoy_service_discovery_v3.RegisterAggregatedDiscoveryServiceServer(grpcServer, cp.server)

	// The xDS server listens on a fixed port (15001) on all interfaces.
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", constants.XDSServerPort))
	if err != nil {
		return err
	}

	// Start the server
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			klog.Errorln("Envoy xDS server failed:", err)
		}
	}()

	// Handle graceful shutdown for both servers
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	return nil
}

// PushXDS pushes a new xDS configuration snapshot to all connected Envoy proxies.
// This method creates a new snapshot with the current xDS resources (listeners, routes, clusters, endpoints)
// and pushes it to the snapshot cache. The snapshot is then distributed to all connected Envoy proxies
// that have established xDS streams.
//
// Currently, this implementation creates an empty snapshot as a placeholder. In a complete implementation,
// this would:
// 1. Translate Gateway API resources into Envoy xDS resources
// 2. Build Listeners, Routes, Clusters, and Endpoints from the translated resources
// 3. Create a snapshot with those resources
// 4. Push the snapshot to all registered node IDs
func (cp *controlPlane) PushXDS(ctx context.Context) {
	logger := slog.Default().With("component", "envoy-controlplane")

	// Increment version for this snapshot
	version := cp.snapshotVersion.Add(1)
	versionStr := strconv.FormatUint(version, 10)

	logger.Info("Creating new xDS snapshot", "version", versionStr)

	// TODO: In a complete implementation, this would translate Gateway API resources into xDS resources.
	// For now, create an empty snapshot to establish the pattern.
	resources := map[envoyresource.Type][]types.Resource{
		envoyresource.EndpointType: {},
		envoyresource.ClusterType:  {},
		envoyresource.RouteType:    {},
		envoyresource.ListenerType: {},
	}

	snapshot, err := envoycache.NewSnapshot(versionStr, resources)
	if err != nil {
		logger.Error("Failed to create xDS snapshot", "error", err, "version", versionStr)
		return
	}

	// Push snapshot to all connected nodes
	// GetStatusKeys returns all node IDs that have connected to the xDS server
	nodeIDs := cp.cache.GetStatusKeys()
	if len(nodeIDs) == 0 {
		logger.Debug("No connected Envoy proxies to push xDS configuration to", "version", versionStr)
		return
	}

	logger.Info("Pushing xDS snapshot to connected proxies", "version", versionStr, "nodeCount", len(nodeIDs))

	successCount := 0
	failureCount := 0
	for _, nodeID := range nodeIDs {
		if err := cp.cache.SetSnapshot(ctx, nodeID, snapshot); err != nil {
			logger.Error("Failed to set snapshot for node", "error", err, "nodeID", nodeID, "version", versionStr)
			failureCount++
			continue
		}
		logger.Debug("Successfully pushed snapshot to node", "nodeID", nodeID, "version", versionStr)
		successCount++
	}

	logger.Info("Completed xDS snapshot push",
		"version", versionStr,
		"successCount", successCount,
		"failureCount", failureCount,
		"totalNodes", len(nodeIDs))
}
